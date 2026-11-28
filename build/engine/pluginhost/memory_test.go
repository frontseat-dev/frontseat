package pluginhost

import (
	"testing"
	"unsafe"
)

// A linear memory grows in place: every size up to its maximum is the
// same bytes, longer, so nothing a module keeps in it moves.
func TestALinearMemoryGrowsInPlace(t *testing.T) {
	const max = 64 << 20
	m := allocate(1<<20, max)
	defer m.Free()
	first := m.Reallocate(1 << 20)
	first[0] = 7
	for _, size := range []uint64{8 << 20, 32 << 20, max} {
		buf := m.Reallocate(size)
		if uint64(len(buf)) != size {
			t.Fatalf("asked for %d bytes, got %d", size, len(buf))
		}
		if unsafe.SliceData(buf) != unsafe.SliceData(first) || buf[0] != 7 {
			t.Fatalf("growing to %d bytes moved the memory", size)
		}
	}
}

// A linear memory never grows past its maximum: asked for more, it
// gives nothing, which the module sees as a failed memory.grow.
func TestALinearMemoryStopsAtItsMaximum(t *testing.T) {
	const max = 1 << 20
	m := allocate(1<<16, max)
	defer m.Free()
	if buf := m.Reallocate(max + 1); buf != nil {
		t.Fatalf("grew to %d bytes past a maximum of %d", len(buf), max)
	}
}

// Where the kernel reserves nothing, the heap holds the memory: it
// grows keeping its bytes, if not its place.
func TestAHeapMemoryGrowsKeepingItsBytes(t *testing.T) {
	m := &heapMemory{buf: make([]byte, 0, 1<<20)}
	first := m.Reallocate(1 << 20)
	first[0] = 7
	buf := m.Reallocate(4 << 20)
	if len(buf) != 4<<20 || buf[0] != 7 {
		t.Fatalf("heap memory grew to %d bytes, first byte %d", len(buf), buf[0])
	}
}
