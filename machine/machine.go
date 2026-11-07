package machine

import (
	"encoding/binary"
	"fmt"
)

// MemSize is the size of the emulated address space (1 MB + HMA).
const MemSize = 0x110000

// Block is a piece of translated code. It executes one basic block of the
// original program, updating the CPU state (including CS:IP).
type Block func(m *M)

// M is the complete machine.
type M struct {
	CPU
	Mem [MemSize]byte

	VGA  *VGA
	PIT  *PIT
	PIC  *PIC
	Kbd  *Keyboard
	DOS  *DOS
	SB   *SoundBlaster
	OPL  *OPL
	DMA  *DMA
	Snd  *Mixer
	Clk  *Clock
	Hook Hooks

	// Translated code: per code segment, blocks indexed by IP.
	segTabs map[uint16][]Block
	lastCS  uint16
	lastTab []Block

	// Native (BIOS/DOS) trap handlers, executed when the CPU reaches
	// the trap address in the BIOS segment.
	traps map[uint32]func(m *M)

	// InterpOnly runs everything through the reference interpreter while
	// keeping the interrupt timing of the translated code.
	InterpOnly bool

	Halted   bool
	ExitCode int
	ExitHook func(m *M) // called when the program terminates (INT 21h/4Ch)

	irqPending uint16
	// Interpreter-only statistics.
	InterpSteps uint64
	Trace       func(m *M)
}

// Hooks lets the frontend observe the machine.
type Hooks struct {
	Frame func(m *M) // called at the start of every vertical retrace
}

// NewMachine creates a machine with all devices attached.
func NewMachine() *M {
	m := &M{}
	m.lastCS = 0xffff
	m.traps = map[uint32]func(*M){}
	m.Clk = NewClock()
	m.PIC = NewPIC()
	m.PIT = NewPIT(m)
	m.VGA = NewVGA(m)
	m.Kbd = NewKeyboard(m)
	m.DMA = NewDMA(m)
	m.OPL = NewOPL(MixRate)
	m.SB = NewSoundBlaster(m)
	m.Snd = NewMixer(m)
	m.DOS = NewDOS(m)
	m.setupBIOS()
	return m
}

// Linear address helpers.
func Lin(seg, off uint16) uint32 { return uint32(seg)<<4 + uint32(off) }

// ---- memory access ----

func (m *M) RB(seg, off uint16) uint8 {
	a := uint32(seg)<<4 + uint32(off)
	if a-0xA0000 < 0x10000 {
		return m.VGA.Read(a - 0xA0000)
	}
	return m.Mem[a]
}

func (m *M) WB(seg, off uint16, v uint8) {
	a := uint32(seg)<<4 + uint32(off)
	if a-0xA0000 < 0x10000 {
		m.VGA.Write(a-0xA0000, v)
		return
	}
	if a >= 0xC0000 && a < 0x100000 {
		return // ROM
	}
	m.Mem[a] = v
}

func (m *M) RW(seg, off uint16) uint16 {
	if off != 0xffff {
		a := uint32(seg)<<4 + uint32(off)
		if a-0xA0000 >= 0x10000 {
			return binary.LittleEndian.Uint16(m.Mem[a:])
		}
	}
	return uint16(m.RB(seg, off)) | uint16(m.RB(seg, off+1))<<8
}

func (m *M) WW(seg, off uint16, v uint16) {
	if off != 0xffff {
		a := uint32(seg)<<4 + uint32(off)
		if a-0xA0000 >= 0x10000 && a < 0xBFFFF {
			binary.LittleEndian.PutUint16(m.Mem[a:], v)
			return
		}
	}
	m.WB(seg, off, uint8(v))
	m.WB(seg, off+1, uint8(v>>8))
}

// ---- stack ----

func (m *M) Push(v uint16) {
	m.R[SP] -= 2
	m.WW(m.S[SS], m.R[SP], v)
}

func (m *M) Pop() uint16 {
	v := m.RW(m.S[SS], m.R[SP])
	m.R[SP] += 2
	return v
}

