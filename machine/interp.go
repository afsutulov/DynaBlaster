package machine

// This file contains a reference interpreter for the 8086/80186
// instruction set. It is used for the BIOS stubs, as a safety net for any
// code path that was not translated ahead of time, and by the developer
// tools that discover and verify the translated code.

// EA computes the effective address offset and default segment of a
// ModRM memory operand.
func (m *M) EA(in *Inst) (seg uint16, off uint16) {
	def := DS
	switch in.RM {
	case 0:
		off = m.R[BX] + m.R[SI]
	case 1:
		off = m.R[BX] + m.R[DI]
	case 2:
		off = m.R[BP] + m.R[SI]
		def = SS
	case 3:
		off = m.R[BP] + m.R[DI]
		def = SS
	case 4:
		off = m.R[SI]
	case 5:
		off = m.R[DI]
	case 6:
		if in.Mod == 0 {
			off = 0
		} else {
			off = m.R[BP]
			def = SS
		}
	case 7:
		off = m.R[BX]
	}
	off += in.Disp
	if in.Seg >= 0 {
		def = in.Seg
	}
	return m.S[def], off
}

func (m *M) rm8(in *Inst) uint8 {
	if in.Mod == 3 {
		return m.R8(int(in.RM))
	}
	s, o := m.EA(in)
	return m.RB(s, o)
}
func (m *M) setRM8(in *Inst, v uint8) {
	if in.Mod == 3 {
		m.SetR8(int(in.RM), v)
		return
	}
	s, o := m.EA(in)
	m.WB(s, o, v)
}
func (m *M) rm16(in *Inst) uint16 {
	if in.Mod == 3 {
		return m.R[in.RM]
	}
	s, o := m.EA(in)
	return m.RW(s, o)
}
func (m *M) setRM16(in *Inst, v uint16) {
	if in.Mod == 3 {
		m.R[in.RM] = v
		return
	}
	s, o := m.EA(in)
	m.WW(s, o, v)
}

func (m *M) dsSeg(in *Inst) uint16 {
	if in.Seg >= 0 {
		return m.S[in.Seg]
	}
	return m.S[DS]
}

// FetchInst decodes the instruction at CS:IP.
func (m *M) FetchInst() Inst {
	cs, ip := m.S[CS], m.IP
	return Decode(func(i int) byte { return m.RB(cs, ip+uint16(i)) })
}

// Interp executes exactly one instruction (with its REP prefix).
func (m *M) Interp() Inst {
	in := m.FetchInst()
	m.InterpSteps++
	m.Cycles++
	if m.Trace != nil {
		m.Trace(m)
	}
	m.ExecInst(&in)
	return in
}

