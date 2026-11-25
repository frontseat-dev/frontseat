package reapitest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bazelbuild/remote-apis-sdks/go/pkg/fakes"
	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	bspb "google.golang.org/genproto/googleapis/bytestream"
	oppb "google.golang.org/genproto/googleapis/longrunning"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// Keeping is a grid on the loopback that keeps an action's result by the
// instance it was run under and its digest, as Buildbarn's action cache
// does, and its files by digest alone, as its storage does: what it ran
// for one instance another finds only by asking that instance. Each
// action it is sent it runs, as one that prints nothing and passes. It
// notes each instance its cache was asked of, and each action it was
// sent to run.
type Keeping struct {
	repb.UnimplementedActionCacheServer
	repb.UnimplementedExecutionServer

	// Cached, where set before it serves, has it answer every action it
	// is sent to run as one it answered from its cache (cached_result),
	// whatever it was told.
	Cached bool

	mu sync.Mutex
	// refuses is what its cache answers when asked of an instance, by
	// the instance (Refuse).
	refuses  map[string]codes.Code
	kept     map[string]*repb.ActionResult
	looked   []string
	executed []*repb.ExecuteRequest
	storage  *fakes.CAS
	// letting are the files its storage lets go of once it has said it
	// holds them (LetGoOnceFound), and gone those it let go of, by hash.
	letting, gone map[string]bool
	// failing are the files reading which its storage answers with a
	// code, by hash (FailReading).
	failing map[string]codes.Code
	// empties is whether its storage says it holds every file of no
	// bytes, whatever its hash (HoldAnyOfNoBytes).
	empties bool
}

func (k *Keeping) GetActionResult(_ context.Context, request *repb.GetActionResultRequest) (*repb.ActionResult, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.looked = append(k.looked, request.GetInstanceName())
	if code, refused := k.refuses[request.GetInstanceName()]; refused {
		// The grid's words name the instance, as Buildbarn's do
		// ("Invalid instance name %#v").
		return nil, status.Errorf(code, "the grid's own words of %q", request.GetInstanceName())
	}
	if result, kept := k.kept[request.GetInstanceName()+" "+request.GetActionDigest().GetHash()]; kept {
		return result, nil
	}
	return nil, status.Error(codes.NotFound, "not kept")
}

// Execute runs an action, which prints nothing and passes, and keeps
// its result under the instance it was run for, as a worker writes it.
func (k *Keeping) Execute(request *repb.ExecuteRequest, stream repb.Execution_ExecuteServer) error {
	result := &repb.ActionResult{}
	k.mu.Lock()
	k.executed = append(k.executed, request)
	k.mu.Unlock()
	k.Keep(request.GetInstanceName(), request.GetActionDigest().GetHash(), result)
	response, err := anypb.New(&repb.ExecuteResponse{Result: result, CachedResult: k.Cached})
	if err != nil {
		return err
	}
	return stream.Send(&oppb.Operation{Name: "ran", Done: true, Result: &oppb.Operation_Response{Response: response}})
}

// Refuse has the grid's cache answer every question of an instance with
// a code, from now on, in words that name the instance as Buildbarn's
// do: an instance it does not serve, as Buildbarn answers one
// (InvalidArgument), one it does not let the daemon read, for now or for
// good (PermissionDenied), or one that fails.
func (k *Keeping) Refuse(instance string, code codes.Code) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.refuses == nil {
		k.refuses = map[string]codes.Code{}
	}
	k.refuses[instance] = code
}

// Keep has the grid keep a result for an action, by its digest's hash,
// under an instance, as a worker of that instance would have written it.
func (k *Keeping) Keep(instance, action string, result *repb.ActionResult) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.kept == nil {
		k.kept = map[string]*repb.ActionResult{}
	}
	k.kept[instance+" "+action] = result
}

// Holds has the grid's storage hold a file, and returns its digest.
func (k *Keeping) Holds(blob []byte) *repb.Digest {
	return k.storage.Put(blob).ToProto()
}

// LetGoOnceFound has the grid's storage let go of a file once it has
// answered that it holds it, as a storage that evicts may between two
// questions: from then on it is missing, and reading it is answered
// NotFound.
func (k *Keeping) LetGoOnceFound(file *repb.Digest) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.letting == nil {
		k.letting, k.gone = map[string]bool{}, map[string]bool{}
	}
	k.letting[file.GetHash()] = true
}

// FailReading has the grid's storage answer every read of a file with a
// code, as a storage that fails may, while it still says it holds it.
func (k *Keeping) FailReading(file *repb.Digest, code codes.Code) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failing == nil {
		k.failing = map[string]codes.Code{}
	}
	k.failing[file.GetHash()] = code
}

// HoldAnyOfNoBytes has the grid's storage say, from now on, that it
// holds every file of no bytes whatever the hash its digest names, as a
// grid that misbehaves could: the hash of no bytes is one hash.
func (k *Keeping) HoldAnyOfNoBytes() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.empties = true
}

// read is what the storage answers a read of a file with, where it does
// not read it: NotFound for one it let go of, and the code of one it
// fails reading.
func (k *Keeping) read(file string) (codes.Code, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.gone[file] {
		return codes.NotFound, true
	}
	code, failing := k.failing[file]
	return code, failing
}

