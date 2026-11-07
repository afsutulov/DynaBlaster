package machine

import "math"

// OPL is a Yamaha YM3812 (OPL2) FM synthesis emulator. It follows the
// well documented table-driven model of the chip (log-sine / exponential
// tables, rate-based envelope generator with the original increment
// patterns, LFO tables and the rhythm section phase generation), running
// at the chip's native sample rate of 3.579545 MHz / 72 = 49716 Hz.

const (
	oplRate   = 49716
	freqSh    = 16
	egSh      = 16
	lfoSh     = 24
	freqMask  = (1 << freqSh) - 1
	envBits   = 10
	envLen    = 1 << envBits
	envStep   = 128.0 / envLen
	maxAtt    = envLen - 1
	minAtt    = 0
	sinBits   = 10
	sinLen    = 1 << sinBits
	sinMask   = sinLen - 1
	tlResLen  = 256
	tlTabLen  = 12 * 2 * tlResLen
	envQuiet  = tlTabLen >> 4
	rateSteps = 8

	egAtt = 4
	egDec = 3
	egSus = 2
	egRel = 1
	egOff = 0
)

var (
	tlTab  [tlTabLen]int32
	sinTab [sinLen * 4]uint32
	fnTab  [1024]uint32
	ksltab [8 * 16]uint32
	slTab  [16]uint32
)

var mulTab = [16]uint32{1, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 20, 24, 24, 30, 30}

var egInc = [15 * rateSteps]uint8{
	0, 1, 0, 1, 0, 1, 0, 1,
	0, 1, 0, 1, 1, 1, 0, 1,
	0, 1, 1, 1, 0, 1, 1, 1,
	0, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 2, 1, 1, 1, 2,
	1, 2, 1, 2, 1, 2, 1, 2,
	1, 2, 2, 2, 1, 2, 2, 2,
	2, 2, 2, 2, 2, 2, 2, 2,
	2, 2, 2, 4, 2, 2, 2, 4,
	2, 4, 2, 4, 2, 4, 2, 4,
	2, 4, 4, 4, 2, 4, 4, 4,
	4, 4, 4, 4, 4, 4, 4, 4,
	8, 8, 8, 8, 8, 8, 8, 8,
	0, 0, 0, 0, 0, 0, 0, 0,
}

var egRateSelect [16 + 64 + 16]uint8
var egRateShift [16 + 64 + 16]uint8

var lfoAmTable [210]uint8

var lfoPmTable = [8 * 8 * 2]int8{
	0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
	1, 0, 0, 0, -1, 0, 0, 0,
	1, 0, 0, 0, -1, 0, 0, 0,
	2, 1, 0, -1, -2, -1, 0, 1,
	1, 0, 0, 0, -1, 0, 0, 0,
	3, 1, 0, -1, -3, -1, 0, 1,
	2, 1, 0, -1, -2, -1, 0, 1,
	4, 2, 0, -2, -4, -2, 0, 2,
	2, 1, 0, -1, -2, -1, 0, 1,
	5, 2, 0, -2, -5, -2, 0, 2,
	3, 1, 0, -1, -3, -1, 0, 1,
	6, 3, 0, -3, -6, -3, 0, 3,
	3, 1, 0, -1, -3, -1, 0, 1,
	7, 3, 0, -3, -7, -3, 0, 3,
}

