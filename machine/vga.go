package machine

// VGA emulates the IBM VGA adapter at register level: planar video
// memory with latches and all four write modes, the sequencer, graphics
// controller, attribute controller, DAC and the CRT controller (start
// address, offset, line compare, pixel panning, scan doubling and the
// retrace timing derived from the programmed CRTC values).
type VGA struct {
	m      *M
	Planes [4][0x10000]byte
	latch  [4]byte

	Misc     uint8
	SeqIdx   uint8
	Seq      [8]uint8
	GCIdx    uint8
	GC       [16]uint8
	CRTCIdx  uint8
	CRTC     [32]uint8
	AttrIdx  uint8
	Attr     [32]uint8
	attrFlip bool

	DAC      [256][3]uint8
	dacW     uint8
	dacR     uint8
	dacComp  int
	dacRComp int
	PelMask  uint8

	// timing
	frameStart  int64 // virtual time of the start of the current frame (top of display)
	framePeriod int64
	linePeriod  int64
	nextRetrace int64
	pollCount   int
	lastPoll    uint8
	Frames      uint64

	// Latched display state captured at the start of vertical retrace.
	snapStart uint16
	snapPan   uint8
}

func NewVGA(m *M) *VGA {
	v := &VGA{m: m, PelMask: 0xff}
	v.SetBIOSMode(0x03) // power-on state: text mode
	v.recalc()
	v.nextRetrace = v.framePeriod
	return v
}

// ---------- memory ----------

func (v *VGA) Read(a uint32) uint8 {
	off := uint16(a)
	if v.Seq[4]&8 != 0 { // chain 4
		p := off & 3
		off &^= 3
		for i := 0; i < 4; i++ {
			v.latch[i] = v.Planes[i][off]
		}
		return v.Planes[p][off]
	}
	for i := 0; i < 4; i++ {
		v.latch[i] = v.Planes[i][off]
	}
	if v.GC[5]&8 == 0 {
		return v.latch[v.GC[4]&3]
	}
	// read mode 1: color compare
	var r uint8
	cc, dc := v.GC[2], v.GC[7]
	for b := 0; b < 8; b++ {
		match := true
		for p := 0; p < 4; p++ {
			if dc&(1<<p) == 0 {
				continue
			}
			bit := (v.latch[p] >> b) & 1
			if bit != (cc>>p)&1 {
				match = false
				break
			}
		}
		if match {
			r |= 1 << b
		}
	}
	return r
}

func (v *VGA) Write(a uint32, d uint8) {
	off := uint16(a)
	mask := v.Seq[2] & 15
	if v.Seq[4]&8 != 0 {
		p := off & 3
		off &^= 3
		if mask&(1<<p) != 0 {
			v.Planes[p][off] = d
		}
		return
	}
	mode := v.GC[5] & 3
	if mode == 1 {
		for p := 0; p < 4; p++ {
			if mask&(1<<p) != 0 {
				v.Planes[p][off] = v.latch[p]
			}
		}
		return
	}
	rot := v.GC[3] & 7
	fn := (v.GC[3] >> 3) & 3
	bm := v.GC[8]
	sr, esr := v.GC[0], v.GC[1]
	if rot != 0 {
		d = d>>rot | d<<(8-rot)
	}
	for p := 0; p < 4; p++ {
		if mask&(1<<p) == 0 {
			continue
		}
		var val uint8
		m := bm
		switch mode {
		case 0:
			if esr&(1<<p) != 0 {
				if sr&(1<<p) != 0 {
					val = 0xff
				}
			} else {
				val = d
			}
		case 2:
			if d&(1<<p) != 0 {
				val = 0xff
			}
		case 3:
			m = d & bm
			if sr&(1<<p) != 0 {
				val = 0xff
			}
		}
		l := v.latch[p]
		switch fn {
		case 1:
			val &= l
		case 2:
			val |= l
		case 3:
			val ^= l
		}
		v.Planes[p][off] = val&m | l&^m
	}
}

// ---------- ports ----------

