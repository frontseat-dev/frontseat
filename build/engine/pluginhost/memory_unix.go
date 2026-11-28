//go:build unix

package pluginhost

import (
	"math"
	"runtime"

	"github.com/tetratelabs/wazero/experimental"
	"golang.org/x/sys/unix"
)

// mapped is one module's linear memory, reserved whole from the kernel:
// the address space of its maximum at once, resident only where the
// module has touched it. Growing it hands out more of the reservation,
// so nothing is copied, as frontseat-archive's host did (f85d3109).
//
// Freeing it gives its pages back to the kernel and keeps its addresses:
// a module is closed while a call of it may still run, when the call's
// context ends (WithCloseOnContextDone), and code that runs on in memory
// that was unmapped faults the whole process, where in memory given back
// it reads zeros and its answer is thrown away. The addresses are
// unmapped once nothing reaches the memory any more, so no call can run
// in it.
type mapped struct {
	buf []byte
}

// mapMemory reserves a linear memory of max bytes; nil where the kernel
// will not, which leaves it to the heap.
func mapMemory(_, max uint64) experimental.LinearMemory {
	if max > math.MaxInt {
		return nil
	}
	flags := unix.MAP_PRIVATE | unix.MAP_ANON | unix.MAP_NORESERVE
	buf, err := unix.Mmap(-1, 0, int(max), unix.PROT_READ|unix.PROT_WRITE, flags)
	if err != nil {
		return nil
	}
	m := &mapped{buf: buf}
	runtime.AddCleanup(m, func(buf []byte) { _ = unix.Munmap(buf) }, buf)
	return m
}

func (m *mapped) Reallocate(size uint64) []byte {
	if size > uint64(cap(m.buf)) {
		return nil
	}
	return m.buf[:size]
}

func (m *mapped) Free() {
	_ = unix.Madvise(m.buf, unix.MADV_DONTNEED)
}