func init() {
	for x := 0; x < tlResLen; x++ {
		m := float64(1<<16) / math.Pow(2, float64(x+1)*(envStep/4.0)/8.0)
		m = math.Floor(m)
		n := int32(m)
		n >>= 4
		if n&1 != 0 {
			n = (n >> 1) + 1
		} else {
			n = n >> 1
		}
		n <<= 1
		tlTab[x*2] = n
		tlTab[x*2+1] = -n
		for i := 1; i < 12; i++ {
			tlTab[x*2+i*2*tlResLen] = tlTab[x*2] >> uint(i)
			tlTab[x*2+1+i*2*tlResLen] = -tlTab[x*2+i*2*tlResLen]
		}
	}
	for i := 0; i < sinLen; i++ {
		m := math.Sin(float64(i*2+1) * math.Pi / sinLen)
		var o float64
		if m > 0 {
			o = 8 * math.Log(1.0/m) / math.Log(2)
		} else {
			o = 8 * math.Log(-1.0/m) / math.Log(2)
		}
		o = o / (envStep / 4)
		n := int(2.0 * o)
		if n&1 != 0 {
			n = (n >> 1) + 1
		} else {
			n >>= 1
		}
		s := uint32(n * 2)
		if m < 0 {
			s++
		}
		sinTab[i] = s
	}
	for i := 0; i < sinLen; i++ {
		if i&(1<<(sinBits-1)) != 0 {
			sinTab[sinLen+i] = tlTabLen
		} else {
			sinTab[sinLen+i] = sinTab[i]
		}
		sinTab[2*sinLen+i] = sinTab[i&(sinMask>>1)]
		if i&(1<<(sinBits-2)) != 0 {
			sinTab[3*sinLen+i] = tlTabLen
		} else {
			sinTab[3*sinLen+i] = sinTab[i&(sinMask>>2)]
		}
	}
	for i := 0; i < 1024; i++ {
		fnTab[i] = uint32(float64(i) * 64 * (1 << (freqSh - 10)))
	}
	oct7 := []float64{0, 9, 12, 13.875, 15, 16.125, 16.875, 17.625, 18, 18.75, 19.125, 19.5, 19.875, 20.25, 20.625, 21}
	dv := 0.1875 / 2.0
	for o := 0; o < 8; o++ {
		for i := 0; i < 16; i++ {
			v := 0.0
			if i > 0 {
				v = oct7[i] - 3*float64(7-o)
				if v < 0 {
					v = 0
				}
			}
			ksltab[o*16+i] = uint32(v / dv)
		}
	}
	// sustain levels in steps of 3 dB; the last entry is 93 dB
	for i := 0; i < 15; i++ {
		slTab[i] = uint32(float64(i) * 2.0 / envStep)
	}
	slTab[15] = uint32(31 * 2.0 / envStep)
	// rate select / shift tables
	idx := 0
	for i := 0; i < 16; i++ {
		egRateSelect[idx] = 14
		egRateShift[idx] = 0
		idx++
	}
	for r := 0; r < 13; r++ {
		for j := 0; j < 4; j++ {
			egRateSelect[idx] = uint8(j)
			egRateShift[idx] = uint8(12 - r)
			idx++
		}
	}
	for j := 0; j < 4; j++ {
		egRateSelect[idx] = uint8(4 + j)
		idx++
	}
	for j := 0; j < 4; j++ {
		egRateSelect[idx] = uint8(8 + j)
		idx++
	}
	for j := 0; j < 4; j++ {
		egRateSelect[idx] = 12
		idx++
	}
	for i := 0; i < 16; i++ {
		egRateSelect[idx] = 12
		idx++
	}
	for i := range egRateSelect {
		egRateSelect[i] *= rateSteps
	}
	// AM table
	k := 0
	for i := 0; i < 7; i++ {
		lfoAmTable[k] = 0
		k++
	}
	for v := 1; v <= 25; v++ {
		for j := 0; j < 4; j++ {
			lfoAmTable[k] = uint8(v)
			k++
		}
	}
	for j := 0; j < 3; j++ {
		lfoAmTable[k] = 26
		k++
	}
	for v := 25; v >= 1; v-- {
		for j := 0; j < 4; j++ {
			lfoAmTable[k] = uint8(v)
			k++
		}
	}
}

type oplSlot struct {
	ar, dr, rr uint32
	ksr        uint8
	KSR        uint8
	mul        uint32
	Cnt        uint32
	Incr       uint32
	FB         uint8
	connectAdd bool // connect1 → output (true) or phase modulation
	op1out     [2]int32
	CON        bool
	egType     bool
	state      int
	TL         uint32
	TLL        int32
	volume     int32
	sl         uint32
	egShAr     uint8
	egSelAr    uint8
	egShDr     uint8
	egSelDr    uint8
	egShRr     uint8
	egSelRr    uint8
	key        uint32
	AMmask     uint32
	vib        bool
	waveTable  uint32
	kslShift   uint8
}

