// Package machine implements the hardware environment that the original
// 1992 DOS game expected: a real-mode 80x86 CPU state, 1 MB of memory,
// a VGA adapter, PIT/PIC timers, keyboard, DOS file services, a Sound
// Blaster DSP and an OPL2 (AdLib) FM synthesizer.
//
// The game logic itself lives in package game (Go code translated from the
// original machine code); it manipulates the CPU state defined here.
package machine

// General purpose register indices (x86 encoding order).
const (
	AX = iota
	CX
	DX
	BX
	SP
	BP
	SI
	DI
)

// Segment register indices (x86 encoding order).
const (
	ES = iota
	CS
	SS
	DS
)

// Flag bits.
const (
	FCF uint16 = 1 << 0
	FPF uint16 = 1 << 2
	FAF uint16 = 1 << 4
	FZF uint16 = 1 << 6
	FSF uint16 = 1 << 7
	FTF uint16 = 1 << 8
	FIF uint16 = 1 << 9
	FDF uint16 = 1 << 10
	FOF uint16 = 1 << 11
)

var parity [256]bool

func init() {
	for i := 0; i < 256; i++ {
		p := 0
		for b := i; b != 0; b >>= 1 {
			p ^= b & 1
		}
		parity[i] = p == 0
	}
}

// CPU holds the architectural state of the processor.
type CPU struct {
	R     [8]uint16
	S     [4]uint16
	IP    uint16
	Flags uint16
	// Cycles counts executed instructions; it is the base of virtual time.
	Cycles uint64
}

// 8-bit register accessors.
func (c *CPU) AL() uint8     { return uint8(c.R[AX]) }
func (c *CPU) AH() uint8     { return uint8(c.R[AX] >> 8) }
func (c *CPU) BL() uint8     { return uint8(c.R[BX]) }
func (c *CPU) BH() uint8     { return uint8(c.R[BX] >> 8) }
func (c *CPU) CL() uint8     { return uint8(c.R[CX]) }
func (c *CPU) CH() uint8     { return uint8(c.R[CX] >> 8) }
func (c *CPU) DL() uint8     { return uint8(c.R[DX]) }
func (c *CPU) DH() uint8     { return uint8(c.R[DX] >> 8) }
func (c *CPU) SetAL(v uint8) { c.R[AX] = c.R[AX]&0xff00 | uint16(v) }
func (c *CPU) SetAH(v uint8) { c.R[AX] = c.R[AX]&0x00ff | uint16(v)<<8 }
func (c *CPU) SetBL(v uint8) { c.R[BX] = c.R[BX]&0xff00 | uint16(v) }
func (c *CPU) SetBH(v uint8) { c.R[BX] = c.R[BX]&0x00ff | uint16(v)<<8 }
func (c *CPU) SetCL(v uint8) { c.R[CX] = c.R[CX]&0xff00 | uint16(v) }
func (c *CPU) SetCH(v uint8) { c.R[CX] = c.R[CX]&0x00ff | uint16(v)<<8 }
func (c *CPU) SetDL(v uint8) { c.R[DX] = c.R[DX]&0xff00 | uint16(v) }
func (c *CPU) SetDH(v uint8) { c.R[DX] = c.R[DX]&0x00ff | uint16(v)<<8 }
func (c *CPU) R8(i int) uint8 {
	if i < 4 {
		return uint8(c.R[i])
	}
	return uint8(c.R[i-4] >> 8)
}
func (c *CPU) SetR8(i int, v uint8) {
	if i < 4 {
		c.R[i] = c.R[i]&0xff00 | uint16(v)
	} else {
		c.R[i-4] = c.R[i-4]&0x00ff | uint16(v)<<8
	}
}

// Flag accessors.
func (c *CPU) CF() bool { return c.Flags&FCF != 0 }
func (c *CPU) ZF() bool { return c.Flags&FZF != 0 }
func (c *CPU) SF() bool { return c.Flags&FSF != 0 }
func (c *CPU) OF() bool { return c.Flags&FOF != 0 }
func (c *CPU) PF() bool { return c.Flags&FPF != 0 }
func (c *CPU) DF() bool { return c.Flags&FDF != 0 }
func (c *CPU) IF() bool { return c.Flags&FIF != 0 }

func (c *CPU) setF(f uint16, v bool) {
	if v {
		c.Flags |= f
	} else {
		c.Flags &^= f
	}
}
func (c *CPU) SetCF(v bool) { c.setF(FCF, v) }
func (c *CPU) SetDF(v bool) { c.setF(FDF, v) }
func (c *CPU) SetIF(v bool) { c.setF(FIF, v) }