func (v *VGA) In(port uint16) uint8 {
	switch port {
	case 0x3da, 0x3ba:
		v.attrFlip = false
		return v.status()
	case 0x3c0:
		return v.AttrIdx
	case 0x3c1:
		return v.Attr[v.AttrIdx&31]
	case 0x3c4:
		return v.SeqIdx
	case 0x3c5:
		return v.Seq[v.SeqIdx&7]
	case 0x3c6:
		return v.PelMask
	case 0x3c7:
		return 3
	case 0x3c8:
		return v.dacW
	case 0x3c9:
		r := v.DAC[v.dacR][v.dacRComp]
		v.dacRComp++
		if v.dacRComp == 3 {
			v.dacRComp = 0
			v.dacR++
		}
		return r
	case 0x3cc:
		return v.Misc
	case 0x3ce:
		return v.GCIdx
	case 0x3cf:
		return v.GC[v.GCIdx&15]
	case 0x3d4, 0x3b4:
		return v.CRTCIdx
	case 0x3d5, 0x3b5:
		return v.CRTC[v.CRTCIdx&31]
	}
	return 0xff
}

func (v *VGA) Out(port uint16, d uint8) {
	v.pollCount = 0
	switch port {
	case 0x3c0:
		if !v.attrFlip {
			v.AttrIdx = d
		} else {
			v.Attr[v.AttrIdx&31] = d
		}
		v.attrFlip = !v.attrFlip
	case 0x3c2:
		v.Misc = d
		v.recalc()
	case 0x3c4:
		v.SeqIdx = d
	case 0x3c5:
		v.Seq[v.SeqIdx&7] = d
		if v.SeqIdx == 1 {
			v.recalc()
		}
	case 0x3c6:
		v.PelMask = d
	case 0x3c7:
		v.dacR = d
		v.dacRComp = 0
	case 0x3c8:
		v.dacW = d
		v.dacComp = 0
	case 0x3c9:
		v.DAC[v.dacW][v.dacComp] = d & 63
		v.dacComp++
		if v.dacComp == 3 {
			v.dacComp = 0
			v.dacW++
		}
	case 0x3ce:
		v.GCIdx = d
	case 0x3cf:
		v.GC[v.GCIdx&15] = d
	case 0x3d4, 0x3b4:
		v.CRTCIdx = d
	case 0x3d5, 0x3b5:
		i := v.CRTCIdx & 31
		if i < 8 && v.CRTC[0x11]&0x80 != 0 && i != 7 {
			return // write protected
		}
		if i == 7 && v.CRTC[0x11]&0x80 != 0 {
			d = d&0x10 | v.CRTC[7]&^0x10
		}
		v.CRTC[i] = d
		if i <= 0x12 || i == 0x15 || i == 0x16 {
			v.recalc()
		}
	}
}

// ---------- timing ----------

func (v *VGA) vtotal() int {
	c := v.CRTC
	return int(c[6]) | int(c[7]&1)<<8 | int(c[7]&0x20)<<4
}
func (v *VGA) vde() int {
	c := v.CRTC
	return int(c[0x12]) | int(c[7]&2)<<7 | int(c[7]&0x40)<<3
}
func (v *VGA) vrs() int {
	c := v.CRTC
	return int(c[0x10]) | int(c[7]&4)<<6 | int(c[7]&0x80)<<2
}
func (v *VGA) lineCompare() int {
	c := v.CRTC
	return int(c[0x18]) | int(c[7]&0x10)<<4 | int(c[9]&0x40)<<3
}

func (v *VGA) recalc() {
	clk := 25175000.0
	if (v.Misc>>2)&3 == 1 {
		clk = 28322000.0
	}
	dots := 9.0
	if v.Seq[1]&1 != 0 {
		dots = 8
	}
	if v.Seq[1]&8 != 0 {
		clk /= 2
	}
	htotal := float64(int(v.CRTC[0])+5) * dots
	if htotal < 100 {
		htotal = 800
	}
	lp := htotal / clk * 1e9
	vt := v.vtotal() + 2
	if vt < 100 {
		vt = 449
	}
	v.linePeriod = int64(lp)
	v.framePeriod = int64(lp * float64(vt))
}

