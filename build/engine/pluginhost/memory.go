package pluginhost

import "github.com/tetratelabs/wazero/experimental"

// heapMemory is a linear memory on the Go heap, for where the kernel
// cannot reserve one: it grows as a slice does.
type heapMemory struct {
	buf []byte
}

func (m *heapMemory) Reallocate(size uint64) []byte {
	if size > uint64(cap(m.buf)) {
		m.buf = append(m.buf[:cap(m.buf)], make([]byte, size-uint64(cap(m.buf)))...)
	}
	return m.buf[:size]
}

func (m *heapMemory) Free() { m.buf = nil }

// allocate reserves a module's linear memory from the kernel, or on the
// heap where it cannot.
func allocate(cap, max uint64) experimental.LinearMemory {
	if m := mapMemory(cap, max); m != nil {
		return m
	}
	return &heapMemory{buf: make([]byte, 0, cap)}
}