// ExecInst executes a decoded instruction located at CS:IP.
func (m *M) ExecInst(in *Inst) {
	next := m.IP + uint16(in.Len)
	if in.Bad {
		m.fault("invalid opcode %02X %02X", in.Op, in.Op2)
	}
	op := in.Op
	switch {
	case op < 0x40 && op&7 < 6:
		alu := int(op >> 3)
		switch op & 7 {
		case 0:
			r := m.Alu8(alu, m.rm8(in), m.R8(int(in.Reg)))
			if alu != 7 {
				m.setRM8(in, r)
			}
		case 1:
			r := m.Alu16(alu, m.rm16(in), m.R[in.Reg])
			if alu != 7 {
				m.setRM16(in, r)
			}
		case 2:
			r := m.Alu8(alu, m.R8(int(in.Reg)), m.rm8(in))
			if alu != 7 {
				m.SetR8(int(in.Reg), r)
			}
		case 3:
			r := m.Alu16(alu, m.R[in.Reg], m.rm16(in))
			if alu != 7 {
				m.R[in.Reg] = r
			}
		case 4:
			r := m.Alu8(alu, m.AL(), uint8(in.Imm))
			if alu != 7 {
				m.SetAL(r)
			}
		case 5:
			r := m.Alu16(alu, m.R[AX], in.Imm)
			if alu != 7 {
				m.R[AX] = r
			}
		}
		m.IP = next
		return
	}
	switch op {
	case 0x06, 0x0e, 0x16, 0x1e:
		m.Push(m.S[op>>3])
	case 0x07, 0x17, 0x1f:
		m.S[op>>3] = m.Pop()
	case 0x0f:
		m.fault("pop cs")
	case 0x27:
		m.Daa()
	case 0x2f:
		m.Das()
	case 0x37:
		m.Aaa()
	case 0x3f:
		m.Aas()
	case 0x40, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47:
		m.R[op&7] = m.Inc16(m.R[op&7])
	case 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f:
		m.R[op&7] = m.Dec16(m.R[op&7])
	case 0x50, 0x51, 0x52, 0x53, 0x55, 0x56, 0x57:
		m.Push(m.R[op&7])
	case 0x54: // 80286+: pushes the value before the decrement
		v := m.R[SP]
		m.Push(v)
	case 0x58, 0x59, 0x5a, 0x5b, 0x5c, 0x5d, 0x5e, 0x5f:
		m.R[op&7] = m.Pop()
	case 0x60: // PUSHA
		sp := m.R[SP]
		for i := 0; i < 8; i++ {
			if i == SP {
				m.Push(sp)
			} else {
				m.Push(m.R[i])
			}
		}
	case 0x61: // POPA
		for i := 7; i >= 0; i-- {
			v := m.Pop()
			if i != SP {
				m.R[i] = v
			}
		}
	case 0x62: // BOUND
		s, o := m.EA(in)
		lo, hi := int16(m.RW(s, o)), int16(m.RW(s, o+2))
		v := int16(m.R[in.Reg])
		if v < lo || v > hi {
			m.Int(5, m.IP) // the fault returns to the instruction itself
			return
		}
	case 0x68:
		m.Push(in.Imm)
	case 0x69:
		m.R[in.Reg] = m.Imul3(m.rm16(in), in.Imm)
	case 0x6a:
		m.Push(uint16(int16(int8(in.Imm))))
	case 0x6b:
		m.R[in.Reg] = m.Imul3(m.rm16(in), uint16(int16(int8(in.Imm))))
	case 0x6c, 0x6d, 0x6e, 0x6f, 0xa4, 0xa5, 0xa6, 0xa7, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf:
		m.stringOp(in)
	case 0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0x7f:
		if m.Cond(int(op & 15)) {
			next += uint16(int16(int8(in.Imm)))
		}
	case 0x80, 0x82:
		r := m.Alu8(int(in.Reg), m.rm8(in), uint8(in.Imm))
		if in.Reg != 7 {
			m.setRM8(in, r)
		}
	case 0x81:
		r := m.Alu16(int(in.Reg), m.rm16(in), in.Imm)
		if in.Reg != 7 {
			m.setRM16(in, r)
		}
	case 0x83:
		r := m.Alu16(int(in.Reg), m.rm16(in), uint16(int16(int8(in.Imm))))
		if in.Reg != 7 {
			m.setRM16(in, r)
		}
	case 0x84:
		m.Test8(m.rm8(in), m.R8(int(in.Reg)))
	case 0x85:
		m.Test16(m.rm16(in), m.R[in.Reg])
	case 0x86:
		a, b := m.rm8(in), m.R8(int(in.Reg))
		m.setRM8(in, b)
		m.SetR8(int(in.Reg), a)
	case 0x87:
		a, b := m.rm16(in), m.R[in.Reg]
		m.setRM16(in, b)
		m.R[in.Reg] = a
	case 0x88:
		m.setRM8(in, m.R8(int(in.Reg)))
	case 0x89:
		m.setRM16(in, m.R[in.Reg])
	case 0x8a:
		m.SetR8(int(in.Reg), m.rm8(in))
	case 0x8b:
		m.R[in.Reg] = m.rm16(in)
	case 0x8c:
		m.setRM16(in, m.S[in.Reg&3])
	case 0x8d:
		_, o := m.EA(in)
		m.R[in.Reg] = o
	case 0x8e:
		m.S[in.Reg&3] = m.rm16(in)
	case 0x8f:
		v := m.Pop()
		m.setRM16(in, v)
	case 0x90:
	case 0x91, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97:
		m.R[AX], m.R[op&7] = m.R[op&7], m.R[AX]
	case 0x98:
		m.R[AX] = uint16(int16(int8(m.AL())))
	case 0x99:
		if m.R[AX]&0x8000 != 0 {
			m.R[DX] = 0xffff
		} else {
			m.R[DX] = 0
		}
	case 0x9a:
		m.CallFar(in.Imm2, in.Imm, next)
		return
	case 0x9b: // WAIT
	case 0x9c:
		m.PushF()
	case 0x9d:
		m.PopF()
	case 0x9e:
		m.Flags = m.Flags&0xff00 | uint16(m.AH())&0xd5 | 2
	case 0x9f:
		m.SetAH(uint8(m.Flags&0xd5 | 2))
	case 0xa0:
		m.SetAL(m.RB(m.dsSeg(in), in.Imm))
	case 0xa1:
		m.R[AX] = m.RW(m.dsSeg(in), in.Imm)
	case 0xa2:
		m.WB(m.dsSeg(in), in.Imm, m.AL())
	case 0xa3:
		m.WW(m.dsSeg(in), in.Imm, m.R[AX])
	case 0xa8:
		m.Test8(m.AL(), uint8(in.Imm))
	case 0xa9:
		m.Test16(m.R[AX], in.Imm)
	case 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7:
		m.SetR8(int(op&7), uint8(in.Imm))
	case 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf:
		m.R[op&7] = in.Imm
	case 0xc0, 0xd0, 0xd2:
		var c uint8 = 1
		if op == 0xc0 {
			c = uint8(in.Imm)
		} else if op == 0xd2 {
			c = m.CL()
		}
		m.setRM8(in, m.Shift8(int(in.Reg), m.rm8(in), c))
	case 0xc1, 0xd1, 0xd3:
		var c uint8 = 1
		if op == 0xc1 {
			c = uint8(in.Imm)
		} else if op == 0xd3 {
			c = m.CL()
		}
		m.setRM16(in, m.Shift16(int(in.Reg), m.rm16(in), c))
	case 0xc2:
		m.Ret(in.Imm)
		return
	case 0xc3:
		m.Ret(0)
		return
	case 0xc4, 0xc5:
		s, o := m.EA(in)
		m.R[in.Reg] = m.RW(s, o)
		v := m.RW(s, o+2)
		if op == 0xc4 {
			m.S[ES] = v
		} else {
			m.S[DS] = v
		}
	case 0xc6:
		m.setRM8(in, uint8(in.Imm))
	case 0xc7:
		m.setRM16(in, in.Imm)
	case 0xc8: // ENTER
		m.Push(m.R[BP])
		fp := m.R[SP]
		lvl := in.Imm2 & 31
		for i := uint16(1); i < lvl; i++ {
			m.R[BP] -= 2
			m.Push(m.RW(m.S[SS], m.R[BP]))
		}
		if lvl > 0 {
			m.Push(fp)
		}
		m.R[BP] = fp
		m.R[SP] -= in.Imm
	case 0xc9: // LEAVE
		m.R[SP] = m.R[BP]
		m.R[BP] = m.Pop()
	case 0xca:
		m.RetFar(in.Imm)
		return
	case 0xcb:
		m.RetFar(0)
		return
	case 0xcc:
		m.Int(3, next)
		return
	case 0xcd:
		m.Int(uint8(in.Imm), next)
		return
	case 0xce:
		if m.OF() {
			m.Int(4, next)
			return
		}
	case 0xcf:
		m.Iret()
		return
	case 0xd4:
		if !m.Aam(uint8(in.Imm)) {
			m.DivError(next)
			return
		}
	case 0xd5:
		m.Aad(uint8(in.Imm))
	case 0xd6: // SALC
		if m.CF() {
			m.SetAL(0xff)
		} else {
			m.SetAL(0)
		}
	case 0xd7:
		m.SetAL(m.RB(m.dsSeg(in), m.R[BX]+uint16(m.AL())))
	case 0xd8, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf:
		// no FPU present: ignore
	case 0xe0, 0xe1, 0xe2:
		m.R[CX]--
		t := m.R[CX] != 0
		if op == 0xe0 {
			t = t && !m.ZF()
		} else if op == 0xe1 {
			t = t && m.ZF()
		}
		if t {
			next += uint16(int16(int8(in.Imm)))
		}
	case 0xe3:
		if m.R[CX] == 0 {
			next += uint16(int16(int8(in.Imm)))
		}
	case 0xe4:
		m.SetAL(m.In8(in.Imm))
	case 0xe5:
		m.R[AX] = m.In16(in.Imm)
	case 0xe6:
		m.Out8(in.Imm, m.AL())
	case 0xe7:
		m.Out16(in.Imm, m.R[AX])
	case 0xe8:
		m.Call(next+in.Imm, next)
		return
	case 0xe9:
		next += in.Imm
	case 0xea:
		m.S[CS] = in.Imm2
		m.IP = in.Imm
		return
	case 0xeb:
		next += uint16(int16(int8(in.Imm)))
	case 0xec:
		m.SetAL(m.In8(m.R[DX]))
	case 0xed:
		m.R[AX] = m.In16(m.R[DX])
	case 0xee:
		m.Out8(m.R[DX], m.AL())
	case 0xef:
		m.Out16(m.R[DX], m.R[AX])
	case 0xf4: // HLT: wait for the next interrupt
		m.IP = next
		m.Clk.Idle(m)
		return
	case 0xf5:
		m.Flags ^= FCF
	case 0xf6:
		v := m.rm8(in)
		switch in.Reg {
		case 0, 1:
			m.Test8(v, uint8(in.Imm))
		case 2:
			m.setRM8(in, ^v)
		case 3:
			m.setRM8(in, m.Neg8(v))
		case 4:
			m.Mul8(v)
		case 5:
			m.Imul8(v)
		case 6:
			if !m.Div8(v) {
				m.DivError(next)
				return
			}
		case 7:
			if !m.Idiv8(v) {
				m.DivError(next)
				return
			}
		}
	case 0xf7:
		v := m.rm16(in)
		switch in.Reg {
		case 0, 1:
			m.Test16(v, in.Imm)
		case 2:
			m.setRM16(in, ^v)
		case 3:
			m.setRM16(in, m.Neg16(v))
		case 4:
			m.Mul16(v)
		case 5:
			m.Imul16(v)
		case 6:
			if !m.Div16(v) {
				m.DivError(next)
				return
			}
		case 7:
			if !m.Idiv16(v) {
				m.DivError(next)
				return
			}
		}
	case 0xf8:
		m.Flags &^= FCF
	case 0xf9:
		m.Flags |= FCF
	case 0xfa:
		m.Flags &^= FIF
	case 0xfb:
		m.Flags |= FIF
	case 0xfc:
		m.Flags &^= FDF
	case 0xfd:
		m.Flags |= FDF
	case 0xfe:
		v := m.rm8(in)
		switch in.Reg {
		case 0:
			m.setRM8(in, m.Inc8(v))
		case 1:
			m.setRM8(in, m.Dec8(v))
		default:
			m.fault("bad FE /%d", in.Reg)
		}
	case 0xff:
		switch in.Reg {
		case 0:
			m.setRM16(in, m.Inc16(m.rm16(in)))
		case 1:
			m.setRM16(in, m.Dec16(m.rm16(in)))
		case 2:
			t := m.rm16(in)
			m.Call(t, next)
			return
		case 3:
			s, o := m.EA(in)
			off, seg := m.RW(s, o), m.RW(s, o+2)
			m.CallFar(seg, off, next)
			return
		case 4:
			m.IP = m.rm16(in)
			return
		case 5:
			s, o := m.EA(in)
			m.IP = m.RW(s, o)
			m.S[CS] = m.RW(s, o+2)
			return
		case 6:
			m.Push(m.rm16(in))
		default:
			m.fault("bad FF /7")
		}
	default:
		m.fault("unimplemented opcode %02X", op)
	}
	m.IP = next
}

