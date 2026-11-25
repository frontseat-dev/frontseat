// Package reapi runs actions on a grid that speaks the Remote Execution
// API. It is how frontseat builds: the daemon sends an action's inputs
// and its command, the grid runs it or answers from what it kept, and the
// daemon reads back what the action printed and what it left.
//
// The grid is not frontseat's. Any service that speaks the protocol is
// one, and a daemon speaks to the one it is given: it speaks only the
// standard protocol, and what Buildbarn documents beyond it.
package reapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/balancer"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/client"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/command"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/digest"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/filemetadata"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/uploadinfo"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/build/engine/exec"
	"github.com/frontseat-dev/frontseat/build/engine/exec/access"
	"github.com/frontseat-dev/frontseat/common/confine"
	"github.com/frontseat-dev/frontseat/core/telemetry"
)

// Config says which grid.
type Config struct {
	// Address is the grid's address, host:port.
	Address string
	// Instance is the instance name asked of: the one an action is
	// looked up in first, and the one it is run under.
	Instance string
	// Reads are other instances of the grid an action's result is looked
	// up in, in this order, after Instance and before the action is run;
	// none is run under. What one of them kept is used as any result is
	// (Execute), where the grid holds every file it names. Each is asked
	// once when the grid is dialled, and one the grid does not serve the
	// daemon is refused then (Unread).
	Reads []string
	// Access is how a grid that asks who a daemon is, is asked. Empty,
	// the grid is asked in the clear.
	Access access.Config
	// Log is where the daemon says what a person who reads its log
	// should know of the grid, a line each: an instance of Reads the
	// grid answers other than with what it kept or that it kept nothing,
	// once of each. None, it is said to telemetry alone.
	Log io.Writer
}

// Grid is a grid a daemon runs its actions on.
type Grid struct {
	client *client.Client
	// reads are the other instances an action is looked up in, in order,
	// after the client's own.
	reads []string
	files filemetadata.Cache
	// access is how the grid is asked: its token's file is named where
	// the grid stops admitting the daemon.
	access access.Config
	// log is where what a person should know of the grid is said
	// (Config.Log); told, under mu, the entries of reads said so, by
	// their place.
	log  io.Writer
	mu   sync.Mutex
	told []bool
}

var _ exec.Executor = (*Grid)(nil)

// Dial reaches a grid and asks what it serves. A grid that does not
// answer, or serves no execution, is an error here and not at the first
// action.
func Dial(ctx context.Context, config Config) (*Grid, error) {
	ctx = access.Watch(ctx)
	options, shaken, err := config.Access.Options(telemetry.GRPCOptions(ctx)...)
	if err != nil {
		return nil, fmt.Errorf("reapi: the grid at %s: %w", config.Address, err)
	}
	// As the grid's client dials when it is given parameters
	// (client.NewClient): a pool of connections, each asking up to the
	// client's default of requests at once. It is dialled here, with the
	// options the asset service is dialled with, since what the client
	// adds to its parameters' options would replace their transport's.
	// The address is handed to the dialer as it is, as gRPC's Dial did.
	pool, err := balancer.NewRRConnPool(ctx, client.DefaultMaxConcurrentRequests, func(context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///"+config.Address, options...)
	})
	if err != nil {
		return nil, fmt.Errorf("reapi: the grid at %s: %w", config.Address, err)
	}
	c, err := client.NewClientFromConnection(ctx, config.Instance, pool, pool)
	if err != nil {
		pool.Close()
		return nil, unanswered(ctx, config, shaken, err)
	}
	capabilities, err := c.GetCapabilities(ctx)
	if err != nil {
		c.Close()
		return nil, unanswered(ctx, config, shaken, err)
	}
	if !capabilities.GetExecutionCapabilities().GetExecEnabled() {
		c.Close()
		return nil, fmt.Errorf("reapi: the grid at %s executes nothing for the daemon's own instance (grid.instance)", config.Address)
	}
	if err := readable(ctx, c, config.Reads); err != nil {
		c.Close()
		return nil, fmt.Errorf("reapi: the grid at %s: %w", config.Address, err)
	}
	// A file is read every time it is sent. A daemon outlives a build,
	// and a digest remembered by a file's path would be sent for the file
	// after it changed: the build would be of source that is no longer
	// there.
	return &Grid{client: c, reads: slices.Clone(config.Reads), files: filemetadata.NewNoopCache(), access: config.Access, log: config.Log, told: make([]bool, len(config.Reads))}, nil
}

// nothing is the digest of bytes that are no Action message, and so of
// no action any instance kept: what an instance answers for it says
// whether it is served, and nothing of what it kept.
var nothing = digest.NewFromBlob([]byte("frontseat: no action is these bytes")).ToProto()

