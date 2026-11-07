package machine

// Inst is a decoded 8086/80186 real-mode instruction.
type Inst struct {
	Len   int
	Op    byte // primary opcode (0x0F escape is folded into Op2)
	Op2   byte // second opcode byte for 0x0F instructions
	Rep   byte // 0, 0xF2 (REPNE) or 0xF3 (REP/REPE)
	Seg   int  // segment override, -1 if none
	Lock  bool
	ModRM bool
	Mod   byte
	Reg   byte
	RM    byte
	Disp  uint16
	Imm   uint16
	Imm2  uint16
	Bad   bool
}

// Byte-sized operand?
func (in *Inst) Wide() bool { return in.Op&1 != 0 }

type fetcher func(i int) byte

// hasModRM / immediate size tables for one-byte opcodes.
func opInfo(op byte) (modrm bool, imm int) {
	switch {
	case op < 0x40 && op&7 < 4:
		return true, 0
	case op < 0x40 && op&7 == 4:
		return false, 1
	case op < 0x40 && op&7 == 5:
		return false, 2
	}
	switch op {
	case 0x62, 0x63: // BOUND, ARPL(invalid in real mode)
		return true, 0
	case 0x68:
		return false, 2
	case 0x69:
		return true, 2
	case 0x6a:
		return false, 1
	case 0x6b:
		return true, 1
	case 0x80, 0x82, 0x83, 0xc0, 0xc1, 0xc6:
		return true, 1
	case 0x81, 0xc7:
		return true, 2
	case 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x8b, 0x8c, 0x8d, 0x8e, 0x8f,
		0xc4, 0xc5, 0xd0, 0xd1, 0xd2, 0xd3, 0xd8, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf,
		0xfe, 0xff:
		return true, 0
	case 0xf6, 0xf7:
		return true, -1 // depends on reg
	case 0xa0, 0xa1, 0xa2, 0xa3:
		return false, 2
	case 0xa8, 0xcd, 0xd4, 0xd5, 0xe4, 0xe5, 0xe6, 0xe7, 0xeb, 0xe0, 0xe1, 0xe2, 0xe3:
		return false, 1
	case 0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0x7f:
		return false, 1
	case 0xa9, 0xc2, 0xca, 0xe8, 0xe9:
		return false, 2
	case 0x9a, 0xea:
		return false, 4
	case 0xc8:
		return false, 3
	}
	if op >= 0xb0 && op <= 0xb7 {
		return false, 1
	}
	if op >= 0xb8 && op <= 0xbf {
		return false, 2
	}
	return false, 0
}

// Decode decodes one instruction. f(i) returns the i-th byte from the
// instruction start (IP wrap-around is the caller's business).
func Decode(f fetcher) Inst {
	in := Inst{Seg: -1}
	i := 0
	for {
		b := f(i)
		switch b {
		case 0x26:
			in.Seg = ES
		case 0x2e:
			in.Seg = CS
		case 0x36:
			in.Seg = SS
		case 0x3e:
			in.Seg = DS
		case 0xf2, 0xf3:
			in.Rep = b
		case 0xf0:
			in.Lock = true
		default:
			goto op
		}
		i++
		if i > 14 {
			in.Bad = true
			in.Len = i
			return in
		}
	}
op:
	in.Op = f(i)
	i++
	modrm, imm := opInfo(in.Op)
	if in.Op == 0x0f {
		in.Op2 = f(i)
		i++
		// Only a few 0x0F opcodes might appear; treat as invalid.
		in.Bad = true
		in.Len = i
		return in
	}
	if modrm {
		mb := f(i)
		i++
		in.ModRM = true
		in.Mod = mb >> 6
		in.Reg = (mb >> 3) & 7
		in.RM = mb & 7
		switch in.Mod {
		case 0:
			if in.RM == 6 {
				in.Disp = uint16(f(i)) | uint16(f(i+1))<<8
				i += 2
			}
		case 1:
			in.Disp = uint16(int16(int8(f(i))))
			i++
		case 2:
			in.Disp = uint16(f(i)) | uint16(f(i+1))<<8
			i += 2
		}
		if imm == -1 {
			if in.Reg < 2 {
				if in.Op == 0xf6 {
					imm = 1
				} else {
					imm = 2
				}
			} else {
				imm = 0
			}
		}
	}
	switch imm {
	case 1:
		in.Imm = uint16(f(i))
		i++
	case 2:
		in.Imm = uint16(f(i)) | uint16(f(i+1))<<8
		i += 2
	case 3:
		in.Imm = uint16(f(i)) | uint16(f(i+1))<<8
		in.Imm2 = uint16(f(i + 2))
		i += 3
	case 4:
		in.Imm = uint16(f(i)) | uint16(f(i+1))<<8
		in.Imm2 = uint16(f(i+2)) | uint16(f(i+3))<<8
		i += 4
	}
	in.Len = i
	return in
}

// IsJump reports whether the instruction transfers control (ends a block).
func (in *Inst) EndsBlock() bool {
	op := in.Op
	switch {
	case op >= 0x70 && op <= 0x7f:
		return true
	case op >= 0xe0 && op <= 0xe3:
		return true
	}
	switch op {
	case 0x9a, 0xc2, 0xc3, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf, 0xe8, 0xe9, 0xea, 0xeb, 0xf4:
		return true
	case 0xff:
		return in.Reg >= 2 && in.Reg <= 5
	case 0x9d, 0xfb: // popf / sti may enable interrupts
		return true
	}
	return false
}
