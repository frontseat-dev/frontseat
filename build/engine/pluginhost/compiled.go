package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
)

// Wazero 1.12 initializes its process-wide version cache during runtime
// construction without synchronization (internal/version.GetWazeroVersion),
// and when a compilation cache is kept on disk. This package owns every
// runtime and every cache; serialize their construction, while each host's
// compilation and execution remain independent.
var runtimeCreation sync.Mutex

// compilations is what this process has compiled, shared by every runtime
// the package makes, each of one configuration: a module is compiled once
// in a process, and a host made after another has loaded it loads it from
// here. Wazero keeps a module's machine code only while some runtime holds
// it, so a runtime of the package's own, never closed, holds each plugin
// that has loaded, whatever host loaded it and whether or not that host is
// closed. A daemon holds its plugins for its life all the same; a process
// that loads many modules, a test's, keeps each one it loaded.
//
// Told a directory (Compiled), what is compiled is kept there too, and a
// process loads from there what another compiled.
var compilations = struct {
	mu sync.Mutex
	// cache is what every host's runtime shares; memory what a runtime
	// that compiles in memory alone shares. They are one where nothing is
	// kept on disk.
	cache, memory wazero.CompilationCache
	// onDisk is the directory this version of wazero keeps its compiled
	// code in, where it keeps it on disk.
	onDisk string
	// made is whether a host's runtime has been made, after which the
	// cache is what it is.
	made bool
	// holders hold what has loaded, a runtime for each cache it was
	// compiled in, with the digests each holds.
	holders map[wazero.CompilationCache]*holding
	// failed is whether what was kept on disk could not be used: the
	// process then compiles in memory alone, and has removed it, once.
	failed  bool
	dropped sync.Once
}{cache: memoryCache, memory: memoryCache, holders: map[wazero.CompilationCache]*holding{}}

// holding is a runtime that holds what has loaded, never closed, and the
// digests of what it holds.
type holding struct {
	rt   wazero.Runtime
	held map[string]bool
}

var memoryCache = wazero.NewCompilationCache()

// Compiled keeps what this process compiles in a directory, and loads
// from there what another process compiled: a daemon told one compiles
// each plugin once on its machine, not once each time it starts. The
// directory is made where it is not; what is kept there is machine code
// a daemon runs, so the directory must be its user's, which no one else
// may write (closed), and what this version keeps beneath it is held to
// the same before wazero reads any of it (cleared). It is told before any
// host is made, once. Where it cannot be used it is refused, and the
// process compiles in memory as it would without it.
//
// Wazero keeps each of its versions' code beneath a directory of its own,
// and reads no other's: what another version of it, of this machine's
// architecture and system, kept there is removed, as nothing of this
// version reads it.
func Compiled(dir string) error {
	if err := madeClosed(dir); err != nil {
		return err
	}
	// Wazero keys what it keeps by the module, the processor and its own
	// version, and not by how the runtime is configured, which is
	// compiled in: the code of one configuration is kept apart from
	// another's.
	dir = filepath.Join(dir, configured)
	if err := madeClosed(dir); err != nil {
		return err
	}
	compilations.mu.Lock()
	defer compilations.mu.Unlock()
	if compilations.made {
		return errors.New("pluginhost: told where to keep compiled plugins after a host was made")
	}
	version := filepath.Join(dir, versionDirectory())
	// Held for as long as the process lasts, so that no daemon of
	// another version removes the directory this one keeps in.
	if _, err := lockShared(version + ".lock"); err != nil {
		return fmt.Errorf("pluginhost: keeping compiled plugins in %s: %w", dir, err)
	}
	if err := cleared(version); err != nil {
		return err
	}
	runtimeCreation.Lock()
	cache, err := wazero.NewCompilationCacheWithDir(dir)
	runtimeCreation.Unlock()
	if err != nil {
		return fmt.Errorf("pluginhost: keeping compiled plugins in %s: %w", dir, err)
	}
	if err := cleared(version); err != nil {
		return err
	}
	compilations.cache = cache
	compilations.onDisk = version
	removeOtherVersions(dir)
	return nil
}

// madeClosed makes a directory compiled plugins are kept in where it is
// not, and holds it to one only its user may write (closed).
func madeClosed(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("pluginhost: keeping compiled plugins in %s: %w", dir, err)
	}
	return closed(dir)
}

