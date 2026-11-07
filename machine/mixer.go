package machine

import (
	"math"
	"sync"
)

// MixRate is the output sample rate of the emulated audio.
const MixRate = 48000

// Mixer renders the OPL2 and Sound Blaster DAC outputs in virtual time
// into a ring buffer of signed 16-bit stereo frames.
type Mixer struct {
	m        *M
	last     int64 // virtual time rendered so far
	frac     float64
	oplPhase float64
	oplPrev  int32
	oplCur   int32
	dcIn     float64
	dcOut    float64

	mu   sync.Mutex
	ring []int16 // interleaved stereo
	r, w int
	n    int

	// Volume of the FM and PCM paths (1.0 = unity).
	FMVol, PCMVol float64
}

const ringFrames = MixRate // one second

func NewMixer(m *M) *Mixer {
	return &Mixer{m: m, ring: make([]int16, ringFrames*2), FMVol: 1.0, PCMVol: 1.0}
}

func (x *Mixer) Next() int64 { return x.last + 2000000 }

func (x *Mixer) Tick(m *M, now int64) { x.Sync() }

// Sync renders audio up to the current virtual time.
func (x *Mixer) Sync() {
	now := x.m.Now()
	if now <= x.last {
		return
	}
	x.frac += float64(now-x.last) * MixRate / 1e9
	x.last = now
	n := int(x.frac)
	x.frac -= float64(n)
	if n <= 0 {
		return
	}
	step := float64(oplRate) / MixRate
	buf := make([]int16, 0, n*2)
	for i := 0; i < n; i++ {
		x.oplPhase += step
		for x.oplPhase >= 1 {
			x.oplPhase--
			x.oplPrev = x.oplCur
			x.oplCur = x.m.OPL.Sample()
		}
		fm := float64(x.oplPrev) + (float64(x.oplCur)-float64(x.oplPrev))*x.oplPhase
		pcm := (float64(x.m.SB.nextSample(MixRate)) - 128) * 256
		// analog output stage of the card: DC blocking capacitor
		x.dcOut = pcm - x.dcIn + 0.9985*x.dcOut
		x.dcIn = pcm
		pcm = x.dcOut
		// The card sums the FM and DAC outputs in its analog output stage;
		// the levels below keep both paths in the range they had relative
		// to each other, and a soft limiter replaces hard clipping.
		v := fm*x.FMVol*0.7 + pcm*x.PCMVol*0.55
		v = softClip(v)
		buf = append(buf, int16(v), int16(v))
	}
	x.mu.Lock()
	for _, s := range buf {
		x.ring[x.w] = s
		x.w = (x.w + 1) % len(x.ring)
		if x.n < len(x.ring) {
			x.n++
		} else {
			x.r = (x.r + 1) % len(x.ring)
		}
	}
	x.mu.Unlock()
}

// Buffered returns the number of buffered stereo frames.
func (x *Mixer) Buffered() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.n / 2
}

// Read copies interleaved samples into dst and returns the count.
func (x *Mixer) Read(dst []int16) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	c := 0
	for c < len(dst) && x.n > 0 {
		dst[c] = x.ring[x.r]
		x.r = (x.r + 1) % len(x.ring)
		x.n--
		c++
	}
	return c
}

// softClip limits the signal to the 16-bit range without hard edges.
func softClip(v float64) float64 {
	const knee = 24000.0
	a := math.Abs(v)
	if a <= knee {
		return v
	}
	over := (a - knee) / (32767 - knee)
	r := knee + (32767-knee)*math.Tanh(over)
	if v < 0 {
		return -r
	}
	return r
}