// Flags as stored by PUSHF (reserved bits set like on a 286 in real mode).
func (m *M) PushF() { m.Push(m.Flags&0x0fd5 | 0xf002) }
func (m *M) PopF()  { m.Flags = m.Pop()&0x0fd5 | 0x0002 }

// ---- control transfer helpers used by translated code ----

// Jmp sets the next instruction pointer.
func (m *M) Jmp(ip uint16) { m.IP = ip }

// Call pushes the return address and jumps (near).
func (m *M) Call(target, ret uint16) {
	m.Push(ret)
	m.IP = target
}

// CallFar pushes CS:ret and jumps to seg:off.
func (m *M) CallFar(seg, off, ret uint16) {
	m.Push(m.S[CS])
	m.Push(ret)
	m.S[CS] = seg
	m.IP = off
}

// Ret returns from a near procedure, releasing n extra bytes.
func (m *M) Ret(n uint16) {
	m.IP = m.Pop()
	m.R[SP] += n
}

// RetFar returns from a far procedure.
func (m *M) RetFar(n uint16) {
	m.IP = m.Pop()
	m.S[CS] = m.Pop()
	m.R[SP] += n
}

// Iret returns from an interrupt.
func (m *M) Iret() {
	m.IP = m.Pop()
	m.S[CS] = m.Pop()
	m.PopF()
}

// Int performs a software interrupt: pushes flags, CS and the return IP
// and transfers control through the interrupt vector table.
func (m *M) Int(n uint8, ret uint16) {
	m.PushF()
	m.Push(m.S[CS])
	m.Push(ret)
	m.Flags &^= FIF | FTF
	m.IP = binary.LittleEndian.Uint16(m.Mem[uint32(n)*4:])
	m.S[CS] = binary.LittleEndian.Uint16(m.Mem[uint32(n)*4+2:])
}

// DivError raises INT 0 (the 8086 returns to the faulting instruction on
// a 286+, but the game never divides by zero; we return after it).
func (m *M) DivError(ret uint16) { m.Int(0, ret) }

// OPLWrites counts FM register accesses (diagnostics).
var OPLWrites int

// ---- port I/O ----

// I/O bus timing: an access to an ISA card takes about a microsecond,
// which the original delay loops (e.g. the AdLib detection) rely on.
const (
	ioCostISA = 1000 // ns
	ioCostVGA = 300  // ns (local bus video)
)

func (m *M) ioCost(port uint16) {
	if port >= 0x3c0 && port <= 0x3df {
		m.Clk.skip += ioCostVGA
	} else {
		m.Clk.skip += ioCostISA
	}
}

func (m *M) In8(port uint16) uint8 {
	m.ioCost(port)
	switch {
	case port >= 0x3c0 && port <= 0x3df:
		return m.VGA.In(port)
	case port == 0x20 || port == 0x21 || port == 0xa0 || port == 0xa1:
		return m.PIC.In(port)
	case port >= 0x40 && port <= 0x43:
		return m.PIT.In(port)
	case port == 0x60 || port == 0x61 || port == 0x64:
		return m.Kbd.In(port)
	case port == 0x388 || port == 0x389 || port == 0x228 || port == 0x229:
		return m.OPL.Status(m)
	case port >= 0x220 && port <= 0x22f:
		return m.SB.In(port)
	case port < 0x20 || (port >= 0x80 && port <= 0x8f):
		return m.DMA.In(port)
	case port == 0x201:
		return 0xff // no joystick
	case port >= 0x278 && port <= 0x27f, port >= 0x378 && port <= 0x37f, port >= 0x3bc && port <= 0x3bf:
		return 0xff // no parallel port adapter
	}
	return 0xff
}