// closed holds a directory compiled plugins are kept in to one that only
// its user may write: a directory, no link, that neither its group nor
// others may write. It is no secret: who reads it learns what the
// plugins, which are no secret either, compile to.
func closed(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 || !ours(info) {
		return fmt.Errorf("pluginhost: the directory compiled plugins are kept in, %s, is no directory, is another's or others may write it: it is its user's alone (chmod 700)", dir)
	}
	return nil
}

// ours is whether this process's user owns a file.
func ours(info os.FileInfo) bool {
	owner, known := info.Sys().(*syscall.Stat_t)
	return known && int(owner.Uid) == os.Geteuid()
}

// cleared holds the directory this version of wazero keeps its compiled
// code in to what it would have written itself, before any of it is read.
// Wazero checks nothing of it but each file's header and checksum, which
// whoever writes a file can make, and what is read there is run. The
// directory must be a directory, no link, of this user's alone, and is
// removed whole where it is not (a link is removed, and what it led to is
// not): it was made by someone else, or while the directory above was
// open to others. Each entry must be a regular file of this user's that
// no one else may read or write, and one that is not is removed alone, so
// that the directory stays for the daemons beside that keep in it; one
// gone as it is looked at, moved into place or let go of by one of them,
// is none. What is removed is this package's own: the directory it tells
// wazero to keep this version's code in, which wazero makes again.
func cleared(version string) error {
	info, err := os.Lstat(version)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pluginhost: looking at compiled plugins in %s: %w", version, err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ours(info) {
		if err := os.RemoveAll(version); err != nil {
			return fmt.Errorf("pluginhost: removing compiled plugins that were not this user's alone, in %s: %w", version, err)
		}
		return nil
	}
	return emptied(version, func(entry os.FileInfo) bool {
		return !entry.Mode().IsRegular() || entry.Mode().Perm()&0o077 != 0 || !ours(entry)
	})
}

// emptied removes the entries of a directory of compiled code that
// unused says are, leaving the directory; one gone as it is looked at is
// none, and one it could not remove is said.
func emptied(dir string, unused func(os.FileInfo) bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("pluginhost: looking at compiled plugins in %s: %w", dir, err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || unused(info) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("pluginhost: removing compiled plugins that were not this user's alone, in %s: %w", dir, err)
			}
		}
	}
	return nil
}

// versionDirectory is the directory wazero keeps this version's compiled
// code in, beneath the one it is told, named as wazero names it
// (cache.go, ensuresFileCache): its version as this program's build
// says it (internal/version.GetWazeroVersion), its architecture and its
// system.
func versionDirectory() string {
	version := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if strings.Contains(dep.Path, "github.com/tetratelabs/wazero") {
				version = dep.Version
			}
		}
		if version == "" || version == "(devel)" {
			version = info.Main.Version
		}
	}
	if version == "" || version == "(devel)" {
		version = "dev"
	}
	return "wazero-" + version + "-" + runtime.GOARCH + "-" + runtime.GOOS
}

// removeOtherVersions removes, beneath the directory compiled plugins are
// kept in, what another version of wazero kept for this machine's
// architecture and system: that is this package's own, since it told
// wazero the directory, and nothing of this version reads it. It removes
// nothing where this version's own directory is not where it is taken to
// be, since it could not then tell another's from it; one of another
// architecture or system, which a home shared between machines holds, is
// left.
func removeOtherVersions(dir string) {
	ours := filepath.Base(compilations.onDisk)
	if info, err := os.Lstat(compilations.onDisk); err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	suffix := "-" + runtime.GOARCH + "-" + runtime.GOOS
	for _, entry := range entries {
		name := entry.Name()
		if name != ours && entry.IsDir() && strings.HasPrefix(name, "wazero-") && strings.HasSuffix(name, suffix) {
			removeUnheld(dir, name)
		}
	}
}

// removeUnheld removes another version's directory where no daemon holds
// it (lockShared), as frontseat-archive's pruner did: one that does
// compiles into it still.
func removeUnheld(dir, name string) {
	lock := filepath.Join(dir, name+".lock")
	release, held, err := lockExclusive(lock)
	if err != nil || held {
		return
	}
	defer release()
	if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
		return
	}
	// A daemon that opened the lock before it went holds a file no
	// longer at its path, and takes it again (lockShared).
	_ = os.Remove(lock)
}

// configured names how every runtime of the package is configured
// (configuration), which what is compiled is compiled with: one changed
// is named anew, and keeps its code apart.
const configured = "terminating"