// RefreshHz returns the current vertical refresh rate.
func (v *VGA) RefreshHz() float64 { return 1e9 / float64(v.framePeriod) }

func (v *VGA) Next() int64 { return v.nextRetrace }

// retraceLine returns the scan line where vertical retrace starts and ends.
func (v *VGA) retraceLines() (int, int) {
	s := v.vrs()
	e := int(v.CRTC[0x11] & 15)
	// end is compared against the low 4 bits of the line counter
	ln := s&^15 | e
	if ln <= s {
		ln += 16
	}
	return s, ln
}

func (v *VGA) Tick(m *M, now int64) {
	for now >= v.nextRetrace {
		// start of vertical retrace: the display is latched here
		s, _ := v.retraceLines()
		v.frameStart = v.nextRetrace - int64(s)*v.linePeriod
		v.Frames++
		if m.Hook.Frame != nil {
			m.Hook.Frame(m)
		}
		v.nextRetrace += v.framePeriod
	}
}

// status implements Input Status Register 1 (3DAh).
func (v *VGA) status() uint8 {
	m := v.m
	now := m.Now()
	s, e := v.retraceLines()
	fs := v.frameStart
	for now-fs >= v.framePeriod {
		fs += v.framePeriod
	}
	for now < fs {
		fs -= v.framePeriod
	}
	t := now - fs
	line := int(t / v.linePeriod)
	var r uint8
	vde := v.vde()
	inLine := t % v.linePeriod
	hde := v.linePeriod * int64(v.CRTC[1]+1) / int64(v.CRTC[0]+5)
	if line > vde || inLine > hde {
		r |= 1
	}
	if line >= s && line < e {
		r |= 8
	}
	// Polling detection: a program that keeps reading the same value is
	// waiting for a retrace edge; skip ahead in time.
	if r == v.lastPoll {
		v.pollCount++
		if v.pollCount > 16 {
			v.pollCount = 0
			var t3, t0 int64
			if r&8 != 0 {
				t3 = fs + int64(e)*v.linePeriod
			} else {
				t3 = fs + int64(s)*v.linePeriod
				if t3 <= now {
					t3 += v.framePeriod
				}
			}
			ls := fs + int64(line)*v.linePeriod
			switch {
			case line > vde:
				t0 = fs + v.framePeriod
			case inLine > hde:
				t0 = ls + v.linePeriod
			default:
				t0 = ls + hde + 1
			}
			target := t3
			if t0 < target {
				target = t0
			}
			if target > now {
				m.Clk.SkipTo(m, target)
			}
		}
	} else {
		v.pollCount = 0
		v.lastPoll = r
	}
	return r
}

// ---------- rendering ----------

