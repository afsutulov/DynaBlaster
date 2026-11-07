// Package runner drives the machine in real time: it executes the game in
// its own goroutine, keeps virtual time in step with the wall clock and
// hands finished video frames to the frontend.
package runner

import (
	"sync"
	"time"

	"dynablaster/machine"
)

// Frame is a rendered video frame.
type Frame struct {
	W, H   int
	Aspect float64
	Pix    []byte // RGBA
	Seq    uint64
}

// Runner executes a machine in real time.
type Runner struct {
	M *machine.M

	mu      sync.Mutex
	front   Frame
	back    []byte
	seq     uint64
	paused  bool
	speed   float64
	done    chan struct{}
	err     interface{}
	stopped bool
}

// New wraps a machine.
func New(m *machine.M) *Runner {
	r := &Runner{M: m, speed: 1, done: make(chan struct{})}
	m.Hook.Frame = r.onFrame
	return r
}

func (r *Runner) onFrame(m *machine.M) {
	w, h, pix := m.VGA.Render(r.back)
	r.back = pix
	if w == 0 {
		return
	}
	r.mu.Lock()
	r.seq++
	// swap buffers
	old := r.front.Pix
	r.front = Frame{W: w, H: h, Aspect: m.VGA.Aspect(), Pix: pix, Seq: r.seq}
	r.back = old
	r.mu.Unlock()
}

// Latest returns the most recent frame (the caller must not keep Pix
// beyond the next call).
func (r *Runner) Latest(dst []byte) (Frame, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.front
	if cap(dst) < len(f.Pix) {
		dst = make([]byte, len(f.Pix))
	}
	dst = dst[:len(f.Pix)]
	copy(dst, f.Pix)
	f.Pix = dst
	return f, dst
}

// SetPaused pauses or resumes emulation.
func (r *Runner) SetPaused(p bool) {
	r.mu.Lock()
	r.paused = p
	r.mu.Unlock()
}

// Paused reports whether emulation is paused.
func (r *Runner) Paused() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused
}

// Done is closed when the program terminates.
func (r *Runner) Done() <-chan struct{} { return r.done }

// Err returns the fault that stopped the program, if any.
func (r *Runner) Err() interface{} { return r.err }

// Stop asks the emulation goroutine to finish.
func (r *Runner) Stop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
}

// Run executes the program until it exits. It blocks; call it in a
// goroutine.
func (r *Runner) Run() {
	defer close(r.done)
	defer func() {
		if e := recover(); e != nil {
			r.err = e
		}
	}()
	m := r.M
	start := time.Now()
	base := m.Now()
	const slice = 2 * time.Millisecond
	for !m.Halted {
		r.mu.Lock()
		paused, stopped := r.paused, r.stopped
		r.mu.Unlock()
		if stopped {
			return
		}
		if paused {
			time.Sleep(20 * time.Millisecond)
			start = time.Now()
			base = m.Now()
			continue
		}
		// run for one slice of virtual time
		target := m.Now() + int64(slice)
		for m.Now() < target && !m.Halted {
			m.Step()
		}
		// keep virtual time in step with the wall clock
		virt := time.Duration(m.Now() - base)
		wall := time.Since(start)
		if virt > wall {
			time.Sleep(virt - wall)
		} else if wall-virt > 250*time.Millisecond {
			// the host cannot keep up (or was suspended): resynchronise
			start = time.Now()
			base = m.Now()
		}
	}
}
