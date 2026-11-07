// Dyna Blaster for modern operating systems.
//
// The program runs the game logic (package game) on top of the Go
// implementation of the PC hardware the game was written for (package
// machine) and presents it in a window with sound.
package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"dynablaster/game"
	"dynablaster/machine"
	"dynablaster/runner"
)

//go:embed icon/icon16.png
var icon16 []byte

//go:embed icon/icon32.png
var icon32 []byte

//go:embed icon/icon48.png
var icon48 []byte

//go:embed icon/icon256.png
var icon256 []byte

// ---------- persistent storage (DYNA.CFG) ----------

type dirStorage struct{ dir string }

func (d dirStorage) Load(name string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(d.dir, filepath.FromSlash(name)))
	return b, err == nil
}

func (d dirStorage) Save(name string, data []byte) {
	p := filepath.Join(d.dir, filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, data, 0644)
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "DynaBlaster")
	}
	return "."
}

// ---------- audio ----------

type audioStream struct {
	m   *machine.M
	buf []int16
}

func (s *audioStream) Read(p []byte) (int, error) {
	n := len(p) / 4 * 4
	if n == 0 {
		return 0, nil
	}
	samples := n / 2
	if cap(s.buf) < samples {
		s.buf = make([]int16, samples)
	}
	buf := s.buf[:samples]
	// keep latency bounded: drop audio that piled up (e.g. after a pause)
	if s.m.Snd.Buffered() > machine.MixRate/8 {
		drop := make([]int16, (s.m.Snd.Buffered()-machine.MixRate/20)*2)
		s.m.Snd.Read(drop)
	}
	got := s.m.Snd.Read(buf)
	for i := got; i < samples; i++ {
		buf[i] = 0
	}
	for i, v := range buf {
		p[i*2] = byte(v)
		p[i*2+1] = byte(uint16(v) >> 8)
	}
	return n, nil
}

// ---------- game ----------

// minKeyHold is the shortest time a key is reported as held down.
const minKeyHold = 45 * time.Millisecond

type app struct {
	r      *runner.Runner
	m      *machine.M
	frame  runner.Frame
	pix    []byte
	img    *ebiten.Image
	mu     sync.Mutex
	smooth bool
	pads   padMapper

	pressed   map[ebiten.Key]time.Time
	pendingUp map[ebiten.Key]time.Time

	shotPath  string
	shotAfter time.Duration
	started   time.Time
}

func (a *app) Update() error {
	select {
	case <-a.r.Done():
		if e := a.r.Err(); e != nil {
			return fmt.Errorf("emulation stopped: %v", e)
		}
		return ebiten.Termination
	default:
	}
	// host-only shortcuts
	if inpututil.IsKeyJustPressed(ebiten.KeyF11) ||
		(inpututil.IsKeyJustPressed(ebiten.KeyEnter) && (ebiten.IsKeyPressed(ebiten.KeyAltLeft) || ebiten.IsKeyPressed(ebiten.KeyAltRight))) {
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
		return nil
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPause) || inpututil.IsKeyJustPressed(ebiten.KeyF12) {
		a.r.SetPaused(!a.r.Paused())
	}
	a.pads.update(a.m)
	// keyboard → PC/AT scan codes
	var keys []ebiten.Key
	now := time.Now()
	keys = inpututil.AppendJustPressedKeys(keys[:0])
	for _, k := range keys {
		if sc, ok := scanCodes[k]; ok && !hostKey(k) {
			a.m.Kbd.Push(sc.make()...)
			a.pressed[k] = now
			delete(a.pendingUp, k)
		}
	}
	keys = inpututil.AppendJustReleasedKeys(keys[:0])
	for _, k := range keys {
		if _, ok := scanCodes[k]; ok && !hostKey(k) {
			// A physical key stays down for tens of milliseconds; very short
			// taps (or synthetic events) are stretched so that the game, which
			// samples the keys once per frame, always sees them.
			a.pendingUp[k] = a.pressed[k].Add(minKeyHold)
		}
	}
	for k, t := range a.pendingUp {
		if now.After(t) {
			a.m.Kbd.Push(scanCodes[k].brk()...)
			delete(a.pendingUp, k)
		}
	}
	return nil
}

