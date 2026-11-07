package machine

import (
	"encoding/binary"
	"errors"
)

// BIOS segment containing the interrupt service stubs.
const biosSeg = 0xf000

func stubOff(n int) uint16 { return uint16(0x1000 + n*16) }

func (m *M) setupBIOS() {
	// Every vector points at an IRET-ing stub by default.
	for n := 0; n < 256; n++ {
		off := stubOff(n)
		binary.LittleEndian.PutUint16(m.Mem[n*4:], off)
		binary.LittleEndian.PutUint16(m.Mem[n*4+2:], biosSeg)
		a := Lin(biosSeg, off)
		m.Mem[a] = 0xcf // IRET (also used by the interpreter)
		nn := n
		m.traps[a] = func(m *M) { m.biosInt(nn) }
		m.traps[a+8] = func(m *M) { m.biosInt2(nn) }
	}
	// BIOS data area
	binary.LittleEndian.PutUint16(m.Mem[0x413:], 640)
	binary.LittleEndian.PutUint16(m.Mem[0x463:], 0x3d4)
	m.Mem[0x449] = 0x03
	binary.LittleEndian.PutUint16(m.Mem[0x44a:], 80)
	m.Mem[0x484] = 24
	binary.LittleEndian.PutUint16(m.Mem[0x408:], 0x378) // LPT1 base
	// Machine ID byte
	m.Mem[0xffffe] = 0xfc
}

func (m *M) eoi() { m.PIC.Out(0x20, 0x20) }

// biosInt is the first half of the BIOS service for interrupt n. It runs
// with CS:IP at the stub; it must leave CS:IP somewhere sensible.
func (m *M) biosInt(n int) {
	m.Cycles += 20
	switch n {
	case 0x08: // timer tick
		t := binary.LittleEndian.Uint32(m.Mem[0x46c:]) + 1
		if t >= 0x1800b0 {
			t = 0
			m.Mem[0x470] = 1
		}
		binary.LittleEndian.PutUint32(m.Mem[0x46c:], t)
		m.Int(0x1c, stubOff(n)+8) // continue in second half
		return
	case 0x09:
		m.bios9()
		m.eoi()
	case 0x10:
		m.Iret()
		m.int10()
		return
	case 0x11:
		m.Iret()
		m.R[AX] = 0x0026
		return
	case 0x12:
		m.Iret()
		m.R[AX] = 640
		return
	case 0x15:
		m.Iret()
		m.int15()
		return
	case 0x16:
		m.int16()
		return
	case 0x1a:
		m.Iret()
		m.int1a()
		return
	case 0x21:
		m.Iret()
		m.DOS.Int21()
		return
	case 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f:
		m.eoi()
	case 0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77:
		m.PIC.Out(0xa0, 0x20)
		m.eoi()
	case 0x33:
		m.Iret()
		m.R[AX] = 0 // no mouse
		return
	}
	m.Iret()
}

// biosInt2 is the continuation after a chained call (INT 8 -> INT 1Ch).
func (m *M) biosInt2(n int) {
	if n == 0x08 {
		m.eoi()
	}
	m.Iret()
}

func (m *M) int10() {
	switch m.AH() {
	case 0x00:
		mode := m.AL()
		m.VGA.SetBIOSMode(mode)
		m.Mem[0x449] = mode & 0x7f
	case 0x0f:
		m.SetAL(m.Mem[0x449])
		m.SetAH(80)
		m.SetBH(0)
	case 0x10:
		switch m.AL() {
		case 0x00:
			m.VGA.Attr[m.BL()&15] = m.BH()
		case 0x10:
			i := m.R[BX] & 0xff
			m.VGA.DAC[i] = [3]uint8{m.DH() & 63, m.CH() & 63, m.CL() & 63}
		case 0x12:
			for k := uint16(0); k < m.R[CX]; k++ {
				i := (m.R[BX] + k) & 0xff
				o := m.R[DX] + k*3
				m.VGA.DAC[i] = [3]uint8{m.RB(m.S[ES], o) & 63, m.RB(m.S[ES], o+1) & 63, m.RB(m.S[ES], o+2) & 63}
			}
		case 0x15:
			e := m.VGA.DAC[m.R[BX]&0xff]
			m.SetDH(e[0])
			m.SetCH(e[1])
			m.SetCL(e[2])
		case 0x17:
			for k := uint16(0); k < m.R[CX]; k++ {
				e := m.VGA.DAC[(m.R[BX]+k)&0xff]
				o := m.R[DX] + k*3
				m.WB(m.S[ES], o, e[0])
				m.WB(m.S[ES], o+1, e[1])
				m.WB(m.S[ES], o+2, e[2])
			}
		}
	case 0x1a:
		m.SetAL(0x1a)
		m.SetBL(8)
		m.SetBH(0)
	case 0x12:
		if m.BL() == 0x10 {
			m.SetBH(0)
			m.SetBL(3)
		}
	}
}