type oplChan struct {
	slot      [2]oplSlot
	blockFnum uint32
	fc        uint32
	kslBase   uint32
	kcode     uint8
}

type OPL struct {
	ch         [9]oplChan
	egCnt      uint32
	egTimer    uint32
	rhythm     uint8
	lfoAmDepth bool
	lfoPmDepth uint8
	lfoAmCnt   uint32
	lfoPmCnt   uint32
	noiseRng   uint32
	noiseP     uint32
	wavesel    bool
	addr       uint8
	regs       [256]uint8
	lfoAm      uint32
	lfoPm      uint32
	phaseMod   int32
	output     int32
	timerStart [2]int64
	timerOn    [2]bool
	timerMask  [2]bool
	status     uint8
	now        int64 // virtual time of the current register write
}

// NewOPL creates the chip. Samples are produced at the native rate
// (49716 Hz); the mixer resamples them.
func NewOPL(rate int) *OPL {
	o := &OPL{noiseRng: 1}
	for c := range o.ch {
		for s := range o.ch[c].slot {
			o.ch[c].slot[s].waveTable = 0
			o.ch[c].slot[s].state = egOff
			o.ch[c].slot[s].volume = maxAtt
		}
	}
	o.Reset()
	return o
}

func (o *OPL) Reset() {
	o.egTimer = 0
	o.egCnt = 0
	o.noiseRng = 1
	for i := 0xff; i >= 0x20; i-- {
		o.writeReg(uint8(i), 0)
	}
	for c := range o.ch {
		for s := range o.ch[c].slot {
			sl := &o.ch[c].slot[s]
			sl.waveTable = 0
			sl.state = egOff
			sl.volume = maxAtt
		}
	}
}

// Status returns the timer status register.
func (o *OPL) Status(m *M) uint8 {
	now := m.Now()
	if o.timerOn[0] && !o.timerMask[0] {
		if now-o.timerStart[0] >= int64(256-int(o.regs[2]))*80000 {
			o.status |= 0xc0
		}
	}
	if o.timerOn[1] && !o.timerMask[1] {
		if now-o.timerStart[1] >= int64(256-int(o.regs[3]))*320000 {
			o.status |= 0xa0
		}
	}
	return o.status | 6
}

// Write handles a write to the address (port 0) or data (port 1) register.
func (o *OPL) Write(port uint16, v uint8) {
	if port&1 == 0 {
		o.addr = v
		return
	}
	o.writeReg(o.addr, v)
}

// timerCtl handles register 04h (timer start/mask/IRQ reset).
func (o *OPL) timerCtl(v uint8, now int64) {
	if v&0x80 != 0 {
		o.status = 0
		return
	}
	o.timerMask[0] = v&0x40 != 0
	o.timerMask[1] = v&0x20 != 0
	on0 := v&1 != 0
	on1 := v&2 != 0
	if on0 && !o.timerOn[0] {
		o.timerStart[0] = now
	}
	if on1 && !o.timerOn[1] {
		o.timerStart[1] = now
	}
	o.timerOn[0] = on0
	o.timerOn[1] = on1
}

var slotArray = [32]int{0, 2, 4, 1, 3, 5, -1, -1, 6, 8, 10, 7, 9, 11, -1, -1,
	12, 14, 16, 13, 15, 17, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}

func (o *OPL) slotOf(r uint8) *oplSlot {
	s := slotArray[r&0x1f]
	if s < 0 {
		return nil
	}
	return &o.ch[s/2].slot[s&1]
}

func (o *OPL) chanOfSlot(r uint8) *oplChan {
	s := slotArray[r&0x1f]
	return &o.ch[s/2]
}