// Condition codes for Jcc/SETcc (index = low nibble of 0x70..0x7F).
func (c *CPU) Cond(cc int) bool {
	var r bool
	switch cc >> 1 {
	case 0:
		r = c.OF()
	case 1:
		r = c.CF()
	case 2:
		r = c.ZF()
	case 3:
		r = c.CF() || c.ZF()
	case 4:
		r = c.SF()
	case 5:
		r = c.PF()
	case 6:
		r = c.SF() != c.OF()
	case 7:
		r = c.ZF() || c.SF() != c.OF()
	}
	if cc&1 != 0 {
		return !r
	}
	return r
}

func (c *CPU) szp8(r uint8) {
	c.Flags &^= FZF | FSF | FPF
	if r == 0 {
		c.Flags |= FZF
	}
	if r&0x80 != 0 {
		c.Flags |= FSF
	}
	if parity[r] {
		c.Flags |= FPF
	}
}
func (c *CPU) szp16(r uint16) {
	c.Flags &^= FZF | FSF | FPF
	if r == 0 {
		c.Flags |= FZF
	}
	if r&0x8000 != 0 {
		c.Flags |= FSF
	}
	if parity[uint8(r)] {
		c.Flags |= FPF
	}
}

// ---- ALU helpers (shared by interpreter and translated code) ----

func (c *CPU) Add8(a, b uint8) uint8 { return c.adc8(a, b, 0) }
func (c *CPU) Adc8(a, b uint8) uint8 {
	var ci uint8
	if c.CF() {
		ci = 1
	}
	return c.adc8(a, b, ci)
}
func (c *CPU) adc8(a, b, ci uint8) uint8 {
	r16 := uint16(a) + uint16(b) + uint16(ci)
	r := uint8(r16)
	c.Flags &^= FCF | FOF | FAF
	if r16 > 0xff {
		c.Flags |= FCF
	}
	if (a^r)&(b^r)&0x80 != 0 {
		c.Flags |= FOF
	}
	if (a^b^r)&0x10 != 0 {
		c.Flags |= FAF
	}
	c.szp8(r)
	return r
}
func (c *CPU) Add16(a, b uint16) uint16 { return c.adc16(a, b, 0) }
func (c *CPU) Adc16(a, b uint16) uint16 {
	var ci uint16
	if c.CF() {
		ci = 1
	}
	return c.adc16(a, b, ci)
}
func (c *CPU) adc16(a, b, ci uint16) uint16 {
	r32 := uint32(a) + uint32(b) + uint32(ci)
	r := uint16(r32)
	c.Flags &^= FCF | FOF | FAF
	if r32 > 0xffff {
		c.Flags |= FCF
	}
	if (a^r)&(b^r)&0x8000 != 0 {
		c.Flags |= FOF
	}
	if (a^b^r)&0x10 != 0 {
		c.Flags |= FAF
	}
	c.szp16(r)
	return r
}
func (c *CPU) Sub8(a, b uint8) uint8 { return c.sbb8(a, b, 0) }
func (c *CPU) Sbb8(a, b uint8) uint8 {
	var ci uint8
	if c.CF() {
		ci = 1
	}
	return c.sbb8(a, b, ci)
}
func (c *CPU) sbb8(a, b, ci uint8) uint8 {
	r := a - b - ci
	c.Flags &^= FCF | FOF | FAF
	if uint16(a) < uint16(b)+uint16(ci) {
		c.Flags |= FCF
	}
	if (a^b)&(a^r)&0x80 != 0 {
		c.Flags |= FOF
	}
	if (a^b^r)&0x10 != 0 {
		c.Flags |= FAF
	}
	c.szp8(r)
	return r
}
func (c *CPU) Sub16(a, b uint16) uint16 { return c.sbb16(a, b, 0) }
func (c *CPU) Sbb16(a, b uint16) uint16 {
	var ci uint16
	if c.CF() {
		ci = 1
	}
	return c.sbb16(a, b, ci)
}
func (c *CPU) sbb16(a, b, ci uint16) uint16 {
	r := a - b - ci
	c.Flags &^= FCF | FOF | FAF
	if uint32(a) < uint32(b)+uint32(ci) {
		c.Flags |= FCF
	}
	if (a^b)&(a^r)&0x8000 != 0 {
		c.Flags |= FOF
	}
	if (a^b^r)&0x10 != 0 {
		c.Flags |= FAF
	}
	c.szp16(r)
	return r
}
func (c *CPU) Cmp8(a, b uint8)   { c.sbb8(a, b, 0) }
func (c *CPU) Cmp16(a, b uint16) { c.sbb16(a, b, 0) }