func (m *M) int15() {
	switch m.AH() {
	case 0x84: // joystick: not present
		m.Flags |= FCF
		m.SetAH(0x86)
	case 0xc0:
		m.Flags |= FCF
		m.SetAH(0x86)
	case 0x88:
		m.R[AX] = 0
		m.Flags &^= FCF
	default:
		m.Flags |= FCF
		m.SetAH(0x86)
	}
}

func (m *M) int1a() {
	switch m.AH() {
	case 0x00:
		t := binary.LittleEndian.Uint32(m.Mem[0x46c:])
		m.R[CX] = uint16(t >> 16)
		m.R[DX] = uint16(t)
		m.SetAL(m.Mem[0x470])
		m.Mem[0x470] = 0
	case 0x02:
		t := m.DOS.Now()
		bcd := func(v int) uint8 { return uint8(v/10<<4 | v%10) }
		m.SetCH(bcd(t.Hour()))
		m.SetCL(bcd(t.Minute()))
		m.SetDH(bcd(t.Second()))
		m.Flags &^= FCF
	}
}

func (m *M) int16() {
	switch m.AH() {
	case 0x00, 0x10:
		if k, ok := m.biosKey(true); ok {
			m.Iret()
			m.R[AX] = k
			return
		}
		// Wait for a key: stay on the stub with interrupts enabled.
		m.Flags |= FIF
		m.Clk.Idle(m)
		return
	case 0x01, 0x11:
		k, ok := m.biosKey(false)
		m.Iret()
		if ok {
			m.R[AX] = k
			m.Flags &^= FZF
		} else {
			m.Flags |= FZF
		}
		return
	case 0x02:
		m.Iret()
		m.SetAL(m.Kbd.Shift)
		return
	}
	m.Iret()
}

// Scan code set 1 to ASCII (unshifted, shifted).
var scanASCII = [][2]byte{
	0x02: {'1', '!'}, 0x03: {'2', '@'}, 0x04: {'3', '#'}, 0x05: {'4', '$'}, 0x06: {'5', '%'},
	0x07: {'6', '^'}, 0x08: {'7', '&'}, 0x09: {'8', '*'}, 0x0a: {'9', '('}, 0x0b: {'0', ')'},
	0x0c: {'-', '_'}, 0x0d: {'=', '+'}, 0x0e: {8, 8}, 0x0f: {9, 0},
	0x10: {'q', 'Q'}, 0x11: {'w', 'W'}, 0x12: {'e', 'E'}, 0x13: {'r', 'R'}, 0x14: {'t', 'T'},
	0x15: {'y', 'Y'}, 0x16: {'u', 'U'}, 0x17: {'i', 'I'}, 0x18: {'o', 'O'}, 0x19: {'p', 'P'},
	0x1a: {'[', '{'}, 0x1b: {']', '}'}, 0x1c: {13, 13},
	0x1e: {'a', 'A'}, 0x1f: {'s', 'S'}, 0x20: {'d', 'D'}, 0x21: {'f', 'F'}, 0x22: {'g', 'G'},
	0x23: {'h', 'H'}, 0x24: {'j', 'J'}, 0x25: {'k', 'K'}, 0x26: {'l', 'L'}, 0x27: {';', ':'},
	0x28: {'\'', '"'}, 0x29: {'`', '~'}, 0x2b: {'\\', '|'},
	0x2c: {'z', 'Z'}, 0x2d: {'x', 'X'}, 0x2e: {'c', 'C'}, 0x2f: {'v', 'V'}, 0x30: {'b', 'B'},
	0x31: {'n', 'N'}, 0x32: {'m', 'M'}, 0x33: {',', '<'}, 0x34: {'.', '>'}, 0x35: {'/', '?'},
	0x39: {' ', ' '}, 0x01: {27, 27},
}

