package machine

import (
	"encoding/binary"
	"strings"
	"time"
)

// FileSystem supplies the files the DOS program opens.
type FileSystem interface {
	// ReadFile returns the contents of a file (name is upper case, DOS style).
	ReadFile(name string) ([]byte, bool)
	// WriteFile stores a file created by the program.
	WriteFile(name string, data []byte)
}

type dosFile struct {
	name  string
	data  []byte
	pos   int
	write bool
}

// DOS implements the subset of MS-DOS services used by the game.
type DOS struct {
	m      *M
	FS     FileSystem
	files  map[uint16]*dosFile
	PSP    uint16
	Now    func() time.Time // wall clock for INT 21h/2Ch and INT 1Ah
	memTop uint16
	Print  func(s string) // console output (optional)
}

func NewDOS(m *M) *DOS {
	return &DOS{m: m, files: map[uint16]*dosFile{}, Now: time.Now, memTop: 0x9fff}
}

func (d *DOS) readString(seg, off uint16) string {
	var b strings.Builder
	for i := 0; i < 128; i++ {
		c := d.m.RB(seg, off+uint16(i))
		if c == 0 {
			break
		}
		b.WriteByte(c)
	}
	return b.String()
}

func normName(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "\\")
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(s, ".\\")
	s = strings.TrimPrefix(s, "\\")
	return s
}

func (d *DOS) setErr(code uint16) {
	d.m.R[AX] = code
	d.m.Flags |= FCF
}
func (d *DOS) ok() { d.m.Flags &^= FCF }

func (d *DOS) newHandle() uint16 {
	for h := uint16(5); ; h++ {
		if _, used := d.files[h]; !used {
			return h
		}
	}
}