// holdsEmpty is whether the storage says it holds a file of this digest
// for its having no bytes (HoldAnyOfNoBytes).
func (k *Keeping) holdsEmpty(file *repb.Digest) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.empties && file.GetSizeBytes() == 0
}

// found notes that the storage answered it holds these files, letting
// go of those it is to let go of once found.
func (k *Keeping) found(held []*repb.Digest) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, file := range held {
		if k.letting[file.GetHash()] {
			k.gone[file.GetHash()] = true
		}
	}
}

// let is whether the storage let go of a file.
func (k *Keeping) let(file string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.gone[file]
}

// Asked is what the grid was asked so far: the instances its cache was
// asked of, in order, and the instance each action it ran was run for,
// with whether it was told to answer from no cache.
func (k *Keeping) Asked() (looked []string, ran []string, skipped []bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, each := range k.executed {
		ran = append(ran, each.GetInstanceName())
		skipped = append(skipped, each.GetSkipCacheLookup())
	}
	return slices.Clone(k.looked), ran, skipped
}

// Serve starts the grid and returns its address.
func (k *Keeping) Serve(t testing.TB) string {
	t.Helper()
	k.storage = fakes.NewCAS()
	return (&Answering{Also: func(server *grpc.Server) {
		repb.RegisterContentAddressableStorageServer(server, byDigest{k.storage, k})
		bspb.RegisterByteStreamServer(server, byDigest{k.storage, k})
		repb.RegisterActionCacheServer(server, k)
		repb.RegisterExecutionServer(server, k)
	}}).Serve(t)
}

// byDigest is the storage of a grid that keeps files by their digest
// alone, whatever instance they were sent or are read under, as
// Buildbarn's does: the SDK's fake, which expects one instance, asked
// as that one; less what its grid let go of (LetGoOnceFound) or fails
// reading (FailReading), and saying it holds what it is told to
// (HoldAnyOfNoBytes).
type byDigest struct {
	*fakes.CAS
	grid *Keeping
}

func (b byDigest) FindMissingBlobs(ctx context.Context, request *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error) {
	request.InstanceName = b.InstanceName
	response, err := b.CAS.FindMissingBlobs(ctx, request)
	if err != nil {
		return nil, err
	}
	var held []*repb.Digest
	for _, file := range request.GetBlobDigests() {
		switch {
		case b.grid.let(file.GetHash()):
			response.MissingBlobDigests = append(response.MissingBlobDigests, file)
		case !slices.ContainsFunc(response.GetMissingBlobDigests(), func(each *repb.Digest) bool { return each.GetHash() == file.GetHash() }):
			held = append(held, file)
		}
	}
	response.MissingBlobDigests = slices.DeleteFunc(response.MissingBlobDigests, b.grid.holdsEmpty)
	b.grid.found(held)
	return response, nil
}

func (b byDigest) BatchUpdateBlobs(ctx context.Context, request *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error) {
	request.InstanceName = b.InstanceName
	return b.CAS.BatchUpdateBlobs(ctx, request)
}

func (b byDigest) BatchReadBlobs(ctx context.Context, request *repb.BatchReadBlobsRequest) (*repb.BatchReadBlobsResponse, error) {
	request.InstanceName = b.InstanceName
	response, err := b.CAS.BatchReadBlobs(ctx, request)
	for _, each := range response.GetResponses() {
		if code, unread := b.grid.read(each.GetDigest().GetHash()); unread {
			each.Data, each.Status = nil, status.New(code, "not read").Proto()
		}
	}
	return response, err
}

// Read and Write are the storage's stream of large files, which names
// the instance in each resource's name: it is named as the fake's.
func (b byDigest) Read(request *bspb.ReadRequest, stream bspb.ByteStream_ReadServer) error {
	request.ResourceName = b.named(request.GetResourceName())
	for _, part := range strings.Split(request.GetResourceName(), "/") {
		if code, unread := b.grid.read(part); unread {
			return status.Error(code, "not read")
		}
	}
	return b.CAS.Read(request, stream)
}

func (b byDigest) Write(stream bspb.ByteStream_WriteServer) error {
	return b.CAS.Write(renamed{stream, b})
}

func (b byDigest) QueryWriteStatus(ctx context.Context, request *bspb.QueryWriteStatusRequest) (*bspb.QueryWriteStatusResponse, error) {
	request.ResourceName = b.named(request.GetResourceName())
	return b.CAS.QueryWriteStatus(ctx, request)
}

// named is a resource's name under the fake's instance in place of the
// one it was sent under.
func (b byDigest) named(resource string) string {
	for _, marker := range []string{"uploads/", "blobs/", "compressed-blobs/"} {
		if at := strings.Index(resource, marker); at >= 0 {
			return b.InstanceName + "/" + resource[at:]
		}
	}
	return resource
}

type renamed struct {
	bspb.ByteStream_WriteServer
	storage byDigest
}

func (r renamed) Recv() (*bspb.WriteRequest, error) {
	request, err := r.ByteStream_WriteServer.Recv()
	if request != nil && request.GetResourceName() != "" {
		request.ResourceName = r.storage.named(request.GetResourceName())
	}
	return request, err
}