// readable asks the grid's cache, once, of each instance a daemon reads,
// and refuses one it answers it does not serve, as Buildbarn answers an
// instance's name it rejects or one it serves no one, and that alone
// (InvalidArgument: its action cache's "Invalid instance name", a
// demultiplexer's "Unknown instance name", bb-storage at ae61334):
// asked again, it would answer the same. Every other answer passes: that
// it kept nothing is the answer of an instance served, and a failure,
// PermissionDenied among it, may pass, Buildbarn answering it for an
// authorizer it cannot reach or a token run out as well as for an
// instance it does not let the daemon read (bb-storage's remoteAuthorizer):
// each action meets it as nothing kept. A grid that keeps every
// instance's results in one cache and lets anyone read it, as the local
// stack's, serves every name, and answers one mistyped as one that kept
// nothing: nothing here tells the two apart.
func readable(ctx context.Context, c *client.Client, reads []string) error {
	for i, instance := range reads {
		_, err := once(ctx, c, instance, nothing)
		if status.Code(err) == codes.InvalidArgument {
			return &unread{entry: i + 1, said: err}
		}
	}
	return nil
}

// Close lets the grid go.
func (g *Grid) Close() error { return g.client.Close() }

// named holds what a stored directory lists to being names: each is one
// file name, and no two are the same.
func named(d *repb.Directory) error {
	if d == nil {
		return fmt.Errorf("stored directory is missing")
	}
	seen := map[string]bool{}
	check := func(name string) error {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
			return fmt.Errorf("%q is not a file name", name)
		}
		if seen[name] {
			return fmt.Errorf("it lists %q twice", name)
		}
		seen[name] = true
		return nil
	}
	for _, file := range d.GetFiles() {
		if err := check(file.GetName()); err != nil {
			return err
		}
		if err := requiredDigest(file.GetName(), file.GetDigest()); err != nil {
			return err
		}
	}
	for _, link := range d.GetSymlinks() {
		if err := check(link.GetName()); err != nil {
			return err
		}
	}
	for _, child := range d.GetDirectories() {
		if err := check(child.GetName()); err != nil {
			return err
		}
		if err := requiredDigest(child.GetName(), child.GetDigest()); err != nil {
			return err
		}
	}
	return nil
}

// The SDK digest decoder assumes a present message. Validate at the common
// stored-directory boundary so listing, merging and restoring all refuse
// missing or malformed digests before calling it.
func requiredDigest(name string, value *repb.Digest) error {
	if value == nil {
		return fmt.Errorf("stored entry %q has no digest", name)
	}
	_, err := digest.NewFromProto(value)
	if err != nil {
		return fmt.Errorf("stored entry %q: %w", name, err)
	}
	return nil
}

var _ exec.Keyed = (*Grid)(nil)

// Key implements exec.Keyed: the digest of the action as the grid is
// sent it, which is everything that decides its answer. Nothing is
// asked of the grid, and no file is read that the action's files
// already answer for.
func (g *Grid) Key(ctx context.Context, action exec.Action) (string, error) {
	described, _, _, err := g.described(ctx, action)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d", described.Digest.Hash, described.Digest.Size), nil
}

// described returns an action as the grid is sent it, the command it
// names, and everything else the grid must hold for it.
func (g *Grid) described(ctx context.Context, action exec.Action) (described, named *uploadinfo.Entry, blobs []*uploadinfo.Entry, err error) {
	root, blobs, err := g.root(ctx, action)
	if err != nil {
		return nil, nil, nil, err
	}
	cmd := &repb.Command{
		Arguments:        action.Arguments,
		WorkingDirectory: action.WorkingDirectory,
		OutputPaths:      sorted(action.Outputs),
	}
	for _, name := range keys(action.Environment) {
		cmd.EnvironmentVariables = append(cmd.EnvironmentVariables, &repb.Command_EnvironmentVariable{Name: name, Value: action.Environment[name]})
	}
	if named, err = uploadinfo.EntryFromProto(cmd); err != nil {
		return nil, nil, nil, err
	}
	act := &repb.Action{
		CommandDigest:   named.Digest.ToProto(),
		InputRootDigest: root.ToProto(),
		Platform:        &repb.Platform{},
	}
	for _, name := range keys(action.Platform) {
		act.Platform.Properties = append(act.Platform.Properties, &repb.Platform_Property{Name: name, Value: action.Platform[name]})
	}
	if described, err = uploadinfo.EntryFromProto(act); err != nil {
		return nil, nil, nil, err
	}
	return described, named, blobs, nil
}