// stringOp executes a (possibly repeated) string instruction.
func (m *M) stringOp(in *Inst) {
	m.StringOp(in.Op, in.Rep, m.dsSeg(in))
	m.IP += uint16(in.Len)
}

// StringOp executes string instruction op with prefix rep; src is the
// source segment (DS or the override).
func (m *M) StringOp(op, rep byte, src uint16) {
	var d uint16 = 1
	if op&1 != 0 {
		d = 2
	}
	if m.DF() {
		d = -d
	}
	// fast paths for the common forward block moves/fills
	if rep == 0xf3 && d == 1 && op == 0xa4 {
		m.repMovsb(src)
		return
	}
	if rep == 0xf3 && d == 1 && op == 0xaa {
		m.repStosb()
		return
	}
	for {
		if rep != 0 && m.R[CX] == 0 {
			break
		}
		switch op {
		case 0xa4:
			m.WB(m.S[ES], m.R[DI], m.RB(src, m.R[SI]))
			m.R[SI] += d
			m.R[DI] += d
		case 0xa5:
			m.WW(m.S[ES], m.R[DI], m.RW(src, m.R[SI]))
			m.R[SI] += d
			m.R[DI] += d
		case 0xa6:
			m.Cmp8(m.RB(src, m.R[SI]), m.RB(m.S[ES], m.R[DI]))
			m.R[SI] += d
			m.R[DI] += d
		case 0xa7:
			m.Cmp16(m.RW(src, m.R[SI]), m.RW(m.S[ES], m.R[DI]))
			m.R[SI] += d
			m.R[DI] += d
		case 0xaa:
			m.WB(m.S[ES], m.R[DI], m.AL())
			m.R[DI] += d
		case 0xab:
			m.WW(m.S[ES], m.R[DI], m.R[AX])
			m.R[DI] += d
		case 0xac:
			m.SetAL(m.RB(src, m.R[SI]))
			m.R[SI] += d
		case 0xad:
			m.R[AX] = m.RW(src, m.R[SI])
			m.R[SI] += d
		case 0xae:
			m.Cmp8(m.AL(), m.RB(m.S[ES], m.R[DI]))
			m.R[DI] += d
		case 0xaf:
			m.Cmp16(m.R[AX], m.RW(m.S[ES], m.R[DI]))
			m.R[DI] += d
		case 0x6c:
			m.WB(m.S[ES], m.R[DI], m.In8(m.R[DX]))
			m.R[DI] += d
		case 0x6d:
			m.WW(m.S[ES], m.R[DI], m.In16(m.R[DX]))
			m.R[DI] += d
		case 0x6e:
			m.Out8(m.R[DX], m.RB(src, m.R[SI]))
			m.R[SI] += d
		case 0x6f:
			m.Out16(m.R[DX], m.RW(src, m.R[SI]))
			m.R[SI] += d
		}
		if rep == 0 {
			break
		}
		m.R[CX]--
		if op == 0xa6 || op == 0xa7 || op == 0xae || op == 0xaf {
			if rep == 0xf3 && !m.ZF() {
				break
			}
			if rep == 0xf2 && m.ZF() {
				break
			}
		}
	}
}