func (m *M) bios9() {
	sc := m.In8(0x60)
	k := m.Kbd
	if sc == 0xe0 {
		k.e0 = true
		return
	}
	e0 := k.e0
	k.e0 = false
	rel := sc&0x80 != 0
	c := sc & 0x7f
	setShift := func(bit uint8) {
		if rel {
			k.Shift &^= bit
		} else {
			k.Shift |= bit
		}
	}
	switch c {
	case 0x2a:
		setShift(2)
		return
	case 0x36:
		setShift(1)
		return
	case 0x1d:
		setShift(4)
		return
	case 0x38:
		setShift(8)
		return
	}
	if rel {
		return
	}
	var ascii byte
	if int(c) < len(scanASCII) {
		e := scanASCII[c]
		if k.Shift&3 != 0 {
			ascii = e[1]
		} else {
			ascii = e[0]
		}
	}
	if e0 && c != 0x1c && c != 0x35 {
		ascii = 0
	}
	if len(k.Buffer) < 15 {
		k.Buffer = append(k.Buffer, uint16(c)<<8|uint16(ascii))
	}
	binary.LittleEndian.PutUint16(m.Mem[0x41a:], 0x1e)
}

// ---------- program loading ----------

// LoadEXE loads an MZ executable at the given PSP segment (the image
// starts at psp+0x10) and prepares the CPU to run it.
func (m *M) LoadEXE(exe []byte, psp uint16) error {
	if len(exe) < 28 || exe[0] != 'M' || exe[1] != 'Z' {
		return errors.New("not an MZ executable")
	}
	w := func(i int) uint16 { return binary.LittleEndian.Uint16(exe[i*2:]) }
	size := int(w(2))*512 - int(w(4))*16
	if w(1) != 0 {
		size -= 512 - int(w(1))
	}
	hdr := int(w(4)) * 16
	if hdr+size > len(exe) {
		size = len(exe) - hdr
	}
	load := psp + 0x10
	copy(m.Mem[Lin(load, 0):], exe[hdr:hdr+size])
	nrel := int(w(3))
	rt := int(w(12))
	for i := 0; i < nrel; i++ {
		off := binary.LittleEndian.Uint16(exe[rt+i*4:])
		seg := binary.LittleEndian.Uint16(exe[rt+i*4+2:])
		a := Lin(load+seg, off)
		v := binary.LittleEndian.Uint16(m.Mem[a:])
		binary.LittleEndian.PutUint16(m.Mem[a:], v+load)
	}
	m.setupPSP(psp)
	m.S[SS] = load + w(7)
	m.R[SP] = w(8)
	m.S[CS] = load + w(11)
	m.IP = w(10)
	m.S[DS] = psp
	m.S[ES] = psp
	m.Flags = 0x0202
	return nil
}

func (m *M) setupPSP(psp uint16) {
	p := Lin(psp, 0)
	for i := uint32(0); i < 256; i++ {
		m.Mem[p+i] = 0
	}
	m.Mem[p] = 0xcd
	m.Mem[p+1] = 0x20
	binary.LittleEndian.PutUint16(m.Mem[p+2:], 0x9fff)
	env := psp - 0x10
	binary.LittleEndian.PutUint16(m.Mem[p+0x2c:], env)
	e := Lin(env, 0)
	copy(m.Mem[e:], []byte("COMSPEC=C:\\COMMAND.COM\x00BLASTER=A220 I7 D1 T3\x00\x00\x01\x00C:\\GAME.EXE\x00"))
	m.Mem[p+0x80] = 0
	m.Mem[p+0x81] = 0x0d
	m.DOS.PSP = psp
}

// LoadSnapshot places a memory image at linear address base and sets the
// registers (used for the pre-unpacked program images).
func (m *M) LoadSnapshot(base uint32, image []byte, psp uint16) {
	copy(m.Mem[base:], image)
	m.setupPSP(psp)
}