// Execute implements exec.Executor.
func (g *Grid) Execute(ctx context.Context, action exec.Action) (result exec.Result, err error) {
	if len(action.Arguments) == 0 {
		return exec.Result{}, errors.New("reapi: the action runs nothing")
	}
	start := time.Now()
	ctx, span := telemetry.Start(access.Watch(ctx), "grid", attribute.String("label", action.Label))
	defer func() {
		err = g.said(ctx, executionError(err))
		telemetry.End(span, err)
	}()
	rooting, rooted := telemetry.Start(ctx, "grid.root")
	actionEntry, commandEntry, blobs, err := g.described(rooting, action)
	rooted.SetAttributes(attribute.Int("blobs", len(blobs)))
	telemetry.End(rooted, err)
	if err != nil {
		return exec.Result{}, err
	}
	sending, sent := telemetry.Start(ctx, "grid.send")
	missing, moved, err := g.client.UploadIfMissing(sending, append(blobs, commandEntry, actionEntry)...)
	sent.SetAttributes(attribute.Int("missing", len(missing)), attribute.Int64("bytes", moved))
	telemetry.End(sent, err)
	if err != nil {
		// What could not be sent may be a file that is no longer what the
		// run found: whoever holds the files is asked again, and says so.
		if action.Files != nil {
			for _, blob := range blobs {
				if blob.Path == "" {
					continue
				}
				if _, _, _, changed := action.Files.File(blob.Path); changed != nil {
					err = changed
					break
				}
			}
		}
		return exec.Result{}, fmt.Errorf("reapi: %s: sending its inputs: %w", action.Label, err)
	}

	// The grid's cache is asked first: a scheduler runs what it is sent,
	// and it is the client that knows an answer is already kept. A fresh
	// action is asked of no cache: that is what makes it fresh, the
	// daemon not looking. The grid is sent skip_cache_lookup too, which
	// Buildbarn's scheduler does not read, since it looks in no cache;
	// it may join the action to a run of the same digest under the same
	// instance that is under way, which is a run of it all the same.
	var done *repb.ActionResult
	if !action.Fresh {
		if done, err = g.kept(ctx, actionEntry.Digest); err != nil {
			return exec.Result{}, fmt.Errorf("reapi: %s: %w", action.Label, err)
		}
	}
	cached := done != nil
	if done == nil {
		running, ran := telemetry.Start(ctx, "grid.run")
		done, cached, err = g.run(running, action.Label, actionEntry.Digest, action.Fresh)
		telemetry.End(ran, err)
		if err != nil {
			return exec.Result{}, err
		}
	}
	span.SetAttributes(attribute.Bool("cached", cached))
	result = exec.Result{
		ExitCode: int(done.GetExitCode()),
		Cached:   cached,
		Action:   exec.Digest{Hash: actionEntry.Digest.Hash, Size: actionEntry.Digest.Size},
	}
	// The worker says when it ran the action and for how long. What a
	// cache answered with was run for another, at another time.
	if ran := done.GetExecutionMetadata(); !cached && ran.GetExecutionStartTimestamp().IsValid() && ran.GetExecutionCompletedTimestamp().IsValid() {
		result.Started = ran.GetExecutionStartTimestamp().AsTime()
		result.Worked = ran.GetExecutionCompletedTimestamp().AsTime().Sub(result.Started)
	}
	if result.Stdout, err = g.printed(ctx, done.GetStdoutRaw(), done.GetStdoutDigest()); err != nil {
		return exec.Result{}, fmt.Errorf("reapi: %s: its output: %w", action.Label, err)
	}
	if result.Stderr, err = g.printed(ctx, done.GetStderrRaw(), done.GetStderrDigest()); err != nil {
		return exec.Result{}, fmt.Errorf("reapi: %s: its output: %w", action.Label, err)
	}
	if result.Outputs, err = g.outputs(ctx, done); err != nil {
		return exec.Result{}, fmt.Errorf("reapi: %s: its outputs: %w", action.Label, err)
	}
	result.Duration = time.Since(start)
	return result, nil
}

// kept is what the grid's cache kept of an action: under the daemon's
// own instance, and else under each instance it reads, in the order it
// was given them; nil where none kept it.
//
// What another instance kept is used as what the daemon's own kept is,
// and its files are read under the daemon's own instance, the grid
// keeping files by their digest alone (Buildbarn's storage does). It is
// a result a worker of the grid wrote, as every result is, since no
// client may write the grid's cache: whoever reads an instance trusts
// the workers that run its actions as its own, which is the trust the
// owner accepted in having main's builds read what branches and
// workspaces built.
//
// An instance read is a shortcut, and running the action is always
// right: what one answers other than a result is taken for nothing kept,
// and so is a result that names a file the grid does not hold under the
// daemon's own instance (held). The daemon's own instance failing fails
// the action, as it did before any other was read.
func (g *Grid) kept(ctx context.Context, action digest.Digest) (*repb.ActionResult, error) {
	done, err := g.client.GetActionResult(ctx, &repb.GetActionResultRequest{
		InstanceName: g.client.InstanceName,
		ActionDigest: action.ToProto(),
	})
	switch {
	case err == nil:
		return done, nil
	case status.Code(err) != codes.NotFound:
		return nil, fmt.Errorf("asking the cache: %w", err)
	}
	for i, instance := range g.reads {
		done, err := once(ctx, g.client, instance, action.ToProto())
		if err != nil {
			g.missed(ctx, i+1, err)
			continue
		}
		whole, err := g.held(ctx, done)
		if err != nil {
			return nil, fmt.Errorf("asking the grid for what grid.reads' entry %d kept of it: %w", i+1, err)
		}
		if whole {
			return done, nil
		}
	}
	return nil, nil
}

