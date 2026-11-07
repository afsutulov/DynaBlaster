package machine

// Virtual time.
//
// The emulated processor runs at a fixed nominal speed: every executed
// instruction advances virtual time by NsPerInstr nanoseconds. All
// hardware events (timer interrupts, vertical retrace, DMA completion,
// audio generation) are scheduled in virtual time, which makes execution
// fully deterministic. The frontend keeps virtual time in step with the
// wall clock.

// NsPerInstr sets the emulated CPU speed: 40 ns per instruction, i.e.
// 25 MIPS, roughly a 486DX2. The game calibrates its sound card delay
// loops with the timer; on much faster CPUs the 16-bit loop counter
// overflows and the AdLib detection fails (as it did on real Pentiums).
const NsPerInstr = 40

type Clock struct {
	skip int64 // time skipped while idle (ns)
	next int64 // earliest pending event
}

func NewClock() *Clock { return &Clock{next: 0} }

// Now returns the current virtual time in nanoseconds.
func (m *M) Now() int64 { return int64(m.Cycles)*NsPerInstr + m.Clk.skip }

// Advance fires all device events that are due.
func (c *Clock) Advance(m *M) {
	now := m.Now()
	if now < c.next {
		return
	}
	c.service(m, now)
}

func (c *Clock) service(m *M, now int64) {
	m.PIT.Tick(m, now)
	m.VGA.Tick(m, now)
	m.SB.Tick(m, now)
	m.Kbd.Tick(m, now)
	m.Snd.Tick(m, now)
	n := m.PIT.Next()
	if t := m.VGA.Next(); t < n {
		n = t
	}
	if t := m.SB.Next(); t < n {
		n = t
	}
	if t := m.Kbd.Next(); t < n {
		n = t
	}
	if t := m.Snd.Next(); t < n {
		n = t
	}
	c.next = n
}

// Reschedule forces re-evaluation of the next event time.
func (c *Clock) Reschedule() { c.next = 0 }

// SkipTo advances virtual time to t without executing instructions
// (used when the program is provably waiting).
func (c *Clock) SkipTo(m *M, t int64) {
	now := m.Now()
	if t > now {
		c.skip += t - now
	}
	c.service(m, m.Now())
}

// Idle skips to the next scheduled event (HLT, polling loops).
func (c *Clock) Idle(m *M) {
	c.service(m, m.Now())
	if c.next > m.Now() {
		c.SkipTo(m, c.next)
	}
}