// Int21 services a DOS call. The caller has already performed the IRET
// so flag changes are visible to the program.
func (d *DOS) Int21() {
	m := d.m
	ah := m.AH()
	switch ah {
	case 0x02: // write char
		if d.Print != nil {
			d.Print(string([]byte{m.DL()}))
		}
	case 0x06: // direct console I/O
		if m.DL() == 0xff {
			if k, ok := m.biosKey(true); ok {
				m.SetAL(uint8(k))
				m.Flags &^= FZF
			} else {
				m.SetAL(0)
				m.Flags |= FZF
			}
		}
	case 0x09: // print string
		if d.Print != nil {
			var b []byte
			for i := uint16(0); i < 4096; i++ {
				c := m.RB(m.S[DS], m.R[DX]+i)
				if c == '$' {
					break
				}
				b = append(b, c)
			}
			d.Print(string(b))
		}
	case 0x0b:
		if _, ok := m.biosKey(false); ok {
			m.SetAL(0xff)
		} else {
			m.SetAL(0)
		}
	case 0x0c: // flush buffer, then invoke function AL
		m.Kbd.Buffer = nil
		al := m.AL()
		if al == 1 || al == 6 || al == 7 || al == 8 || al == 0x0a {
			m.SetAH(al)
			d.Int21()
		}
	case 0x19:
		m.SetAL(2)
	case 0x1a: // set DTA
	case 0x25:
		n := uint32(m.AL())
		binary.LittleEndian.PutUint16(m.Mem[n*4:], m.R[DX])
		binary.LittleEndian.PutUint16(m.Mem[n*4+2:], m.S[DS])
	case 0x2a:
		t := d.Now()
		m.R[CX] = uint16(t.Year())
		m.SetDH(uint8(t.Month()))
		m.SetDL(uint8(t.Day()))
		m.SetAL(uint8(t.Weekday()))
	case 0x2c:
		t := d.Now()
		m.SetCH(uint8(t.Hour()))
		m.SetCL(uint8(t.Minute()))
		m.SetDH(uint8(t.Second()))
		m.SetDL(uint8(t.Nanosecond() / 10000000))
	case 0x30:
		m.R[AX] = 0x0005 // DOS 5.0
		m.R[BX] = 0
		m.R[CX] = 0
	case 0x33:
		m.SetDL(0)
	case 0x35:
		n := uint32(m.AL())
		m.R[BX] = binary.LittleEndian.Uint16(m.Mem[n*4:])
		m.S[ES] = binary.LittleEndian.Uint16(m.Mem[n*4+2:])
	case 0x3c: // create
		name := normName(d.readString(m.S[DS], m.R[DX]))
		h := d.newHandle()
		d.files[h] = &dosFile{name: name, write: true}
		m.R[AX] = h
		d.ok()
	case 0x3d: // open
		name := normName(d.readString(m.S[DS], m.R[DX]))
		data, ok := d.FS.ReadFile(name)
		if !ok {
			d.setErr(2)
			return
		}
		h := d.newHandle()
		buf := make([]byte, len(data))
		copy(buf, data)
		d.files[h] = &dosFile{name: name, data: buf, write: m.AL()&3 != 0}
		m.R[AX] = h
		d.ok()
	case 0x3e: // close
		f, ok := d.files[m.R[BX]]
		if !ok {
			if m.R[BX] < 5 {
				d.ok()
				return
			}
			d.setErr(6)
			return
		}
		if f.write {
			d.FS.WriteFile(f.name, f.data)
		}
		delete(d.files, m.R[BX])
		d.ok()
	case 0x3f: // read
		f, ok := d.files[m.R[BX]]
		if !ok {
			if m.R[BX] == 0 {
				m.R[AX] = 0
				d.ok()
				return
			}
			d.setErr(6)
			return
		}
		n := int(m.R[CX])
		if f.pos+n > len(f.data) {
			n = len(f.data) - f.pos
		}
		if n < 0 {
			n = 0
		}
		for i := 0; i < n; i++ {
			m.WB(m.S[DS], m.R[DX]+uint16(i), f.data[f.pos+i])
		}
		f.pos += n
		m.R[AX] = uint16(n)
		d.ok()
	case 0x40: // write
		n := int(m.R[CX])
		f, ok := d.files[m.R[BX]]
		if !ok {
			if m.R[BX] < 5 {
				m.R[AX] = uint16(n)
				d.ok()
				return
			}
			d.setErr(6)
			return
		}
		for i := 0; i < n; i++ {
			b := m.RB(m.S[DS], m.R[DX]+uint16(i))
			if f.pos < len(f.data) {
				f.data[f.pos] = b
			} else {
				f.data = append(f.data, b)
			}
			f.pos++
		}
		m.R[AX] = uint16(n)
		d.ok()
	case 0x42: // seek
		f, ok := d.files[m.R[BX]]
		if !ok {
			d.setErr(6)
			return
		}
		off := int(int32(uint32(m.R[CX])<<16 | uint32(m.R[DX])))
		switch m.AL() {
		case 0:
			f.pos = off
		case 1:
			f.pos += off
		case 2:
			f.pos = len(f.data) + off
		}
		if f.pos < 0 {
			f.pos = 0
		}
		m.R[AX] = uint16(f.pos)
		m.R[DX] = uint16(f.pos >> 16)
		d.ok()
	case 0x44: // IOCTL
		m.R[DX] = 0x80
		d.ok()
	case 0x48: // allocate memory
		d.setErr(8)
		m.R[BX] = 0
	case 0x49, 0x4a:
		d.ok()
	case 0x4c, 0x00:
		m.Halted = true
		m.ExitCode = int(m.AL())
		if m.ExitHook != nil {
			m.ExitHook(m)
		}
	case 0x62:
		m.R[BX] = d.PSP
	default:
		// unsupported: report failure
		m.Flags |= FCF
	}
}

// biosKey fetches a key from the BIOS buffer.
func (m *M) biosKey(remove bool) (uint16, bool) {
	if len(m.Kbd.Buffer) == 0 {
		return 0, false
	}
	k := m.Kbd.Buffer[0]
	if remove {
		m.Kbd.Buffer = m.Kbd.Buffer[1:]
	}
	return k, true
}