// missed tells what an instance read answered, by its entry of
// grid.reads, where it answered other than that it kept nothing: to the
// action's span each time, and in the daemon's log and to telemetry once
// of each entry for as long as the daemon runs, since every action meets
// it and a person would find each of their actions run, not why. Said
// by the entry and the code alone: the instance's name and the grid's
// words, which repeat it, are not. An action cancelled is no answer.
func (g *Grid) missed(ctx context.Context, entry int, err error) {
	code := status.Code(err)
	if code == codes.NotFound || ctx.Err() != nil {
		return
	}
	trace.SpanFromContext(ctx).AddEvent("grid.reads unanswered", trace.WithAttributes(attribute.Int("entry", entry), attribute.String("code", code.String())))
	g.mu.Lock()
	first := !g.told[entry-1]
	g.told[entry-1] = true
	g.mu.Unlock()
	if !first {
		return
	}
	said := fmt.Sprintf("the grid answered %s when asked what grid.reads' entry %d kept: every action is looked up there as one it kept nothing of, and run where nothing else kept it; said once, while the daemon runs", code, entry)
	telemetry.Message(ctx, log.SeverityWarn, said)
	if g.log != nil {
		fmt.Fprintln(g.log, "frontseat:", said)
	}
}

// once asks the grid's cache of an instance the daemon reads what it
// kept of an action, once: what it answers other than a result is
// nothing kept, and asking it again, as the client asks its own instance
// again, would hold up each action for a shortcut.
func once(ctx context.Context, c *client.Client, instance string, action *repb.Digest) (done *repb.ActionResult, err error) {
	err = c.CallWithTimeout(ctx, "GetActionResult", func(ctx context.Context) error {
		done, err = repb.NewActionCacheClient(c.Connection()).GetActionResult(ctx, &repb.GetActionResultRequest{InstanceName: instance, ActionDigest: action}, c.RPCOpts()...)
		return err
	})
	return done, err
}

// held is whether the grid holds, under the daemon's own instance, every
// file a result names: what the action printed where it was not answered
// in the result itself, each file it left, the tree of each directory it
// left and every file in that tree. The directories of a tree are not
// asked for: the daemon stores each from the tree (outputs). A file of
// no bytes is held by every grid; a result that names a file or a tree
// by no digest, or by one that is none, is none to use, and so is one
// whose tree the grid lets go of before it is read. Only the grid
// failing to answer otherwise is an error.
func (g *Grid) held(ctx context.Context, done *repb.ActionResult) (bool, error) {
	var named []*repb.Digest
	// What was printed is named by no digest where nothing was printed.
	if printed := done.GetStdoutDigest(); len(done.GetStdoutRaw()) == 0 && printed != nil {
		named = append(named, printed)
	}
	if printed := done.GetStderrDigest(); len(done.GetStderrRaw()) == 0 && printed != nil {
		named = append(named, printed)
	}
	for _, file := range done.GetOutputFiles() {
		named = append(named, file.GetDigest())
	}
	var trees []digest.Digest
	for _, directory := range done.GetOutputDirectories() {
		tree, sound := digestOf(directory.GetTreeDigest())
		if !sound {
			return false, nil
		}
		named = append(named, directory.GetTreeDigest())
		trees = append(trees, tree)
	}
	if whole, err := g.holds(ctx, named); err != nil || !whole {
		return false, err
	}
	named = named[:0]
	for _, at := range trees {
		tree := &repb.Tree{}
		if _, err := g.client.ReadProto(ctx, at, tree); err != nil {
			// Held when it was asked of, and let go of since: the grid
			// holds it no longer, and the action is run.
			if status.Code(err) == codes.NotFound {
				return false, nil
			}
			return false, err
		}
		for _, directory := range append([]*repb.Directory{tree.GetRoot()}, tree.GetChildren()...) {
			for _, file := range directory.GetFiles() {
				named = append(named, file.GetDigest())
			}
		}
	}
	return g.holds(ctx, named)
}

// holds is whether the grid holds every file of these digests under the
// daemon's own instance. The empty blob's digest is held by every grid;
// no digest, and one that is none (of no bytes and another hash among
// it), is held by none.
func (g *Grid) holds(ctx context.Context, named []*repb.Digest) (bool, error) {
	var asked []digest.Digest
	for _, each := range named {
		file, sound := digestOf(each)
		switch {
		case !sound:
			return false, nil
		case file == digest.Empty:
			continue
		}
		asked = append(asked, file)
	}
	if len(asked) == 0 {
		return true, nil
	}
	missing, err := g.client.MissingBlobs(ctx, asked)
	return len(missing) == 0, err
}

// digestOf is the digest a result names, and whether it is one: given, a
// hash of the grid's digest function, and of no bytes only where its
// hash is the empty blob's.
func digestOf(given *repb.Digest) (digest.Digest, bool) {
	if given == nil {
		return digest.Digest{}, false
	}
	file, err := digest.NewFromProto(given)
	if err != nil || (file.Size == 0 && file != digest.Empty) {
		return digest.Digest{}, false
	}
	return file, true
}

