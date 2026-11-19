// Package reapi runs actions on a grid that speaks the Remote Execution
// API. It is how frontseat builds: the daemon sends an action's inputs
// and its command, the grid runs it or answers from what it kept, and the
// daemon reads back what the action printed and what it left.
//
// The grid is not frontseat's. Any service that speaks the protocol is
// one, and a daemon speaks to the one it is given.
package reapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/client"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/command"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/digest"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/filemetadata"
	"github.com/bazelbuild/remote-apis-sdks/go/pkg/uploadinfo"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/server/internal/confine"
	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

// Config says which grid.
type Config struct {
	// Address is the grid's address, host:port.
	Address string
	// Instance is the instance name asked of.
	Instance string
}

// Grid is a grid a daemon runs its actions on.
type Grid struct {
	client *client.Client
	files  filemetadata.Cache
}

var _ exec.Executor = (*Grid)(nil)

// Dial reaches a grid and asks what it serves. A grid that does not
// answer, or serves no execution, is an error here and not at the first
// action.
func Dial(ctx context.Context, config Config) (*Grid, error) {
	c, err := client.NewClient(ctx, config.Instance, client.DialParams{Service: config.Address, NoSecurity: true, DialOpts: telemetry.GRPCOptions(ctx)})
	if err != nil {
		return nil, fmt.Errorf("reapi: the grid at %s: %w", config.Address, err)
	}
	capabilities, err := c.GetCapabilities(ctx)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("reapi: the grid at %s: %w", config.Address, err)
	}
	if !capabilities.GetExecutionCapabilities().GetExecEnabled() {
		c.Close()
		return nil, fmt.Errorf("reapi: the grid at %s executes nothing for instance %q", config.Address, config.Instance)
	}
	// A file is read every time it is sent. A daemon outlives a build,
	// and a digest remembered by a file's path would be sent for the file
	// after it changed: the build would be of source that is no longer
	// there.
	return &Grid{client: c, files: filemetadata.NewNoopCache()}, nil
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
	ctx, span := telemetry.Start(ctx, "grid", attribute.String("label", action.Label))
	defer func() {
		err = executionError(err)
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
	// and it is the client that knows an answer is already kept.
	done, err := g.client.GetActionResult(ctx, &repb.GetActionResultRequest{
		InstanceName: g.client.InstanceName,
		ActionDigest: actionEntry.Digest.ToProto(),
	})
	cached := err == nil
	switch {
	case status.Code(err) == codes.NotFound:
		running, ran := telemetry.Start(ctx, "grid.run")
		done, cached, err = g.run(running, action.Label, actionEntry.Digest)
		telemetry.End(ran, err)
		if err != nil {
			return exec.Result{}, err
		}
	case err != nil:
		return exec.Result{}, fmt.Errorf("reapi: %s: asking the cache: %w", action.Label, err)
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

// run has the grid execute an action and returns its answer, and whether
// the grid answered from what it kept after all.
func (g *Grid) run(ctx context.Context, label string, action digest.Digest) (*repb.ActionResult, bool, error) {
	operation, err := g.client.ExecuteAndWait(ctx, &repb.ExecuteRequest{
		InstanceName: g.client.InstanceName,
		ActionDigest: action.ToProto(),
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
			return nil, false, fmt.Errorf("reapi: %s: the worker could not start %s, the frontseat-action every action of this daemon is started by, for something it did not find: where the worker has no confiner of this form, install it beside bb_runner (the local stack's action-installer, or `frontseat-action install /bb`); where it has, the directory the action runs in was not there: %w", label, confiner, said.Err())
		}
		return nil, false, fmt.Errorf("reapi: %s: %w", label, said.Err())
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

// lacking is the frontseat-action a worker could not start an action by
// for something it did not find, where that is what the grid says:
// bb_runner answers an argument that is not so, and names the program it
// could not start and why. That is the confiner missing, or the directory
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
func (g *Grid) Materialize(ctx context.Context, directory exec.Held, into string) error {
	if !directory.Directory {
		return fmt.Errorf("%s is a file, not a directory", directory.Digest.Hash)
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
