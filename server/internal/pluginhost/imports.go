package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/frontseat-dev/frontseat/server/internal/pattern"
	"strings"
	"sync"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"google.golang.org/protobuf/proto"
)

// Source is what a plugin may read while it answers: the contract's
// HostService, answered out of a repository as it stood at one moment.
type Source interface {
	// Read returns a file of the repository, and whether it is there.
	Read(ctx context.Context, path string) ([]byte, bool, error)
	// Glob returns repository-relative files beneath a literal directory,
	// matching patterns relative to that directory, sorted.
	Glob(ctx context.Context, directory string, patterns []string) ([]string, error)
}

// Read is one thing a plugin read through the host during a call.
type Read struct {
	// Import is the import it came through: "read" or "glob".
	Import string
	// Argument is what was asked for: a path, or the patterns with a NUL
	// between them.
	Argument string
	// Directory is the literal directory of a glob, empty for the root.
	Directory string
	// Found is whether there was anything there.
	Found bool
	// Digest is the SHA-256 of what was returned: a file's bytes, or the
	// matched paths with a NUL between them.
	Digest string
}

// Known is a repository asked what its files hold without their being
// read where that is known already.
type Known interface {
	// Digest returns the SHA-256 of a file's bytes, and whether the file
	// is there.
	Digest(ctx context.Context, path string) (string, bool, error)
	// Glob has the same directory and pattern semantics as Source.Glob.
	Glob(ctx context.Context, directory string, patterns []string) ([]string, error)
}

// Same reports whether a repository holds what a call read of it: each
// file read is there or not as it was and holds what it held, and each
// set of patterns matches the files it matched. A plugin answers from
// its request and what it read, so where this holds, what it answered
// then is what it would answer now.
func Same(ctx context.Context, repository Known, reads []Read) (bool, error) {
	for _, read := range reads {
		now := Read{Import: read.Import, Argument: read.Argument}
		switch read.Import {
		case "read":
			digest, found, err := repository.Digest(ctx, read.Argument)
			if err != nil {
				return false, err
			}
			now.Found, now.Digest = found, digest
			if !found {
				now.Digest = ""
			}
		case "glob":
			patterns := strings.Split(read.Argument, between)
			paths, err := repository.Glob(ctx, read.Directory, patterns)
			if err != nil {
				return false, err
			}
			now = globbed(read.Directory, patterns, paths)
		default:
			return false, fmt.Errorf("pluginhost: a call read through %q, which is no import", read.Import)
		}
		if now != read {
			return false, nil
		}
	}
	return true, nil
}

// Recorder is a repository read through, with everything read of it
// noted as a plugin's reads are: what a host reads for itself, and
// keeps the outcome of for as long as Same holds.
type Recorder struct {
	// Source is what is read.
	Source Source

	mu    sync.Mutex
	reads []Read
}

var _ Source = (*Recorder)(nil)

// Read implements Source.
func (r *Recorder) Read(ctx context.Context, path string) ([]byte, bool, error) {
	content, found, err := r.Source.Read(ctx, path)
	if err != nil {
		return nil, false, err
	}
	r.note(was("read", path, content, found))
	return content, found, nil
}

// Glob implements Source.
func (r *Recorder) Glob(ctx context.Context, directory string, patterns []string) ([]string, error) {
	paths, err := r.Source.Glob(ctx, directory, patterns)
	if err != nil {
		return nil, err
	}
	r.note(globbed(directory, patterns, paths))
	return paths, nil
}

func (r *Recorder) note(read Read) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = append(r.reads, read)
}

// Reads returns everything read so far.
func (r *Recorder) Reads() []Read {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Read(nil), r.reads...)
}

// between is what stands between the patterns, and between the paths,
// of one thing read: the one byte no file's name holds, so that no list
// is recorded as another is.
const between = "\x00"

// was is one thing read, as it is recorded.
func was(imp, argument string, content []byte, found bool) Read {
	r := Read{Import: imp, Argument: argument, Found: found}
	if found {
		sum := sha256.Sum256(content)
		r.Digest = hex.EncodeToString(sum[:])
	}
	return r
}

// globbed is one listing, as it is recorded: the patterns it was asked
// with beneath a literal directory, and the paths they selected. A
// listing is always found, of no paths where none matched.
func globbed(directory string, patterns, paths []string) Read {
	read := was("glob", strings.Join(patterns, between), []byte(strings.Join(paths, between)), true)
	read.Directory = directory
	return read
}

type callKey struct{}

// callState is one call's source and what was read from it.
type callState struct {
	source Source
	reads  []Read
	err    error
}

func (s *callState) record(imp, argument string, content []byte, found bool) {
	s.reads = append(s.reads, was(imp, argument, content, found))
}

var errNoSource = errors.New("the plugin read the repository in a call that is given none")

// serveImports exports the contract's HostService to modules, as the
// imports of the module "frontseat".
func serveImports(ctx context.Context, rt wazero.Runtime) error {
	b := rt.NewHostModuleBuilder("frontseat")
	serve := func(name string, request func() proto.Message, answer func(context.Context, *callState, proto.Message) (proto.Message, error)) {
		b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, m api.Module, stack []uint64) {
			ptr, size := uint32(stack[0]), uint32(stack[1])
			stack[0] = 0
			state, ok := ctx.Value(callKey{}).(*callState)
			if !ok || state.err != nil {
				return
			}
			if state.source == nil {
				state.err = errNoSource
				return
			}
			raw, ok := m.Memory().Read(ptr, size)
			if !ok {
				state.err = errors.New("the plugin asked from outside its memory")
				return
			}
			req := request()
			if state.err = proto.Unmarshal(raw, req); state.err != nil {
				return
			}
			resp, err := answer(ctx, state, req)
			if err != nil {
				state.err = err
				return
			}
			out, err := proto.Marshal(resp)
			if err != nil {
				state.err = err
				return
			}
			if len(out) == 0 {
				return
			}
			rptr, err := writeInto(ctx, m, m.ExportedFunction("frontseat_alloc"), out)
			if err != nil {
				state.err = err
				return
			}
			stack[0] = uint64(rptr)<<32 | uint64(len(out))
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, []api.ValueType{api.ValueTypeI64}).Export(name)
	}

	serve("read", func() proto.Message { return &pluginv1.ReadRequest{} },
		func(ctx context.Context, s *callState, m proto.Message) (proto.Message, error) {
			req := m.(*pluginv1.ReadRequest)
			content, found, err := s.source.Read(ctx, req.GetPath())
			if err != nil {
				return nil, err
			}
			s.record("read", req.GetPath(), content, found)
			return &pluginv1.ReadResponse{Found: found, Content: content}, nil
		})
	serve("glob", func() proto.Message { return &pluginv1.GlobRequest{} },
		func(ctx context.Context, s *callState, m proto.Message) (proto.Message, error) {
			req := m.(*pluginv1.GlobRequest)
			if req.GetDirectory() != "" && !pattern.Local(req.GetDirectory()) {
				return nil, fmt.Errorf("glob directory %q leaves the repository", req.GetDirectory())
			}
			// A pattern the contract's language leaves out selects
			// nothing, and is not asked of the source.
			paths, err := s.source.Glob(ctx, req.GetDirectory(), pattern.Spelling(req.GetPatterns()))
			if err != nil {
				return nil, err
			}
			s.reads = append(s.reads, globbed(req.GetDirectory(), req.GetPatterns(), paths))
			return &pluginv1.GlobResponse{Paths: paths}, nil
		})
	_, err := b.Instantiate(ctx)
	return err
}