// run has the grid execute an action and returns its answer, and whether
// the grid answered from what it kept after all. One that is fresh is
// sent skip_cache_lookup, and a grid that answers it from its cache all
// the same (cached_result) is refused: what a release ships is made for
// it.
func (g *Grid) run(ctx context.Context, label string, action digest.Digest, fresh bool) (*repb.ActionResult, bool, error) {
	operation, err := g.client.ExecuteAndWait(ctx, &repb.ExecuteRequest{
		InstanceName:    g.client.InstanceName,
		ActionDigest:    action.ToProto(),
		SkipCacheLookup: fresh,
	})
	if err != nil {
		return nil, false, fmt.Errorf("reapi: %s: %w", label, err)
	}
	response := &repb.ExecuteResponse{}
	if err := operation.GetResponse().UnmarshalTo(response); err != nil {
		return nil, false, fmt.Errorf("reapi: %s: the grid's answer: %w", label, err)
	}
	if said := status.FromProto(response.GetStatus()); said.Code() != codes.OK {
		if gone(said) {
			return nil, false, fmt.Errorf("reapi: %s: %s: %w", label, said.Message(), exec.ErrNotKept)
		}
		if confiner, lacks := lacking(said); lacks {
			return nil, false, fmt.Errorf("reapi: %s: the worker could not start %s, the frontseat-confine every action of this daemon is started by, for something it did not find: where the worker has no confiner of this form, install it beside the runner (the local stack's installer, or `frontseat-confine install /bb`); where it has, the directory the action runs in was not there: %w", label, confiner, said.Err())
		}
		// The call was answered: this is what the grid says became of the
		// action where it ran, a worker's own refusal among it.
		return nil, false, fmt.Errorf("reapi: %s: where it ran: %w", label, &ran{said.Err()})
	}
	if fresh && response.GetCachedResult() {
		return nil, false, fmt.Errorf("reapi: %s: the grid answered it from its cache (cached_result), and it is fresh, told to answer from none (skip_cache_lookup): what a release ships is made for it, never taken from what was kept", label)
	}
	return response.GetResult(), response.GetCachedResult(), nil
}

// executionError gives transport cancellation the Executor's context meaning
// while preserving the grid's status and detail. Scheduling need not know gRPC.
func executionError(err error) error {
	var cause error
	switch status.Code(err) {
	case codes.Canceled:
		cause = context.Canceled
	case codes.DeadlineExceeded:
		cause = context.DeadlineExceeded
	default:
		return err
	}
	if errors.Is(err, cause) {
		return err
	}
	return fmt.Errorf("%w: %w", err, cause)
}

// lacking is the frontseat-confine a worker could not start an action by
// for something it did not find, where that is what the grid says:
// Frontseat's runner (frontseat-runner serve) answers an argument that is
// not so, and names the program it could not start and why, as bb_runner
// did. That is the confiner missing, or the directory
// the action runs in, which the process reports as the program's when it
// cannot change to it; both are said, and neither is taken for the other.
// Another failure to start a program, or to start this one, is not taken
// for it.
func lacking(said *status.Status) (string, bool) {
	if said.Code() != codes.InvalidArgument {
		return "", false
	}
	_, after, found := strings.Cut(said.Message(), "fork/exec "+confine.Confiners)
	if !found {
		return "", false
	}
	form, why, found := strings.Cut(after, ": ")
	if !found || !strings.Contains(why, "no such file or directory") {
		return "", false
	}
	return confine.Confiners + form, true
}

// gone is whether a grid that did not run an action says it no longer
// holds something the action was handed. The Remote Execution API has a
// grid say so as a precondition that failed, with what is missing; one
// whose workers read an action's root as the action does, as Buildbarn's
// do, says what its storage said when the read came: not found.
func gone(said *status.Status) bool {
	switch said.Code() {
	case codes.NotFound:
		return true
	case codes.FailedPrecondition:
		for _, detail := range said.Details() {
			failure, is := detail.(*errdetails.PreconditionFailure)
			if !is {
				continue
			}
			for _, violation := range failure.GetViolations() {
				if violation.GetType() == "MISSING" {
					return true
				}
			}
		}
	}
	return false
}

// root builds the action's root: its inputs as they are under the
// action's Root, and what the grid holds already, each at its path. It
// returns the root's digest and everything the grid must hold for it but
// what it holds already.
func (g *Grid) root(ctx context.Context, action exec.Action) (digest.Digest, []*uploadinfo.Entry, error) {
	return g.rootAt(ctx, action, 0)
}