func (o *OPL) calcFcSlot(ch *oplChan, sl *oplSlot) {
	sl.Incr = ch.fc * sl.mul
	ksr := ch.kcode >> sl.KSR
	if sl.ksr != ksr {
		sl.ksr = ksr
		if sl.ar+uint32(ksr) < 16+62 {
			sl.egShAr = egRateShift[sl.ar+uint32(ksr)]
			sl.egSelAr = egRateSelect[sl.ar+uint32(ksr)]
		} else {
			sl.egShAr = 0
			sl.egSelAr = 13 * rateSteps
		}
		sl.egShDr = egRateShift[sl.dr+uint32(ksr)]
		sl.egSelDr = egRateSelect[sl.dr+uint32(ksr)]
		sl.egShRr = egRateShift[sl.rr+uint32(ksr)]
		sl.egSelRr = egRateSelect[sl.rr+uint32(ksr)]
	}
}

func (o *OPL) setMul(r uint8, v uint8) {
	sl := o.slotOf(r)
	ch := o.chanOfSlot(r)
	sl.mul = mulTab[v&0x0f]
	if v&0x10 != 0 {
		sl.KSR = 0
	} else {
		sl.KSR = 2
	}
	sl.egType = v&0x20 != 0
	sl.vib = v&0x40 != 0
	if v&0x80 != 0 {
		sl.AMmask = 0xffffffff
	} else {
		sl.AMmask = 0
	}
	o.calcFcSlot(ch, sl)
}

func (o *OPL) setKslTl(r uint8, v uint8) {
	sl := o.slotOf(r)
	ch := o.chanOfSlot(r)
	ksl := v >> 6
	if ksl != 0 {
		sl.kslShift = 3 - ksl
	} else {
		sl.kslShift = 31
	}
	sl.TL = uint32(v&0x3f) << (envBits - 1 - 7)
	sl.TLL = int32(sl.TL + (ch.kslBase >> sl.kslShift))
}

func (o *OPL) setArDr(r uint8, v uint8) {
	sl := o.slotOf(r)
	if v>>4 != 0 {
		sl.ar = 16 + uint32(v>>4)<<2
	} else {
		sl.ar = 0
	}
	if sl.ar+uint32(sl.ksr) < 16+62 {
		sl.egShAr = egRateShift[sl.ar+uint32(sl.ksr)]
		sl.egSelAr = egRateSelect[sl.ar+uint32(sl.ksr)]
	} else {
		sl.egShAr = 0
		sl.egSelAr = 13 * rateSteps
	}
	if v&0x0f != 0 {
		sl.dr = 16 + uint32(v&0x0f)<<2
	} else {
		sl.dr = 0
	}
	sl.egShDr = egRateShift[sl.dr+uint32(sl.ksr)]
	sl.egSelDr = egRateSelect[sl.dr+uint32(sl.ksr)]
}

func (o *OPL) setSlRr(r uint8, v uint8) {
	sl := o.slotOf(r)
	sl.sl = slTab[v>>4]
	if v&0x0f != 0 {
		sl.rr = 16 + uint32(v&0x0f)<<2
	} else {
		sl.rr = 0
	}
	sl.egShRr = egRateShift[sl.rr+uint32(sl.ksr)]
	sl.egSelRr = egRateSelect[sl.rr+uint32(sl.ksr)]
}

func (sl *oplSlot) keyOn(set uint32) {
	if sl.key == 0 {
		sl.Cnt = 0
		sl.state = egAtt
	}
	sl.key |= set
}

func (sl *oplSlot) keyOff(clr uint32) {
	if sl.key != 0 {
		sl.key &= clr
		if sl.key == 0 && sl.state > egRel {
			sl.state = egRel
		}
	}
}