func hostKey(k ebiten.Key) bool {
	if k == ebiten.KeyF11 || k == ebiten.KeyF12 || k == ebiten.KeyPause {
		return true
	}
	if k == ebiten.KeyEnter && (ebiten.IsKeyPressed(ebiten.KeyAltLeft) || ebiten.IsKeyPressed(ebiten.KeyAltRight)) {
		return true
	}
	return false
}

func (a *app) Draw(screen *ebiten.Image) {
	f, pix := a.r.Latest(a.pix)
	a.pix = pix
	if f.W == 0 {
		return
	}
	if a.img == nil || a.img.Bounds().Dx() != f.W || a.img.Bounds().Dy() != f.H {
		a.img = ebiten.NewImage(f.W, f.H)
	}
	a.img.WritePixels(f.Pix)
	sw, sh := screen.Bounds().Dx(), screen.Bounds().Dy()
	// fit the picture with the aspect ratio of a 4:3 VGA monitor
	dw := float64(sw)
	dh := dw / f.Aspect
	if dh > float64(sh) {
		dh = float64(sh)
		dw = dh * f.Aspect
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(dw/float64(f.W), dh/float64(f.H))
	op.GeoM.Translate((float64(sw)-dw)/2, (float64(sh)-dh)/2)
	if a.smooth {
		op.Filter = ebiten.FilterLinear
	} else {
		op.Filter = ebiten.FilterNearest
	}
	screen.DrawImage(a.img, op)
	if a.shotPath != "" && time.Since(a.started) > a.shotAfter {
		px := make([]byte, 4*sw*sh)
		screen.ReadPixels(px)
		im := &image.RGBA{Pix: px, Stride: 4 * sw, Rect: image.Rect(0, 0, sw, sh)}
		if f, err := os.Create(a.shotPath); err == nil {
			png.Encode(f, im)
			f.Close()
		}
		a.shotPath = ""
	}
}

func (a *app) Layout(w, h int) (int, int) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return int(float64(w) * s), int(float64(h) * s)
}

func main() {
	scale := flag.Int("scale", 3, "initial window scale")
	full := flag.Bool("fullscreen", false, "start in fullscreen mode")
	smooth := flag.Bool("smooth", false, "bilinear filtering instead of sharp pixels")
	nosound := flag.Bool("nosound", false, "disable audio output")
	interp := flag.Bool("interpret", false, "run the reference interpreter instead of the translated code")
	cfgDir := flag.String("config", configDir(), "directory for the setup file (DYNA.CFG)")
	shot := flag.String("screenshot", "", "save a screenshot of the window to this file (testing)")
	shotAfter := flag.Duration("screenshot-after", 10*time.Second, "delay before -screenshot")
	flag.Parse()

	m := game.New(game.Options{Storage: dirStorage{*cfgDir}, Interpret: *interp})
	r := runner.New(m)

	ebiten.SetWindowTitle("Dyna Blaster")
	ebiten.SetWindowSize(256**scale, 232**scale)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetFullscreen(*full)
	ebiten.SetTPS(120)
	ebiten.SetVsyncEnabled(true)
	var icons []image.Image
	for _, b := range [][]byte{icon16, icon32, icon48, icon256} {
		if img, err := png.Decode(bytes.NewReader(b)); err == nil {
			icons = append(icons, img)
		}
	}
	ebiten.SetWindowIcon(icons)

	if !*nosound {
		actx := audio.NewContext(machine.MixRate)
		player, err := actx.NewPlayer(&audioStream{m: m})
		if err == nil {
			player.SetBufferSize(60 * time.Millisecond)
			player.Play()
		}
	}

	go r.Run()
	a := &app{r: r, m: m, smooth: *smooth,
		pressed: map[ebiten.Key]time.Time{}, pendingUp: map[ebiten.Key]time.Time{}, shotPath: *shot, shotAfter: *shotAfter, started: time.Now()}
	if err := ebiten.RunGame(a); err != nil {
		r.Stop()
		if !strings.Contains(err.Error(), "termination") {
			fmt.Fprintln(os.Stderr, err)
			if strings.Contains(err.Error(), "audio") {
				fmt.Fprintln(os.Stderr, "(no usable audio device: start with -nosound)")
			}
			os.Exit(1)
		}
	}
	r.Stop()
}