func (g *Grid) rootAt(ctx context.Context, action exec.Action, depth int) (digest.Digest, []*uploadinfo.Entry, error) {
	if depth > 8 {
		return digest.Digest{}, nil, fmt.Errorf("reapi: prepared input roots are nested too deeply")
	}
	inputs := &command.InputSpec{Inputs: action.Inputs}
	for _, at := range action.Directories {
		if !filepath.IsLocal(at) || filepath.ToSlash(filepath.Clean(at)) != at {
			return digest.Digest{}, nil, fmt.Errorf("reapi: an input directory is not a plain local path")
		}
		inputs.VirtualInputs = append(inputs.VirtualInputs, &command.VirtualInput{Path: at, IsEmptyDirectory: true})
	}
	for _, at := range keys(action.Written) {
		inputs.VirtualInputs = append(inputs.VirtualInputs, &command.VirtualInput{Path: at, Contents: action.Written[at].Content, IsExecutable: action.Written[at].Executable})
	}
	// Where its command runs is there whether or not an input is: a worker
	// makes no directory of the root's for it.
	if action.WorkingDirectory != "" {
		inputs.VirtualInputs = append(inputs.VirtualInputs, &command.VirtualInput{Path: action.WorkingDirectory, IsEmptyDirectory: true})
	}
	files := g.files
	if action.Files != nil {
		files = held{action.Files}
	}
	root, blobs, _, err := g.client.ComputeMerkleTree(ctx, action.Root, "", "", inputs, files)
	if err != nil {
		return digest.Digest{}, nil, fmt.Errorf("reapi: %s: its inputs: %w", action.Label, said(err))
	}
	if len(action.Held) == 0 && len(action.Copies) == 0 && len(action.Prepared) == 0 {
		return root, blobs, nil
	}
	// A copy is a tree of its own, built from where its files are and
	// placed as something held is.
	held := make(map[string]exec.Held, len(action.Held)+len(action.Copies)+len(action.Prepared))
	for at, each := range action.Held {
		held[at] = each
	}
	for _, at := range keys(action.Copies) {
		copied, more, _, err := g.client.ComputeMerkleTree(ctx, action.Copies[at].From, "", "", &command.InputSpec{Inputs: action.Copies[at].Files}, files)
		if err != nil {
			return digest.Digest{}, nil, fmt.Errorf("reapi: %s: what it is handed at %s: %w", action.Label, at, said(err))
		}
		blobs = append(blobs, more...)
		held[at] = exec.Held{Digest: exec.Digest{Hash: copied.Hash, Size: copied.Size}, Directory: true}
	}
	for _, at := range keys(action.Prepared) {
		if !filepath.IsLocal(at) || filepath.ToSlash(filepath.Clean(at)) != at || at == "." {
			return digest.Digest{}, nil, fmt.Errorf("reapi: a prepared input root is not a plain child directory")
		}
		if _, exists := held[at]; exists {
			return digest.Digest{}, nil, fmt.Errorf("reapi: a prepared root replaces another input")
		}
		prepared, more, err := g.rootAt(ctx, action.Prepared[at], depth+1)
		if err != nil {
			return digest.Digest{}, nil, err
		}
		blobs = append(blobs, more...)
		held[at] = exec.Held{Digest: exec.Digest{Hash: prepared.Hash, Size: prepared.Size}, Directory: true}
	}
	// The directories of the inputs, by digest: what is held is placed
	// among them, and each directory on the way to it is made again.
	made := &directories{label: action.Label, byDigest: map[digest.Digest]*repb.Directory{}}
	for _, blob := range blobs {
		if !blob.IsBlob() {
			continue
		}
		directory := &repb.Directory{}
		if proto.Unmarshal(blob.Contents, directory) == nil {
			made.byDigest[blob.Digest] = directory
		}
	}
	top := made.byDigest[root]
	if top == nil {
		top = &repb.Directory{}
	}
	for _, at := range keys(held) {
		if top, err = made.place(top, at, strings.Split(path.Clean(at), "/"), held[at]); err != nil {
			return digest.Digest{}, nil, err
		}
	}
	entry, err := uploadinfo.EntryFromProto(top)
	if err != nil {
		return digest.Digest{}, nil, err
	}
	return entry.Digest, append(append(blobs, made.entries...), entry), nil
}

// held answers what a file holds from what an action's files held when
// the run began, in place of reading the file as it is now.
type held struct{ files exec.Files }

var _ filemetadata.Cache = held{}

// Get implements filemetadata.Cache. A file the run did not find is an
// error and no file that is not there: one that is not there is left
// out of a root without a word.
func (h held) Get(path string) *filemetadata.Metadata {
	hash, size, executable, err := h.files.File(path)
	if err != nil {
		return &filemetadata.Metadata{Err: &filemetadata.FileError{Err: err}}
	}
	return &filemetadata.Metadata{Digest: digest.Digest{Hash: hash, Size: size}, IsExecutable: executable}
}

// Delete implements filemetadata.Cache: nothing is kept here.
func (held) Delete(string) error { return nil }

// Update implements filemetadata.Cache: nothing is kept here.
func (held) Update(string, *filemetadata.Metadata) error { return nil }

// GetCacheHits implements filemetadata.Cache.
func (held) GetCacheHits() uint64 { return 0 }

// GetCacheMisses implements filemetadata.Cache.
func (held) GetCacheMisses() uint64 { return 0 }

