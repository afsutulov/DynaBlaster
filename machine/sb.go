package machine

// SBIRQ is the interrupt line of the emulated Sound Blaster.
const SBIRQ = 7

// SBDMA is the 8-bit DMA channel of the emulated Sound Blaster.
const SBDMA = 1

// DMA emulates the 8237 DMA controller (channels 0-3, 8 bit).
type DMA struct {
	m      *M
	flip   bool
	base   [4]uint16
	cur    [4]uint16
	bcount [4]uint16
	ccount [4]uint16
	page   [4]uint8
	mode   [4]uint8
	masked [4]bool
	tc     uint8
}

func NewDMA(m *M) *DMA {
	d := &DMA{m: m}
	for i := range d.masked {
		d.masked[i] = true
	}
	return d
}

var dmaPagePort = map[uint16]int{0x87: 0, 0x83: 1, 0x81: 2, 0x82: 3}

func (d *DMA) In(port uint16) uint8 {
	if port < 8 {
		ch := int(port >> 1)
		var v uint16
		if port&1 == 0 {
			v = d.cur[ch]
		} else {
			v = d.ccount[ch]
		}
		d.flip = !d.flip
		if d.flip {
			return uint8(v)
		}
		return uint8(v >> 8)
	}
	if port == 8 {
		t := d.tc
		d.tc = 0
		return t
	}
	return 0xff
}

func (d *DMA) Out(port uint16, v uint8) {
	if ch, ok := dmaPagePort[port]; ok {
		d.page[ch] = v
		return
	}
	if port < 8 {
		ch := int(port >> 1)
		var p *uint16
		var q *uint16
		if port&1 == 0 {
			p, q = &d.base[ch], &d.cur[ch]
		} else {
			p, q = &d.bcount[ch], &d.ccount[ch]
		}
		if !d.flip {
			*p = *p&0xff00 | uint16(v)
		} else {
			*p = *p&0x00ff | uint16(v)<<8
		}
		*q = *p
		d.flip = !d.flip
		return
	}
	switch port {
	case 0x0a:
		d.masked[v&3] = v&4 != 0
	case 0x0b:
		d.mode[v&3] = v
	case 0x0c:
		d.flip = false
	case 0x0d:
		for i := range d.masked {
			d.masked[i] = true
		}
		d.flip = false
	case 0x0f:
		for i := range d.masked {
			d.masked[i] = v&(1<<i) != 0
		}
	}
}

// Read transfers one byte from memory on channel ch. It returns the byte
// and whether the transfer count reached terminal count.
func (d *DMA) Read(ch int) (uint8, bool) {
	if d.masked[ch] {
		return 0x80, false
	}
	a := uint32(d.page[ch])<<16 | uint32(d.cur[ch])
	v := d.m.Mem[a&0xfffff]
	if d.mode[ch]&0x20 != 0 {
		d.cur[ch]--
	} else {
		d.cur[ch]++
	}
	tc := false
	if d.ccount[ch] == 0 {
		tc = true
		d.tc |= 1 << ch
		if d.mode[ch]&0x10 != 0 {
			d.cur[ch] = d.base[ch]
			d.ccount[ch] = d.bcount[ch]
		} else {
			d.masked[ch] = true
			d.ccount[ch] = 0xffff
		}
	} else {
		d.ccount[ch]--
	}
	return v, tc
}

// Write transfers one byte to memory on channel ch (device to memory).
func (d *DMA) Write(ch int, v uint8) {
	if d.masked[ch] {
		return
	}
	a := uint32(d.page[ch])<<16 | uint32(d.cur[ch])
	d.m.Mem[a&0xfffff] = v
	if d.mode[ch]&0x20 != 0 {
		d.cur[ch]--
	} else {
		d.cur[ch]++
	}
	if d.ccount[ch] == 0 {
		d.tc |= 1 << ch
		if d.mode[ch]&0x10 != 0 {
			d.cur[ch] = d.base[ch]
			d.ccount[ch] = d.bcount[ch]
		} else {
			d.masked[ch] = true
			d.ccount[ch] = 0xffff
		}
	} else {
		d.ccount[ch]--
	}
}

