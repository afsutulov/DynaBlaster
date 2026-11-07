package machine

// PITHz is the input clock of the 8253/8254 timer.
const PITHz = 1193182

type pitChan struct {
	reload  uint32
	mode    uint8
	access  uint8 // 1 lo, 2 hi, 3 lo/hi
	wrHi    bool
	rdHi    bool
	latched bool
	latch   uint16
	start   int64 // virtual time the count was loaded
	partial uint8
}

// PIT emulates the 8254 programmable interval timer.
type PIT struct {
	ch       [3]pitChan
	nextIRQ0 int64
	period0  int64 // ns
	m        *M
}

func NewPIT(m *M) *PIT {
	p := &PIT{m: m}
	for i := range p.ch {
		p.ch[i].reload = 0x10000
		p.ch[i].access = 3
		p.ch[i].mode = 3
	}
	p.period0 = int64(0x10000) * 1e9 / PITHz
	p.nextIRQ0 = p.period0
	return p
}

func (p *PIT) Next() int64 { return p.nextIRQ0 }

func (p *PIT) Tick(m *M, now int64) {
	for now >= p.nextIRQ0 {
		m.RaiseIRQ(0)
		p.nextIRQ0 += p.period0
	}
}

func (p *PIT) count(m *M, i int) uint16 {
	c := &p.ch[i]
	el := (m.Now() - c.start) * PITHz / 1e9
	r := int64(c.reload)
	if c.mode == 3 {
		// square wave counts down by two, twice per period
		v := r - (el*2)%r
		return uint16(v)
	}
	return uint16(r - el%r)
}

func (p *PIT) In(port uint16) uint8 {
	if port == 0x43 {
		return 0xff
	}
	i := int(port - 0x40)
	c := &p.ch[i]
	v := c.latch
	if !c.latched {
		v = p.count(p.m, i)
	}
	switch c.access {
	case 1:
		c.latched = false
		return uint8(v)
	case 2:
		c.latched = false
		return uint8(v >> 8)
	default:
		if !c.rdHi {
			c.rdHi = true
			return uint8(v)
		}
		c.rdHi = false
		c.latched = false
		return uint8(v >> 8)
	}
}

func (p *PIT) Out(port uint16, v uint8) {
	m := p.m
	if port == 0x43 {
		i := int(v >> 6)
		if i == 3 {
			return // read-back: unsupported
		}
		c := &p.ch[i]
		acc := (v >> 4) & 3
		if acc == 0 {
			c.latch = p.count(m, i)
			c.latched = true
			c.rdHi = false
			return
		}
		c.access = acc
		c.mode = (v >> 1) & 7
		if c.mode > 5 {
			c.mode &= 3
		}
		c.wrHi = false
		c.rdHi = false
		return
	}
	i := int(port - 0x40)
	c := &p.ch[i]
	load := false
	switch c.access {
	case 1:
		c.reload = uint32(v)
		load = true
	case 2:
		c.reload = uint32(v) << 8
		load = true
	default:
		if !c.wrHi {
			c.partial = v
			c.wrHi = true
		} else {
			c.reload = uint32(c.partial) | uint32(v)<<8
			c.wrHi = false
			load = true
		}
	}
	if load {
		if c.reload == 0 {
			c.reload = 0x10000
		}
		c.start = m.Now()
		if i == 0 {
			p.period0 = int64(c.reload) * 1e9 / PITHz
			p.nextIRQ0 = m.Now() + p.period0
			m.Clk.Reschedule()
		}
	}
}

// Ch0Period returns the current timer 0 interrupt period in ns.
func (p *PIT) Ch0Period() int64 { return p.period0 }