// said returns what was said of a file in place of the error it was
// carried in, which hides it from whoever asks what went wrong.
func said(err error) error {
	var carried *filemetadata.FileError
	if errors.As(err, &carried) && carried.Err != nil {
		return carried.Err
	}
	return err
}

// directories are the directories of an action's root while what the
// grid holds is placed in it.
type directories struct {
	label    string
	byDigest map[digest.Digest]*repb.Directory
	// entries are the directories made on the way.
	entries []*uploadinfo.Entry
}

// place returns a directory with something the grid holds at a path
// beneath it. The directories on the way are those there are, made again
// to hold it, or new ones where there are none.
func (d *directories) place(in *repb.Directory, at string, segments []string, held exec.Held) (*repb.Directory, error) {
	out := proto.Clone(in).(*repb.Directory)
	name := segments[0]
	for _, file := range out.Files {
		if file.Name == name {
			return nil, fmt.Errorf("reapi: %s: what is held at %s sits where an input does", d.label, at)
		}
	}
	child := -1
	for i, directory := range out.Directories {
		if directory.Name == name {
			child = i
		}
	}
	dg := &repb.Digest{Hash: held.Digest.Hash, SizeBytes: held.Digest.Size}
	switch {
	case len(segments) > 1:
		below := &repb.Directory{}
		if child >= 0 {
			there, err := digest.NewFromProto(out.Directories[child].Digest)
			if err != nil {
				return nil, err
			}
			if below = d.byDigest[there]; below == nil {
				return nil, fmt.Errorf("reapi: %s: what is held at %s sits inside something else that is held", d.label, at)
			}
			out.Directories = append(out.Directories[:child], out.Directories[child+1:]...)
		}
		placed, err := d.place(below, at, segments[1:], held)
		if err != nil {
			return nil, err
		}
		entry, err := uploadinfo.EntryFromProto(placed)
		if err != nil {
			return nil, err
		}
		d.entries = append(d.entries, entry)
		d.byDigest[entry.Digest] = placed
		out.Directories = append(out.Directories, &repb.DirectoryNode{Name: name, Digest: entry.Digest.ToProto()})
	case child >= 0:
		return nil, fmt.Errorf("reapi: %s: what is held at %s sits where an input does", d.label, at)
	case held.Directory:
		out.Directories = append(out.Directories, &repb.DirectoryNode{Name: name, Digest: dg})
	default:
		out.Files = append(out.Files, &repb.FileNode{Name: name, Digest: dg, IsExecutable: held.Executable})
	}
	sort.Slice(out.Directories, func(i, j int) bool { return out.Directories[i].Name < out.Directories[j].Name })
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Name < out.Files[j].Name })
	return out, nil
}

// Materialize implements exec.Materializing: the directory is written
// beside its path and moved into place, so it is there whole or not at
// all.
func (g *Grid) Materialize(ctx context.Context, directory exec.Held, into string) (err error) {
	ctx = access.Watch(ctx)
	defer func() { err = g.said(ctx, err) }()
	if !directory.Directory {
		return g.file(ctx, directory, into)
	}
	// The tree is walked as a listing is, each name and digest held to
	// the contract, with the links a tool's tree has within itself; the
	// files are then fetched together. A grid need not serve whole trees
	// at once.
	root := digest.Digest{Hash: directory.Digest.Hash, Size: directory.Digest.Size}
	files, links, dirs, err := tree(ctx, g.directoryStore(), root)
	if err != nil {
		return executionError(err)
	}
	outputs := make(map[digest.Digest]*client.TreeOutput, len(files))
	for at, file := range files {
		dg := digest.Digest{Hash: file.Digest.Hash, Size: file.Digest.Size}
		if _, there := outputs[dg]; there {
			// One content at two paths is fetched once, and copied.
			outputs[dg].Path = outputs[dg].Path + "\x00" + at
			continue
		}
		outputs[dg] = &client.TreeOutput{Digest: dg, Path: at, IsExecutable: file.Executable}
	}
	beside, err := os.MkdirTemp(filepath.Dir(into), "."+filepath.Base(into)+"-")
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err == nil {
			err = os.MkdirAll(filepath.Join(beside, filepath.FromSlash(dir)), 0o755)
		}
	}
	if err == nil {
		err = g.download(ctx, beside, outputs)
	}
	for at, to := range links {
		if err == nil {
			err = os.MkdirAll(filepath.Dir(filepath.Join(beside, filepath.FromSlash(at))), 0o755)
		}
		if err == nil {
			err = os.Symlink(filepath.FromSlash(to), filepath.Join(beside, filepath.FromSlash(at)))
		}
	}
	// Each link, followed on the disk through whatever other links it
	// leads through, leads within the tree, or the tree is refused: a
	// chain of links can lead where no one link does.
	if err == nil {
		err = linksWithin(beside, links)
	}
	// What was brought beside is its own to delete: a directory made
	// here, for this one tree, which nothing else names.
	if err != nil {
		os.RemoveAll(beside)
		if status.Code(err) == codes.NotFound {
			return fmt.Errorf("%s: %w", directory.Digest.Hash, exec.ErrNotKept)
		}
		return err
	}
	if err := os.Rename(beside, into); err != nil {
		os.RemoveAll(beside)
		// Another brought the same tree there first: it is the same.
		if _, there := os.Stat(into); there == nil {
			return nil
		}
		return err
	}
	return nil
}