// repMovsb is REP MOVSB with DF=0.
func (m *M) repMovsb(src uint16) {
	for m.R[CX] != 0 {
		m.WB(m.S[ES], m.R[DI], m.RB(src, m.R[SI]))
		m.R[SI]++
		m.R[DI]++
		m.R[CX]--
	}
}

// repStosb is REP STOSB with DF=0.
func (m *M) repStosb() {
	al := m.AL()
	for m.R[CX] != 0 {
		m.WB(m.S[ES], m.R[DI], al)
		m.R[DI]++
		m.R[CX]--
	}
}

// ---- helpers used by translated code ----

func (m *M) Pusha() {
	sp := m.R[SP]
	for i := 0; i < 8; i++ {
		if i == SP {
			m.Push(sp)
		} else {
			m.Push(m.R[i])
		}
	}
}

func (m *M) Popa() {
	for i := 7; i >= 0; i-- {
		v := m.Pop()
		if i != SP {
			m.R[i] = v
		}
	}
}

func (m *M) Cwd() {
	if m.R[AX]&0x8000 != 0 {
		m.R[DX] = 0xffff
	} else {
		m.R[DX] = 0
	}
}

func (m *M) Sahf() { m.Flags = m.Flags&0xff00 | uint16(m.AH())&0xd5 | 2 }
func (m *M) Lahf() { m.SetAH(uint8(m.Flags&0xd5 | 2)) }

func (m *M) Salc() {
	if m.CF() {
		m.SetAL(0xff)
	} else {
		m.SetAL(0)
	}
}

func (m *M) Enter(size uint16, lvl uint16) {
	m.Push(m.R[BP])
	fp := m.R[SP]
	lvl &= 31
	for i := uint16(1); i < lvl; i++ {
		m.R[BP] -= 2
		m.Push(m.RW(m.S[SS], m.R[BP]))
	}
	if lvl > 0 {
		m.Push(fp)
	}
	m.R[BP] = fp
	m.R[SP] -= size
}

func (m *M) Leave() {
	m.R[SP] = m.R[BP]
	m.R[BP] = m.Pop()
}