func (c *CPU) logic8(r uint8) uint8 {
	c.Flags &^= FCF | FOF | FAF
	c.szp8(r)
	return r
}
func (c *CPU) logic16(r uint16) uint16 {
	c.Flags &^= FCF | FOF | FAF
	c.szp16(r)
	return r
}
func (c *CPU) And8(a, b uint8) uint8    { return c.logic8(a & b) }
func (c *CPU) Or8(a, b uint8) uint8     { return c.logic8(a | b) }
func (c *CPU) Xor8(a, b uint8) uint8    { return c.logic8(a ^ b) }
func (c *CPU) And16(a, b uint16) uint16 { return c.logic16(a & b) }
func (c *CPU) Or16(a, b uint16) uint16  { return c.logic16(a | b) }
func (c *CPU) Xor16(a, b uint16) uint16 { return c.logic16(a ^ b) }
func (c *CPU) Test8(a, b uint8)         { c.logic8(a & b) }
func (c *CPU) Test16(a, b uint16)       { c.logic16(a & b) }

// Alu8 performs ALU operation op (0=add,1=or,2=adc,3=sbb,4=and,5=sub,6=xor,7=cmp).
func (c *CPU) Alu8(op int, a, b uint8) uint8 {
	switch op {
	case 0:
		return c.Add8(a, b)
	case 1:
		return c.Or8(a, b)
	case 2:
		return c.Adc8(a, b)
	case 3:
		return c.Sbb8(a, b)
	case 4:
		return c.And8(a, b)
	case 5:
		return c.Sub8(a, b)
	case 6:
		return c.Xor8(a, b)
	default:
		c.Cmp8(a, b)
		return a
	}
}
func (c *CPU) Alu16(op int, a, b uint16) uint16 {
	switch op {
	case 0:
		return c.Add16(a, b)
	case 1:
		return c.Or16(a, b)
	case 2:
		return c.Adc16(a, b)
	case 3:
		return c.Sbb16(a, b)
	case 4:
		return c.And16(a, b)
	case 5:
		return c.Sub16(a, b)
	case 6:
		return c.Xor16(a, b)
	default:
		c.Cmp16(a, b)
		return a
	}
}

func (c *CPU) Inc8(a uint8) uint8 {
	cf := c.Flags & FCF
	r := c.adc8(a, 1, 0)
	c.Flags = c.Flags&^FCF | cf
	return r
}
func (c *CPU) Dec8(a uint8) uint8 {
	cf := c.Flags & FCF
	r := c.sbb8(a, 1, 0)
	c.Flags = c.Flags&^FCF | cf
	return r
}
func (c *CPU) Inc16(a uint16) uint16 {
	cf := c.Flags & FCF
	r := c.adc16(a, 1, 0)
	c.Flags = c.Flags&^FCF | cf
	return r
}
func (c *CPU) Dec16(a uint16) uint16 {
	cf := c.Flags & FCF
	r := c.sbb16(a, 1, 0)
	c.Flags = c.Flags&^FCF | cf
	return r
}
func (c *CPU) Neg8(a uint8) uint8 {
	r := c.sbb8(0, a, 0)
	c.SetCF(a != 0)
	return r
}
func (c *CPU) Neg16(a uint16) uint16 {
	r := c.sbb16(0, a, 0)
	c.SetCF(a != 0)
	return r
}