func (o *OPL) writeReg(r uint8, v uint8) {
	o.regs[r] = v
	switch r & 0xe0 {
	case 0x00:
		switch r & 0x1f {
		case 0x01:
			o.wavesel = v&0x20 != 0
		case 0x04:
			o.timerCtl(v, o.now)
		case 0x08:
		}
	case 0x20:
		if o.slotOf(r) != nil {
			o.setMul(r, v)
		}
	case 0x40:
		if o.slotOf(r) != nil {
			o.setKslTl(r, v)
		}
	case 0x60:
		if o.slotOf(r) != nil {
			o.setArDr(r, v)
		}
	case 0x80:
		if o.slotOf(r) != nil {
			o.setSlRr(r, v)
		}
	case 0xa0:
		if r == 0xbd {
			o.lfoAmDepth = v&0x80 != 0
			if v&0x40 != 0 {
				o.lfoPmDepth = 8
			} else {
				o.lfoPmDepth = 0
			}
			o.rhythm = v & 0x3f
			if o.rhythm&0x20 != 0 {
				bit := func(b uint8, sl *oplSlot) {
					if v&b != 0 {
						sl.keyOn(2)
					} else {
						sl.keyOff(^uint32(2))
					}
				}
				bit(0x10, &o.ch[6].slot[0])
				bit(0x10, &o.ch[6].slot[1])
				bit(0x01, &o.ch[7].slot[0])
				bit(0x08, &o.ch[7].slot[1])
				bit(0x04, &o.ch[8].slot[0])
				bit(0x02, &o.ch[8].slot[1])
			} else {
				for _, p := range [][2]int{{6, 0}, {6, 1}, {7, 0}, {7, 1}, {8, 0}, {8, 1}} {
					o.ch[p[0]].slot[p[1]].keyOff(^uint32(2))
				}
			}
			return
		}
		if r&0x0f > 8 {
			return
		}
		ch := &o.ch[r&0x0f]
		var bf uint32
		if r&0x10 == 0 {
			bf = ch.blockFnum&0x1f00 | uint32(v)
		} else {
			bf = uint32(v&0x1f)<<8 | ch.blockFnum&0xff
			if v&0x20 != 0 {
				ch.slot[0].keyOn(1)
				ch.slot[1].keyOn(1)
			} else {
				ch.slot[0].keyOff(^uint32(1))
				ch.slot[1].keyOff(^uint32(1))
			}
		}
		if ch.blockFnum != bf {
			block := uint8(bf >> 10)
			ch.blockFnum = bf
			ch.kslBase = ksltab[bf>>6]
			ch.fc = fnTab[bf&0x3ff] >> (7 - block)
			ch.kcode = uint8((ch.blockFnum & 0x1c00) >> 9)
			if o.regs[0x08]&0x40 != 0 {
				ch.kcode |= uint8((ch.blockFnum & 0x100) >> 8)
			} else {
				ch.kcode |= uint8((ch.blockFnum & 0x200) >> 9)
			}
			for s := 0; s < 2; s++ {
				sl := &ch.slot[s]
				sl.TLL = int32(sl.TL + (ch.kslBase >> sl.kslShift))
				o.calcFcSlot(ch, sl)
			}
		}
	case 0xc0:
		if r&0x0f > 8 {
			return
		}
		ch := &o.ch[r&0x0f]
		fb := (v >> 1) & 7
		if fb != 0 {
			ch.slot[0].FB = fb + 7
		} else {
			ch.slot[0].FB = 0
		}
		ch.slot[0].CON = v&1 != 0
		ch.slot[0].connectAdd = v&1 != 0
	case 0xe0:
		sl := o.slotOf(r)
		if sl == nil {
			return
		}
		if o.wavesel {
			sl.waveTable = uint32(v&3) * sinLen
		}
	}
}

func (o *OPL) advanceLFO() {
	o.lfoAmCnt += uint32((1.0 / 64.0) * (1 << lfoSh))
	if o.lfoAmCnt >= uint32(210)<<lfoSh {
		o.lfoAmCnt -= uint32(210) << lfoSh
	}
	tmp := uint32(lfoAmTable[o.lfoAmCnt>>lfoSh])
	if o.lfoAmDepth {
		o.lfoAm = tmp
	} else {
		o.lfoAm = tmp >> 2
	}
	o.lfoPmCnt += uint32((1.0 / 1024.0) * (1 << lfoSh))
	o.lfoPm = ((o.lfoPmCnt >> lfoSh) & 7) | uint32(o.lfoPmDepth)
}