// Render draws the visible picture into dst (RGBA, w*h*4) and returns its
// dimensions. Pixel aspect is not corrected here.
func (v *VGA) Render(dst []byte) (w, h int, out []byte) {
	c := &v.CRTC
	is256 := v.Attr[0x10]&0x40 != 0
	chars := int(c[1]) + 1
	ppc := 8
	if is256 {
		ppc = 4
	} else if v.Seq[1]&8 != 0 {
		ppc = 8
	}
	w = chars * ppc
	if v.Seq[1]&8 != 0 && !is256 {
		w = chars * 8 / 2 * 2
	}
	h = v.vde() + 1
	if w <= 0 || h <= 0 || w > 1024 || h > 1024 {
		return 0, 0, dst
	}
	need := w * h * 4
	if cap(dst) < need {
		dst = make([]byte, need)
	}
	dst = dst[:need]
	if v.AttrIdx&0x20 == 0 || v.Seq[1]&0x20 != 0 {
		for i := range dst {
			dst[i] = 0
			if i&3 == 3 {
				dst[i] = 255
			}
		}
		return w, h, dst
	}
	// palette to RGBA
	var pal [256][4]byte
	for i := 0; i < 256; i++ {
		e := v.DAC[uint8(i)&v.PelMask]
		pal[i] = [4]byte{e[0]<<2 | e[0]>>4, e[1]<<2 | e[1]>>4, e[2]<<2 | e[2]>>4, 255}
	}
	start := uint16(c[0x0c])<<8 | uint16(c[0x0d])
	start += uint16((c[8] >> 5) & 3)
	offset := uint16(c[0x13]) * 2
	msl := int(c[9]&31) + 1
	dbl := 1
	if c[9]&0x80 != 0 {
		dbl = 2
	}
	pan := int(v.Attr[0x13] & 15)
	if is256 {
		pan = (pan >> 1) & 3
	} else if pan > 7 {
		pan = 0
	}
	lc := v.lineCompare()
	var addrMode int // 0 byte, 1 word, 2 dword
	if c[0x14]&0x40 != 0 {
		addrMode = 2
	} else if c[0x17]&0x40 == 0 {
		addrMode = 1
	}
	ma := start
	rowScan := 0
	curPan := pan
	for y := 0; y < h; y++ {
		if y == lc {
			ma = 0
			rowScan = 0
			if v.Attr[0x10]&0x20 != 0 {
				curPan = 0
			}
		}
		line := dst[y*w*4 : (y+1)*w*4]
		v.renderLine(line, w, ma, curPan, is256, addrMode, &pal)
		rowScan++
		if rowScan >= msl*dbl {
			rowScan = 0
			ma += offset
		}
	}
	return w, h, dst
}

func (v *VGA) memAddr(ma uint16, mode int) uint16 {
	switch mode {
	case 2:
		return ma<<2 | (ma>>14)&3
	case 1:
		if v.CRTC[0x17]&0x20 != 0 {
			return ma<<1 | (ma>>15)&1
		}
		return ma<<1 | (ma>>13)&1
	}
	return ma
}

func (v *VGA) renderLine(line []byte, w int, ma uint16, pan int, is256 bool, mode int, pal *[256][4]byte) {
	x := -pan
	cma := ma
	if is256 {
		for x < w {
			a := v.memAddr(cma, mode)
			for p := 0; p < 4; p++ {
				if x >= 0 && x < w {
					copy(line[x*4:x*4+4], pal[v.Planes[p][a]][:])
				}
				x++
			}
			cma++
		}
		return
	}
	for x < w {
		a := v.memAddr(cma, mode)
		b0, b1, b2, b3 := v.Planes[0][a], v.Planes[1][a], v.Planes[2][a], v.Planes[3][a]
		for bit := 7; bit >= 0; bit-- {
			if x >= 0 && x < w {
				ci := (b0>>bit)&1 | ((b1>>bit)&1)<<1 | ((b2>>bit)&1)<<2 | ((b3>>bit)&1)<<3
				ci &= v.Attr[0x12] & 15
				pv := v.Attr[ci]
				var idx uint8
				if v.Attr[0x10]&0x80 != 0 {
					idx = pv&15 | (v.Attr[0x14]&3)<<4
				} else {
					idx = pv & 63
				}
				idx |= (v.Attr[0x14] & 0xc) << 4
				copy(line[x*4:x*4+4], pal[idx][:])
			}
			x++
		}
		cma++
	}
}