// SoundBlaster emulates the DSP of a Sound Blaster 2.0 (8-bit mono PCM).
type SoundBlaster struct {
	m        *M
	resetVal uint8
	out      []uint8 // DSP output queue
	cmd      uint8
	need     int
	args     []uint8
	speaker  bool
	rate     float64
	tc       uint8
	blockLen uint32 // bytes remaining in the current block
	blockSz  uint16
	auto     bool
	active   bool
	paused   bool
	irqTime  int64
	dac      uint8
	phase    float64
	testReg  uint8
	e2val    int
	e2cnt    int
}

var e2Incr = [4][9]int{
	{0x01, -0x02, -0x04, 0x08, -0x10, 0x20, 0x40, -0x80, -106},
	{-0x01, 0x02, -0x04, 0x08, 0x10, -0x20, 0x40, -0x80, 165},
	{-0x01, 0x02, 0x04, -0x08, 0x10, -0x20, -0x40, 0x80, -151},
	{0x01, -0x02, 0x04, -0x08, -0x10, 0x20, -0x40, 0x80, 90},
}

func NewSoundBlaster(m *M) *SoundBlaster {
	return &SoundBlaster{m: m, rate: 11025, dac: 0x80, irqTime: 1 << 62, e2val: 0xaa}
}

func (s *SoundBlaster) Next() int64 { return s.irqTime }

func (s *SoundBlaster) Tick(m *M, now int64) {
	if now >= s.irqTime {
		s.irqTime = 1 << 62
		m.RaiseIRQ(SBIRQ)
	}
}

func (s *SoundBlaster) In(port uint16) uint8 {
	switch port & 0xf {
	case 0xa:
		if len(s.out) > 0 {
			v := s.out[0]
			s.out = s.out[1:]
			return v
		}
		return 0xff
	case 0xc:
		return 0x7f // ready for commands
	case 0xe:
		// reading acknowledges the 8-bit IRQ
		if len(s.out) > 0 {
			return 0xff
		}
		return 0x7f
	case 0x8, 0x9:
		return s.m.OPL.Status(s.m)
	case 0x6:
		return 0xff
	}
	return 0xff
}

func (s *SoundBlaster) Out(port uint16, v uint8) {
	switch port & 0xf {
	case 0x6:
		if v&1 != 0 {
			s.resetVal = 1
		} else if s.resetVal == 1 {
			s.resetVal = 0
			s.reset()
		}
	case 0xc:
		s.write(v)
	case 0x8, 0x9:
		s.m.Snd.Sync()
		s.m.OPL.now = s.m.Now()
		s.m.OPL.Write(port&1, v)
	}
}

func (s *SoundBlaster) reset() {
	s.m.Snd.Sync()
	s.out = []uint8{0xaa}
	s.need = 0
	s.active = false
	s.auto = false
	s.speaker = false
	s.dac = 0x80
	s.irqTime = 1 << 62
}

func (s *SoundBlaster) write(v uint8) {
	if s.need > 0 {
		s.args = append(s.args, v)
		s.need--
		if s.need == 0 {
			s.exec()
		}
		return
	}
	s.cmd = v
	s.args = s.args[:0]
	switch v {
	case 0x10, 0x40, 0xe0, 0xe2, 0xe4, 0x38:
		s.need = 1
	case 0x14, 0x16, 0x17, 0x74, 0x75, 0x76, 0x77, 0x48, 0x91, 0x80:
		s.need = 2
	default:
		s.need = 0
		s.exec()
	}
}

// SBDebug, if set, receives every executed DSP command.
var SBDebug func(cmd uint8, args []uint8)