// Shift/rotate group (op: 0 rol,1 ror,2 rcl,3 rcr,4 shl,5 shr,6 sal,7 sar).
// The count is masked to 5 bits as on the 80186 and later processors.
func (c *CPU) Shift8(op int, v uint8, cnt uint8) uint8 {
	cnt &= 0x1f
	if cnt == 0 {
		return v
	}
	switch op {
	case 0: // ROL
		n := cnt & 7
		r := v<<n | v>>((8-n)&7)
		c.SetCF(r&1 != 0)
		c.setF(FOF, (r>>7)^(r&1) != 0)
		return r
	case 1: // ROR
		n := cnt & 7
		r := v>>n | v<<((8-n)&7)
		c.SetCF(r&0x80 != 0)
		c.setF(FOF, (r>>7)^((r>>6)&1) != 0)
		return r
	case 2: // RCL
		n := cnt % 9
		r := v
		for i := uint8(0); i < n; i++ {
			cf := r&0x80 != 0
			r = r << 1
			if c.CF() {
				r |= 1
			}
			c.SetCF(cf)
		}
		cfb := uint8(0)
		if c.CF() {
			cfb = 1
		}
		c.setF(FOF, (r>>7)^cfb != 0)
		return r
	case 3: // RCR
		n := cnt % 9
		r := v
		for i := uint8(0); i < n; i++ {
			cf := r&1 != 0
			r = r >> 1
			if c.CF() {
				r |= 0x80
			}
			c.SetCF(cf)
		}
		c.setF(FOF, (r>>7)^((r>>6)&1) != 0)
		return r
	case 4, 6: // SHL
		var r uint8
		if cnt <= 8 {
			c.SetCF((v<<(cnt-1))&0x80 != 0)
			r = uint8(uint16(v) << cnt)
		} else {
			c.SetCF(false)
			r = 0
		}
		c.logicSZP8(r)
		c.setF(FOF, (r>>7 != 0) != c.CF())
		return r
	case 5: // SHR
		var r uint8
		if cnt <= 8 {
			c.SetCF((v>>(cnt-1))&1 != 0)
			r = uint8(uint16(v) >> cnt)
		} else {
			c.SetCF(false)
			r = 0
		}
		c.logicSZP8(r)
		c.setF(FOF, cnt == 1 && v&0x80 != 0)
		return r
	default: // SAR
		s := int8(v)
		if cnt > 8 {
			cnt = 8
		}
		c.SetCF((s>>(cnt-1))&1 != 0)
		r := uint8(s >> cnt)
		c.logicSZP8(r)
		c.setF(FOF, false)
		return r
	}
}
func (c *CPU) logicSZP8(r uint8) {
	c.Flags &^= FAF
	c.szp8(r)
}
func (c *CPU) logicSZP16(r uint16) {
	c.Flags &^= FAF
	c.szp16(r)
}
func (c *CPU) Shift16(op int, v uint16, cnt uint8) uint16 {
	cnt &= 0x1f
	if cnt == 0 {
		return v
	}
	switch op {
	case 0:
		n := cnt & 15
		r := v<<n | v>>((16-n)&15)
		c.SetCF(r&1 != 0)
		c.setF(FOF, (r>>15)^(r&1) != 0)
		return r
	case 1:
		n := cnt & 15
		r := v>>n | v<<((16-n)&15)
		c.SetCF(r&0x8000 != 0)
		c.setF(FOF, (r>>15)^((r>>14)&1) != 0)
		return r
	case 2:
		n := cnt % 17
		r := v
		for i := uint8(0); i < n; i++ {
			cf := r&0x8000 != 0
			r = r << 1
			if c.CF() {
				r |= 1
			}
			c.SetCF(cf)
		}
		cfb := uint16(0)
		if c.CF() {
			cfb = 1
		}
		c.setF(FOF, (r>>15)^cfb != 0)
		return r
	case 3:
		n := cnt % 17
		r := v
		for i := uint8(0); i < n; i++ {
			cf := r&1 != 0
			r = r >> 1
			if c.CF() {
				r |= 0x8000
			}
			c.SetCF(cf)
		}
		c.setF(FOF, (r>>15)^((r>>14)&1) != 0)
		return r
	case 4, 6:
		var r uint16
		if cnt <= 16 {
			c.SetCF((uint32(v)<<(cnt-1))&0x8000 != 0)
			r = uint16(uint32(v) << cnt)
		} else {
			c.SetCF(false)
			r = 0
		}
		c.logicSZP16(r)
		c.setF(FOF, (r>>15 != 0) != c.CF())
		return r
	case 5:
		var r uint16
		if cnt <= 16 {
			c.SetCF((v>>(cnt-1))&1 != 0)
			r = uint16(uint32(v) >> cnt)
		} else {
			c.SetCF(false)
			r = 0
		}
		c.logicSZP16(r)
		c.setF(FOF, cnt == 1 && v&0x8000 != 0)
		return r
	default:
		s := int16(v)
		if cnt > 16 {
			cnt = 16
		}
		c.SetCF((s>>(cnt-1))&1 != 0)
		r := uint16(s >> cnt)
		c.logicSZP16(r)
		c.setF(FOF, false)
		return r
	}
}