func (o *OPL) advance() {
	o.egTimer += 1 << egSh
	for o.egTimer >= 1<<egSh {
		o.egTimer -= 1 << egSh
		o.egCnt++
		for c := 0; c < 9; c++ {
			for s := 0; s < 2; s++ {
				op := &o.ch[c].slot[s]
				switch op.state {
				case egAtt:
					if o.egCnt&((1<<op.egShAr)-1) == 0 {
						inc := int32(egInc[uint32(op.egSelAr)+((o.egCnt>>op.egShAr)&7)])
						op.volume += (^op.volume * inc) >> 3
						if op.volume <= minAtt {
							op.volume = minAtt
							op.state = egDec
						}
					}
				case egDec:
					if o.egCnt&((1<<op.egShDr)-1) == 0 {
						op.volume += int32(egInc[uint32(op.egSelDr)+((o.egCnt>>op.egShDr)&7)])
						if op.volume >= int32(op.sl) {
							op.state = egSus
						}
					}
				case egSus:
					if !op.egType {
						if o.egCnt&((1<<op.egShRr)-1) == 0 {
							op.volume += int32(egInc[uint32(op.egSelRr)+((o.egCnt>>op.egShRr)&7)])
							if op.volume >= maxAtt {
								op.volume = maxAtt
							}
						}
					}
				case egRel:
					if o.egCnt&((1<<op.egShRr)-1) == 0 {
						op.volume += int32(egInc[uint32(op.egSelRr)+((o.egCnt>>op.egShRr)&7)])
						if op.volume >= maxAtt {
							op.volume = maxAtt
							op.state = egOff
						}
					}
				}
			}
		}
	}
	for c := 0; c < 9; c++ {
		ch := &o.ch[c]
		for s := 0; s < 2; s++ {
			op := &ch.slot[s]
			if op.vib {
				bf := ch.blockFnum
				fnumLfo := (bf & 0x0380) >> 7
				off := int32(lfoPmTable[o.lfoPm+16*fnumLfo])
				if off != 0 {
					bf = uint32(int32(bf) + off)
					block := (bf & 0x1c00) >> 10
					op.Cnt += (fnTab[bf&0x03ff] >> (7 - block)) * op.mul
				} else {
					op.Cnt += op.Incr
				}
			} else {
				op.Cnt += op.Incr
			}
		}
	}
	o.noiseP += 1 << freqSh
	i := o.noiseP >> freqSh
	o.noiseP &= freqMask
	for ; i > 0; i-- {
		if o.noiseRng&1 != 0 {
			o.noiseRng ^= 0x800302
		}
		o.noiseRng >>= 1
	}
}

func opCalc(phase uint32, env uint32, pm int32, wt uint32) int32 {
	p := (env << 4) + sinTab[wt+((uint32(int32(phase&^freqMask)+(pm<<16))>>freqSh)&sinMask)]
	if p >= tlTabLen {
		return 0
	}
	return tlTab[p]
}

func opCalc1(phase uint32, env uint32, pm int32, wt uint32) int32 {
	p := (env << 4) + sinTab[wt+((uint32(int32(phase&^freqMask)+pm)>>freqSh)&sinMask)]
	if p >= tlTabLen {
		return 0
	}
	return tlTab[p]
}

func (o *OPL) volCalc(op *oplSlot) uint32 {
	return uint32(op.TLL) + uint32(op.volume) + (o.lfoAm & op.AMmask)
}

func (o *OPL) chanCalc(ch *oplChan) {
	o.phaseMod = 0
	s1 := &ch.slot[0]
	env := o.volCalc(s1)
	out := s1.op1out[0] + s1.op1out[1]
	s1.op1out[0] = s1.op1out[1]
	if s1.connectAdd {
		o.output += s1.op1out[0]
	} else {
		o.phaseMod += s1.op1out[0]
	}
	s1.op1out[1] = 0
	if env < envQuiet {
		if s1.FB == 0 {
			out = 0
		}
		s1.op1out[1] = opCalc1(s1.Cnt, env, out<<s1.FB, s1.waveTable)
	}
	s2 := &ch.slot[1]
	env = o.volCalc(s2)
	if env < envQuiet {
		o.output += opCalc(s2.Cnt, env, o.phaseMod, s2.waveTable)
	}
}