func (s *SoundBlaster) exec() {
	m := s.m
	if SBDebug != nil {
		SBDebug(s.cmd, s.args)
	}
	switch s.cmd {
	case 0x10: // direct DAC
		m.Snd.Sync()
		s.dac = s.args[0]
	case 0x14, 0x91, 0x16, 0x17, 0x74, 0x75, 0x76, 0x77: // single-cycle DMA
		m.Snd.Sync()
		s.blockLen = uint32(s.args[0]) | uint32(s.args[1])<<8
		s.blockLen++
		s.active = true
		s.paused = false
		s.auto = false
		s.phase = 0
		s.schedule()
	case 0x1c, 0x90: // auto-init DMA
		m.Snd.Sync()
		s.blockLen = uint32(s.blockSz) + 1
		s.active = true
		s.paused = false
		s.auto = true
		s.schedule()
	case 0x48:
		s.blockSz = uint16(s.args[0]) | uint16(s.args[1])<<8
	case 0x40:
		m.Snd.Sync()
		s.tc = s.args[0]
		s.rate = 1000000.0 / float64(256-int(s.tc))
		if s.active {
			s.schedule()
		}
	case 0x80: // silence
		s.irqTime = m.Now() + int64(float64(uint32(s.args[0])|uint32(s.args[1])<<8+1)*1e9/s.rate)
	case 0xd0:
		m.Snd.Sync()
		s.paused = true
		s.irqTime = 1 << 62
	case 0xd4:
		m.Snd.Sync()
		if s.active {
			s.paused = false
			s.schedule()
		}
	case 0xd1:
		m.Snd.Sync()
		s.speaker = true
	case 0xd3:
		m.Snd.Sync()
		s.speaker = false
	case 0xd8:
		if s.speaker {
			s.out = append(s.out, 0xff)
		} else {
			s.out = append(s.out, 0)
		}
	case 0xda:
		s.auto = false
	case 0xe0:
		s.out = append(s.out, ^s.args[0])
	case 0xe1:
		s.out = append(s.out, 2, 1)
	case 0xe4:
		s.testReg = s.args[0]
	case 0xe2: // DMA identification: write a checksum byte through DMA
		for i := 0; i < 8; i++ {
			if (s.args[0]>>i)&1 != 0 {
				s.e2val += e2Incr[s.e2cnt%4][i]
			}
		}
		s.e2val += e2Incr[s.e2cnt%4][8]
		s.e2cnt++
		m.DMA.Write(SBDMA, uint8(s.e2val))
	case 0xe8:
		s.out = append(s.out, s.testReg)
	case 0xf2:
		s.irqTime = m.Now() + 10000
		m.Clk.Reschedule()
	case 0x20:
		s.out = append(s.out, 0x80)
	}
}

// schedule computes when the current DMA block ends.
func (s *SoundBlaster) schedule() {
	if !s.active || s.paused || s.auto {
		s.irqTime = 1 << 62
		return
	}
	dur := float64(s.blockLen) * 1e9 / s.rate
	s.irqTime = s.m.Now() + int64(dur)
	s.m.Clk.Reschedule()
}

// nextSample advances the DSP by one output sample at the given output
// rate and returns the current DAC value (unsigned 8 bit).
func (s *SoundBlaster) nextSample(outRate float64) uint8 {
	if s.active && !s.paused {
		s.phase += s.rate / outRate
		for s.phase >= 1 {
			s.phase--
			if s.blockLen == 0 {
				break
			}
			v, _ := s.m.DMA.Read(SBDMA)
			s.dac = v
			s.blockLen--
			if s.blockLen == 0 {
				if s.auto {
					s.blockLen = uint32(s.blockSz) + 1
					s.irqTime = s.m.Now()
					s.m.Clk.Reschedule()
				} else {
					s.active = false
				}
			}
		}
	}
	if !s.speaker {
		return 0x80
	}
	return s.dac
}