// linksWithin holds every link of a materialized tree to leading within
// it on the disk, every link on the way followed.
func linksWithin(root string, links map[string]string) error {
	here, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for at := range links {
		led, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(at)))
		if err != nil {
			// A link to nothing leads nowhere, and harms nothing.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("the tree's link at %s: %w", at, err)
		}
		if rel, err := filepath.Rel(here, led); err != nil || rel != "." && !filepath.IsLocal(rel) {
			return fmt.Errorf("the tree holds a link at %s that leads out of it, to %s", at, led)
		}
	}
	return nil
}

// file writes a file the grid holds to a path, beside it first and
// moved into place, so it is there whole or not at all.
func (g *Grid) file(ctx context.Context, held exec.Held, into string) error {
	beside, err := os.MkdirTemp(filepath.Dir(into), "."+filepath.Base(into)+"-")
	if err != nil {
		return err
	}
	// Its own to delete: a directory made here, for this one file.
	defer os.RemoveAll(beside)
	dg := digest.Digest{Hash: held.Digest.Hash, Size: held.Digest.Size}
	if err := g.download(ctx, beside, map[digest.Digest]*client.TreeOutput{dg: {Digest: dg, Path: "file", IsExecutable: held.Executable}}); err != nil {
		if status.Code(err) == codes.NotFound {
			return fmt.Errorf("%s: %w", held.Digest.Hash, exec.ErrNotKept)
		}
		return executionError(err)
	}
	return os.Rename(filepath.Join(beside, "file"), into)
}

// download fetches files into a directory: each digest once, at the
// first of its paths, then copied to the rest.
func (g *Grid) download(ctx context.Context, into string, outputs map[digest.Digest]*client.TreeOutput) error {
	first := make(map[digest.Digest]*client.TreeOutput, len(outputs))
	rest := map[string][]string{}
	for dg, output := range outputs {
		paths := strings.Split(output.Path, "\x00")
		first[dg] = &client.TreeOutput{Digest: dg, Path: paths[0], IsExecutable: output.IsExecutable}
		rest[paths[0]] = paths[1:]
	}
	if _, err := g.client.DownloadFiles(ctx, into, first); err != nil {
		return err
	}
	for from, copies := range rest {
		for _, to := range copies {
			content, err := os.ReadFile(filepath.Join(into, filepath.FromSlash(from)))
			if err != nil {
				return err
			}
			info, err := os.Stat(filepath.Join(into, filepath.FromSlash(from)))
			if err != nil {
				return err
			}
			at := filepath.Join(into, filepath.FromSlash(to))
			if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(at, content, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

// printed returns what an action printed: carried in the answer, or read
// by its digest.
func (g *Grid) printed(ctx context.Context, raw []byte, dg *repb.Digest) ([]byte, error) {
	if len(raw) > 0 || dg.GetSizeBytes() == 0 {
		return raw, nil
	}
	read, err := digest.NewFromProto(dg)
	if err != nil {
		return nil, err
	}
	blob, _, err := g.client.ReadBlob(ctx, read)
	return blob, err
}

// outputs returns what an action left. The grid answers a directory as
// one message holding every directory under it; each is stored on its
// own here, so that the directory can be a tree of a later action.
func (g *Grid) outputs(ctx context.Context, done *repb.ActionResult) (map[string]exec.Held, error) {
	outputs := map[string]exec.Held{}
	for _, file := range done.GetOutputFiles() {
		outputs[file.GetPath()] = exec.Held{
			Digest:     exec.Digest{Hash: file.GetDigest().GetHash(), Size: file.GetDigest().GetSizeBytes()},
			Executable: file.GetIsExecutable(),
		}
	}
	for _, directory := range done.GetOutputDirectories() {
		read, err := digest.NewFromProto(directory.GetTreeDigest())
		if err != nil {
			return nil, err
		}
		tree := &repb.Tree{}
		if _, err := g.client.ReadProto(ctx, read, tree); err != nil {
			return nil, err
		}
		root, err := uploadinfo.EntryFromProto(tree.GetRoot())
		if err != nil {
			return nil, err
		}
		entries := []*uploadinfo.Entry{root}
		for _, child := range tree.GetChildren() {
			entry, err := uploadinfo.EntryFromProto(child)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		if _, _, err := g.client.UploadIfMissing(ctx, entries...); err != nil {
			return nil, err
		}
		outputs[directory.GetPath()] = exec.Held{Digest: exec.Digest{Hash: root.Digest.Hash, Size: root.Digest.Size}, Directory: true}
	}
	return outputs, nil
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