func (o *OPL) rhythmCalc() {
	noise := o.noiseRng & 1
	ch := &o.ch
	o.phaseMod = 0
	s := &ch[6].slot[0]
	env := o.volCalc(s)
	out := s.op1out[0] + s.op1out[1]
	s.op1out[0] = s.op1out[1]
	if !s.CON {
		o.phaseMod = s.op1out[0]
	}
	s.op1out[1] = 0
	if env < envQuiet {
		if s.FB == 0 {
			out = 0
		}
		s.op1out[1] = opCalc1(s.Cnt, env, out<<s.FB, s.waveTable)
	}
	s = &ch[6].slot[1]
	env = o.volCalc(s)
	if env < envQuiet {
		o.output += opCalc(s.Cnt, env, o.phaseMod, s.waveTable) * 2
	}
	s71, s72 := &ch[7].slot[0], &ch[7].slot[1]
	s81, s82 := &ch[8].slot[0], &ch[8].slot[1]
	env = o.volCalc(s71)
	if env < envQuiet {
		bit7 := ((s71.Cnt >> freqSh) >> 7) & 1
		bit3 := ((s71.Cnt >> freqSh) >> 3) & 1
		bit2 := ((s71.Cnt >> freqSh) >> 2) & 1
		res1 := (bit2 ^ bit7) | bit3
		var phase uint32 = 0xd0
		if res1 != 0 {
			phase = 0x200 | (0xd0 >> 2)
		}
		bit5e := ((s82.Cnt >> freqSh) >> 5) & 1
		bit3e := ((s82.Cnt >> freqSh) >> 3) & 1
		if bit3e^bit5e != 0 {
			phase = 0x200 | (0xd0 >> 2)
		}
		if phase&0x200 != 0 {
			if noise != 0 {
				phase = 0x200 | 0xd0
			}
		} else if noise != 0 {
			phase = 0xd0 >> 2
		}
		o.output += opCalc(phase<<freqSh, env, 0, s71.waveTable) * 2
	}
	env = o.volCalc(s72)
	if env < envQuiet {
		bit8 := ((s71.Cnt >> freqSh) >> 8) & 1
		var phase uint32 = 0x100
		if bit8 != 0 {
			phase = 0x200
		}
		if noise != 0 {
			phase ^= 0x100
		}
		o.output += opCalc(phase<<freqSh, env, 0, s72.waveTable) * 2
	}
	env = o.volCalc(s81)
	if env < envQuiet {
		o.output += opCalc(s81.Cnt, env, 0, s81.waveTable) * 2
	}
	env = o.volCalc(s82)
	if env < envQuiet {
		bit7 := ((s71.Cnt >> freqSh) >> 7) & 1
		bit3 := ((s71.Cnt >> freqSh) >> 3) & 1
		bit2 := ((s71.Cnt >> freqSh) >> 2) & 1
		res1 := (bit2 ^ bit7) | bit3
		var phase uint32 = 0x100
		if res1 != 0 {
			phase = 0x300
		}
		bit5e := ((s82.Cnt >> freqSh) >> 5) & 1
		bit3e := ((s82.Cnt >> freqSh) >> 3) & 1
		if bit3e^bit5e != 0 {
			phase = 0x300
		}
		o.output += opCalc(phase<<freqSh, env, 0, s82.waveTable) * 2
	}
}

// Sample generates one sample at 49716 Hz.
func (o *OPL) Sample() int32 {
	o.advanceLFO()
	o.output = 0
	for c := 0; c < 6; c++ {
		o.chanCalc(&o.ch[c])
	}
	if o.rhythm&0x20 == 0 {
		o.chanCalc(&o.ch[6])
		o.chanCalc(&o.ch[7])
		o.chanCalc(&o.ch[8])
	} else {
		o.rhythmCalc()
	}
	out := o.output
	o.advance()
	return out
}