// shared is the cache a host's runtime shares, which is then what it is.
func shared() wazero.CompilationCache {
	compilations.mu.Lock()
	defer compilations.mu.Unlock()
	compilations.made = true
	if compilations.failed {
		return compilations.memory
	}
	return compilations.cache
}

// compileWorkers is how many of a module's functions are compiled at once:
// compiling the largest plugin (backstage, 1.1 MB) took 348–384 ms with
// one, 162–186 ms with two and 88–105 ms with four, and no less with
// sixteen, measured 2026-11-28 on an Apple M-series laptop of 16 cores.
// A daemon loads its plugins at once (LoadAll), so a machine of fewer
// processors is given no more workers than it has: the memory each holds
// while it compiles is not measured for twelve modules at once.
const compileWorkers = 4

// compiling is a context a module is compiled under, by compileWorkers or
// as many processors as Go runs on, whichever is fewer.
func compiling(ctx context.Context) context.Context {
	return experimental.WithCompilationWorkers(ctx, min(compileWorkers, runtime.GOMAXPROCS(0)))
}

// configuration is how every runtime of the package runs a module, which
// is compiled into it: the runtimes that share what is compiled share it.
func configuration() wazero.RuntimeConfig {
	return wazero.NewRuntimeConfig().
		WithMemoryLimitPages(memoryLimitPages).
		WithCloseOnContextDone(true)
}

func newRuntime(ctx context.Context, cache wazero.CompilationCache) wazero.Runtime {
	runtimeCreation.Lock()
	defer runtimeCreation.Unlock()
	return wazero.NewRuntimeWithConfig(ctx, configuration().WithCompilationCache(cache))
}

// hold keeps a module that has loaded compiled in the cache it was
// compiled in, for as long as the process lasts. Its host holds it as
// this is called, so compiling it again is taking it from the cache. A
// module that could not be held is compiled again by the next host to
// load it, which costs time and nothing else.
func hold(module []byte, digest string, cache wazero.CompilationCache) {
	compilations.mu.Lock()
	defer compilations.mu.Unlock()
	holder := compilations.holders[cache]
	if holder == nil {
		holder = &holding{rt: newRuntime(context.Background(), cache), held: map[string]bool{}}
		compilations.holders[cache] = holder
	}
	if holder.held[digest] {
		return
	}
	if _, err := holder.rt.CompileModule(context.Background(), module); err == nil {
		holder.held[digest] = true
	}
}

// compile compiles a module on the host's runtime. Where compiled code is
// kept on disk and that fails, as wazero fails a compile whose code on
// disk could not be read or written (an entry that is not whole, a disk
// that is full), the module is compiled again on a runtime that compiles
// in memory alone and loaded there, and what was kept on disk is removed:
// what is kept on disk only ever saves time. A module that does not
// compile in memory either is refused as it is.
func (h *Host) compile(ctx context.Context, module []byte) (wazero.Runtime, wazero.CompiledModule, error) {
	ctx = compiling(ctx)
	if !failedOnDisk() {
		compiled, err := h.rt.CompileModule(ctx, module)
		if err == nil || !keptOnDisk() {
			return h.rt, compiled, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.memory == nil {
		memory, err := hostRuntime(ctx, compilations.memory)
		if err != nil {
			return nil, nil, err
		}
		h.memory = memory
	}
	again, err := h.memory.CompileModule(ctx, module)
	if err != nil {
		return nil, nil, err
	}
	compilations.dropped.Do(func() {
		compilations.mu.Lock()
		compilations.failed = true
		compilations.mu.Unlock()
		// What could not be used is removed, and the directory left for
		// the daemons beside that keep in it: it is this package's own,
		// the one it told wazero to keep this version's code in. This
		// process compiles in memory from now on, as do the hosts made
		// after; the next one compiles what it needs again, and keeps it.
		_ = emptied(compilations.onDisk, func(os.FileInfo) bool { return true })
	})
	return h.memory, again, nil
}

// failedOnDisk is whether what was kept on disk could not be used, after
// which the process compiles in memory alone.
func failedOnDisk() bool {
	compilations.mu.Lock()
	defer compilations.mu.Unlock()
	return compilations.failed
}

// keptOnDisk is whether what is compiled is kept on disk.
func keptOnDisk() bool {
	compilations.mu.Lock()
	defer compilations.mu.Unlock()
	return compilations.onDisk != ""
}
