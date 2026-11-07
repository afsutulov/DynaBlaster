package game

import (
	"hash/fnv"
	"testing"
	"time"

	"dynablaster/machine"
)

// run executes the game for n frames with a few scripted key presses and
// returns one state hash per frame.
func run(t *testing.T, interp bool, n int) []uint64 {
	m := New(Options{})
	m.DOS.Now = func() time.Time { return time.Date(1993, 1, 1, 12, 0, 0, 0, time.UTC) }
	if interp {
		m.InterpOnly = true
	}
	keys := map[int]uint8{120: 0x39, 124: 0xb9, 260: 0x39, 264: 0xb9}
	var hashes []uint64
	m.Hook.Frame = func(m *machine.M) {
		if k, ok := keys[len(hashes)]; ok {
			m.Kbd.Push(k)
		}
		h := fnv.New64a()
		for p := 0; p < 4; p++ {
			h.Write(m.VGA.Planes[p][:])
		}
		h.Write(m.Mem[0x8100:0x80000])
		hashes = append(hashes, h.Sum64())
	}
	for len(hashes) < n && !m.Halted {
		m.Step()
	}
	return hashes
}

// TestTranslationMatchesInterpreter checks that the translated code and
// the reference interpreter produce identical machine states.
func TestTranslationMatchesInterpreter(t *testing.T) {
	n := 400
	if testing.Short() {
		n = 150
	}
	a := run(t, false, n)
	b := run(t, true, n)
	if len(a) != len(b) {
		t.Fatalf("frame counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("state differs at frame %d", i)
		}
	}
}