// Multiply / divide. Div functions return false on divide error.
func (c *CPU) Mul8(b uint8) {
	r := uint16(c.AL()) * uint16(b)
	c.R[AX] = r
	f := r&0xff00 != 0
	c.setF(FCF, f)
	c.setF(FOF, f)
	c.szp8(uint8(r))
}
func (c *CPU) Imul8(b uint8) {
	r := int16(int8(c.AL())) * int16(int8(b))
	c.R[AX] = uint16(r)
	f := r != int16(int8(r))
	c.setF(FCF, f)
	c.setF(FOF, f)
	c.szp8(uint8(r))
}
func (c *CPU) Mul16(b uint16) {
	r := uint32(c.R[AX]) * uint32(b)
	c.R[AX] = uint16(r)
	c.R[DX] = uint16(r >> 16)
	f := c.R[DX] != 0
	c.setF(FCF, f)
	c.setF(FOF, f)
	c.szp16(uint16(r))
}
func (c *CPU) Imul16(b uint16) {
	r := int32(int16(c.R[AX])) * int32(int16(b))
	c.R[AX] = uint16(r)
	c.R[DX] = uint16(uint32(r) >> 16)
	f := r != int32(int16(r))
	c.setF(FCF, f)
	c.setF(FOF, f)
	c.szp16(uint16(r))
}

// Imul3 is the 80186 three-operand IMUL r16, r/m16, imm.
func (c *CPU) Imul3(a, b uint16) uint16 {
	r := int32(int16(a)) * int32(int16(b))
	f := r != int32(int16(r))
	c.setF(FCF, f)
	c.setF(FOF, f)
	c.szp16(uint16(r))
	return uint16(r)
}
func (c *CPU) Div8(b uint8) bool {
	if b == 0 {
		return false
	}
	a := c.R[AX]
	q := a / uint16(b)
	if q > 0xff {
		return false
	}
	c.R[AX] = uint16(a%uint16(b))<<8 | q
	return true
}
func (c *CPU) Idiv8(b uint8) bool {
	if b == 0 {
		return false
	}
	a := int16(c.R[AX])
	q := a / int16(int8(b))
	if q > 127 || q < -128 {
		return false
	}
	r := a % int16(int8(b))
	c.R[AX] = uint16(uint8(r))<<8 | uint16(uint8(q))
	return true
}
func (c *CPU) Div16(b uint16) bool {
	if b == 0 {
		return false
	}
	a := uint32(c.R[DX])<<16 | uint32(c.R[AX])
	q := a / uint32(b)
	if q > 0xffff {
		return false
	}
	c.R[AX] = uint16(q)
	c.R[DX] = uint16(a % uint32(b))
	return true
}
func (c *CPU) Idiv16(b uint16) bool {
	if b == 0 {
		return false
	}
	a := int32(uint32(c.R[DX])<<16 | uint32(c.R[AX]))
	q := a / int32(int16(b))
	if q > 32767 || q < -32768 {
		return false
	}
	c.R[AX] = uint16(q)
	c.R[DX] = uint16(a % int32(int16(b)))
	return true
}

// BCD adjust instructions.
func (c *CPU) Daa() {
	al := c.AL()
	cf := c.CF()
	if al&0xf > 9 || c.Flags&FAF != 0 {
		c.Flags |= FAF
		cf = cf || al > 0xf9
		al += 6
	} else {
		c.Flags &^= FAF
	}
	if c.AL() > 0x99 || c.CF() {
		al += 0x60
		cf = true
	}
	c.SetCF(cf)
	c.SetAL(al)
	c.szp8(al)
}
func (c *CPU) Das() {
	al := c.AL()
	old := al
	cf := c.CF()
	if al&0xf > 9 || c.Flags&FAF != 0 {
		c.Flags |= FAF
		cf = cf || al < 6
		al -= 6
	} else {
		c.Flags &^= FAF
	}
	if old > 0x99 || c.CF() {
		al -= 0x60
		cf = true
	}
	c.SetCF(cf)
	c.SetAL(al)
	c.szp8(al)
}
func (c *CPU) Aaa() {
	if c.AL()&0xf > 9 || c.Flags&FAF != 0 {
		c.R[AX] += 0x106
		c.Flags |= FAF | FCF
	} else {
		c.Flags &^= FAF | FCF
	}
	c.SetAL(c.AL() & 0xf)
}
func (c *CPU) Aas() {
	if c.AL()&0xf > 9 || c.Flags&FAF != 0 {
		c.R[AX] -= 6
		c.SetAH(c.AH() - 1)
		c.Flags |= FAF | FCF
	} else {
		c.Flags &^= FAF | FCF
	}
	c.SetAL(c.AL() & 0xf)
}
func (c *CPU) Aam(b uint8) bool {
	if b == 0 {
		return false
	}
	al := c.AL()
	c.SetAH(al / b)
	c.SetAL(al % b)
	c.szp8(c.AL())
	return true
}
func (c *CPU) Aad(b uint8) {
	al := c.AL() + c.AH()*b
	c.R[AX] = uint16(al)
	c.szp8(al)
}