// SetBIOSMode programs the registers like the VGA BIOS for modes 03h, 12h
// and 13h.
func (v *VGA) SetBIOSMode(mode uint8) {
	var seq, crtc, attr, gc []uint8
	var misc uint8
	switch mode & 0x7f {
	case 0x13:
		misc = 0x63
		seq = []uint8{0x03, 0x01, 0x0f, 0x00, 0x0e}
		crtc = []uint8{0x5f, 0x4f, 0x50, 0x82, 0x54, 0x80, 0xbf, 0x1f, 0x00, 0x41, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x9c, 0x8e, 0x8f, 0x28, 0x40, 0x96, 0xb9, 0xa3, 0xff}
		attr = []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 0x41, 0x00, 0x0f, 0x00, 0x00}
		gc = []uint8{0, 0, 0, 0, 0, 0x40, 0x05, 0x0f, 0xff}
	case 0x12:
		misc = 0xe3
		seq = []uint8{0x03, 0x01, 0x0f, 0x00, 0x06}
		crtc = []uint8{0x5f, 0x4f, 0x50, 0x82, 0x54, 0x80, 0x0b, 0x3e, 0x00, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0xea, 0x8c, 0xdf, 0x28, 0x00, 0xe7, 0x04, 0xe3, 0xff}
		attr = []uint8{0, 1, 2, 3, 4, 5, 0x14, 7, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x3f, 0x01, 0x00, 0x0f, 0x00, 0x00}
		gc = []uint8{0, 0, 0, 0, 0, 0x00, 0x05, 0x0f, 0xff}
	default: // text mode 3 (rendered as blank)
		misc = 0x67
		seq = []uint8{0x03, 0x00, 0x03, 0x00, 0x02}
		crtc = []uint8{0x5f, 0x4f, 0x50, 0x82, 0x55, 0x81, 0xbf, 0x1f, 0x00, 0x4f, 0x0d, 0x0e, 0x00, 0x00, 0x00, 0x00,
			0x9c, 0x8e, 0x8f, 0x28, 0x1f, 0x96, 0xb9, 0xa3, 0xff}
		attr = []uint8{0, 1, 2, 3, 4, 5, 0x14, 7, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x3f, 0x0c, 0x00, 0x0f, 0x08, 0x00}
		gc = []uint8{0, 0, 0, 0, 0, 0x10, 0x0e, 0x0f, 0xff}
	}
	v.Misc = misc
	copy(v.Seq[:], seq)
	v.CRTC = [32]uint8{}
	copy(v.CRTC[:], crtc)
	v.Attr = [32]uint8{}
	copy(v.Attr[:], attr)
	v.GC = [16]uint8{}
	copy(v.GC[:], gc)
	v.AttrIdx = 0x20
	v.attrFlip = false
	if mode&0x80 == 0 {
		for p := 0; p < 4; p++ {
			for i := range v.Planes[p] {
				v.Planes[p][i] = 0
			}
		}
	}
	v.setDefaultDAC(mode & 0x7f)
	v.recalc()
}

func (v *VGA) setDefaultDAC(mode uint8) {
	// EGA-compatible 16 colours followed by greys and a colour cube.
	ega := func(i int) [3]uint8 {
		r := uint8((i>>2)&1)*42 + uint8((i>>5)&1)*21
		g := uint8((i>>1)&1)*42 + uint8((i>>4)&1)*21
		b := uint8(i&1)*42 + uint8((i>>3)&1)*21
		return [3]uint8{r, g, b}
	}
	cga := []int{0, 1, 2, 3, 4, 5, 0x14, 7, 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x3f}
	if mode == 0x13 {
		for i := 0; i < 16; i++ {
			v.DAC[i] = ega(cga[i])
		}
		for i := 16; i < 256; i++ {
			g := uint8((i - 16) % 64)
			v.DAC[i] = [3]uint8{g, g, g}
		}
		return
	}
	for i := 0; i < 64; i++ {
		v.DAC[i] = ega(i)
	}
}

// Aspect returns the width/height ratio of the visible picture as a VGA
// monitor shows it (the active area of a 640x480 signal fills a 4:3 tube;
// 400-line modes are stretched to the same height).
func (v *VGA) Aspect() float64 {
	clk := 25175000.0
	if (v.Misc>>2)&3 == 1 {
		clk = 28322000.0
	}
	dots := 9.0
	if v.Seq[1]&1 != 0 {
		dots = 8
	}
	if v.Seq[1]&8 != 0 {
		clk /= 2
	}
	active := float64(int(v.CRTC[1])+1) * dots / clk
	std := 640.0 / 25175000.0
	lines := float64(v.vde() + 1)
	ref := 480.0
	if v.vtotal()+2 < 480 {
		ref = 400
	}
	a := (4.0 / 3.0) * (active / std) / (lines / ref)
	if a < 0.5 || a > 3 {
		return 4.0 / 3.0
	}
	return a
}