func (m *M) Out8(port uint16, v uint8) {
	m.ioCost(port)
	switch {
	case port >= 0x3c0 && port <= 0x3df:
		m.VGA.Out(port, v)
	case port == 0x20 || port == 0x21 || port == 0xa0 || port == 0xa1:
		m.PIC.Out(port, v)
	case port >= 0x40 && port <= 0x43:
		m.PIT.Out(port, v)
	case port == 0x60 || port == 0x61 || port == 0x64:
		m.Kbd.Out(port, v)
	case port == 0x388 || port == 0x389 || port == 0x228 || port == 0x229:
		m.Snd.Sync()
		m.OPL.now = m.Now()
		m.OPL.Write(port&1, v)
		OPLWrites++
	case port >= 0x220 && port <= 0x22f:
		m.SB.Out(port, v)
	case port < 0x20 || (port >= 0x80 && port <= 0x8f):
		m.DMA.Out(port, v)
	}
}

func (m *M) In16(port uint16) uint16 {
	return uint16(m.In8(port)) | uint16(m.In8(port+1))<<8
}

func (m *M) Out16(port uint16, v uint16) {
	m.Out8(port, uint8(v))
	m.Out8(port+1, uint8(v>>8))
}

// ---- code registry and dispatch ----

// Register installs translated blocks, keyed by CS<<16 | IP.
func (m *M) Register(blocks map[uint32]Block) {
	if m.segTabs == nil {
		m.segTabs = map[uint16][]Block{}
	}
	for k, b := range blocks {
		cs := uint16(k >> 16)
		t := m.segTabs[cs]
		if t == nil {
			t = make([]Block, 0x10000)
			m.segTabs[cs] = t
		}
		t[uint16(k)] = b
	}
	m.lastCS = 0xffff
	m.lastTab = nil
}

// ClearBlocks removes all translated code (used for pure interpretation).
func (m *M) ClearBlocks() {
	m.segTabs = nil
	m.lastCS = 0xffff
	m.lastTab = nil
}

// Step executes one translated block or, failing that, one interpreted
// instruction, then services pending hardware interrupts.
func (m *M) Step() {
	cs := m.S[CS]
	if cs != m.lastCS {
		m.lastCS = cs
		m.lastTab = m.segTabs[cs]
	}
	if m.lastTab != nil && !m.InterpOnly {
		if b := m.lastTab[m.IP]; b != nil {
			b(m)
			m.checkIRQ()
			return
		}
	}
	a := uint32(cs)<<4 + uint32(m.IP)
	if h, ok := m.traps[a]; ok {
		h(m)
		m.checkIRQ()
		return
	}
	in := m.Interp()
	if m.InterpOnly && !in.EndsBlock() {
		// Reference mode: service interrupts at the same points as the
		// translated code (block boundaries) so both runs are identical.
		if m.S[CS] != m.lastCS {
			m.lastCS = m.S[CS]
			m.lastTab = m.segTabs[m.S[CS]]
		}
		if m.lastTab == nil || m.lastTab[m.IP] == nil {
			return
		}
	}
	m.checkIRQ()
}

// RaiseIRQ marks a hardware interrupt line as pending.
func (m *M) RaiseIRQ(n int) { m.PIC.Raise(n) }

func (m *M) checkIRQ() {
	m.Clk.Advance(m)
	if m.Flags&FIF == 0 {
		return
	}
	if v, ok := m.PIC.Ack(); ok {
		m.PushF()
		m.Push(m.S[CS])
		m.Push(m.IP)
		m.Flags &^= FIF | FTF
		m.IP = binary.LittleEndian.Uint16(m.Mem[uint32(v)*4:])
		m.S[CS] = binary.LittleEndian.Uint16(m.Mem[uint32(v)*4+2:])
	}
}

// Fault reports an unrecoverable emulation error.
type Fault struct {
	CS, IP uint16
	Msg    string
}

func (f *Fault) Error() string { return fmt.Sprintf("%04X:%04X: %s", f.CS, f.IP, f.Msg) }

func (m *M) fault(msg string, args ...interface{}) {
	panic(&Fault{m.S[CS], m.IP, fmt.Sprintf(msg, args...)})
}
