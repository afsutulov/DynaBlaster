package machine

// PIC emulates the pair of 8259A interrupt controllers in their standard
// PC configuration (master vectors 08h-0Fh, slave 70h-77h).
type PIC struct {
	imr  [2]uint8
	irr  [2]uint8
	isr  [2]uint8
	base [2]uint8
	icw  [2]int
	rdIS [2]bool
}

func NewPIC() *PIC {
	return &PIC{base: [2]uint8{8, 0x70}, imr: [2]uint8{0xb8, 0xff}}
}

func (p *PIC) Raise(n int) {
	if n < 8 {
		p.irr[0] |= 1 << n
	} else {
		p.irr[1] |= 1 << (n - 8)
		p.irr[0] |= 1 << 2
	}
}

// Ack returns the vector of the highest priority pending interrupt.
func (p *PIC) Ack() (uint8, bool) {
	pend := p.irr[0] &^ p.imr[0]
	if pend == 0 {
		return 0, false
	}
	for i := 0; i < 8; i++ {
		bit := uint8(1) << i
		if p.isr[0]&bit != 0 {
			return 0, false // higher priority in service
		}
		if pend&bit != 0 {
			if i == 2 {
				sp := p.irr[1] &^ p.imr[1]
				for j := 0; j < 8; j++ {
					b2 := uint8(1) << j
					if p.isr[1]&b2 != 0 {
						return 0, false
					}
					if sp&b2 != 0 {
						p.irr[1] &^= b2
						if p.irr[1] == 0 {
							p.irr[0] &^= bit
						}
						p.isr[1] |= b2
						p.isr[0] |= bit
						return p.base[1] + uint8(j), true
					}
				}
				p.irr[0] &^= bit
				continue
			}
			p.irr[0] &^= bit
			p.isr[0] |= bit
			return p.base[0] + uint8(i), true
		}
	}
	return 0, false
}

// InService reports whether IRQ n is being serviced or pending.
func (p *PIC) Busy(n int) bool {
	return (p.irr[0]|p.isr[0])&(1<<n) != 0
}

func (p *PIC) In(port uint16) uint8 {
	c := 0
	if port >= 0xa0 {
		c = 1
	}
	if port&1 != 0 {
		return p.imr[c]
	}
	if p.rdIS[c] {
		return p.isr[c]
	}
	return p.irr[c]
}

func (p *PIC) Out(port uint16, v uint8) {
	c := 0
	if port >= 0xa0 {
		c = 1
	}
	if port&1 == 0 {
		if v&0x10 != 0 { // ICW1
			p.icw[c] = 1
			p.imr[c] = 0
			p.isr[c] = 0
			return
		}
		if v&0x18 == 0 { // OCW2
			if v&0x20 != 0 { // EOI
				if v&0x40 != 0 {
					p.isr[c] &^= 1 << (v & 7)
				} else {
					for i := 0; i < 8; i++ {
						if p.isr[c]&(1<<i) != 0 {
							p.isr[c] &^= 1 << i
							break
						}
					}
				}
			}
			return
		}
		if v&0x18 == 0x08 { // OCW3
			if v&2 != 0 {
				p.rdIS[c] = v&1 != 0
			}
		}
		return
	}
	switch p.icw[c] {
	case 1:
		p.base[c] = v & 0xf8
		p.icw[c] = 2
	case 2:
		p.icw[c] = 3
	case 3:
		p.icw[c] = 0
	default:
		p.imr[c] = v
	}
}
