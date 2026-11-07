package main

import "github.com/hajimehoshi/ebiten/v2"

// scan is an IBM PC/AT scan code (set 1); ext marks keys of the enhanced
// keyboard that are sent with an E0h prefix.
type scan struct {
	code uint8
	ext  bool
}

func (s scan) make() []uint8 {
	if s.ext {
		return []uint8{0xe0, s.code}
	}
	return []uint8{s.code}
}

func (s scan) brk() []uint8 {
	if s.ext {
		return []uint8{0xe0, s.code | 0x80}
	}
	return []uint8{s.code | 0x80}
}

var scanCodes = map[ebiten.Key]scan{
	ebiten.KeyEscape: {0x01, false},
	ebiten.KeyDigit1: {0x02, false}, ebiten.KeyDigit2: {0x03, false}, ebiten.KeyDigit3: {0x04, false},
	ebiten.KeyDigit4: {0x05, false}, ebiten.KeyDigit5: {0x06, false}, ebiten.KeyDigit6: {0x07, false},
	ebiten.KeyDigit7: {0x08, false}, ebiten.KeyDigit8: {0x09, false}, ebiten.KeyDigit9: {0x0a, false},
	ebiten.KeyDigit0: {0x0b, false}, ebiten.KeyMinus: {0x0c, false}, ebiten.KeyEqual: {0x0d, false},
	ebiten.KeyBackspace: {0x0e, false}, ebiten.KeyTab: {0x0f, false},
	ebiten.KeyQ: {0x10, false}, ebiten.KeyW: {0x11, false}, ebiten.KeyE: {0x12, false}, ebiten.KeyR: {0x13, false},
	ebiten.KeyT: {0x14, false}, ebiten.KeyY: {0x15, false}, ebiten.KeyU: {0x16, false}, ebiten.KeyI: {0x17, false},
	ebiten.KeyO: {0x18, false}, ebiten.KeyP: {0x19, false}, ebiten.KeyBracketLeft: {0x1a, false},
	ebiten.KeyBracketRight: {0x1b, false}, ebiten.KeyEnter: {0x1c, false}, ebiten.KeyControlLeft: {0x1d, false},
	ebiten.KeyA: {0x1e, false}, ebiten.KeyS: {0x1f, false}, ebiten.KeyD: {0x20, false}, ebiten.KeyF: {0x21, false},
	ebiten.KeyG: {0x22, false}, ebiten.KeyH: {0x23, false}, ebiten.KeyJ: {0x24, false}, ebiten.KeyK: {0x25, false},
	ebiten.KeyL: {0x26, false}, ebiten.KeySemicolon: {0x27, false}, ebiten.KeyQuote: {0x28, false},
	ebiten.KeyBackquote: {0x29, false}, ebiten.KeyShiftLeft: {0x2a, false}, ebiten.KeyBackslash: {0x2b, false},
	ebiten.KeyZ: {0x2c, false}, ebiten.KeyX: {0x2d, false}, ebiten.KeyC: {0x2e, false}, ebiten.KeyV: {0x2f, false},
	ebiten.KeyB: {0x30, false}, ebiten.KeyN: {0x31, false}, ebiten.KeyM: {0x32, false}, ebiten.KeyComma: {0x33, false},
	ebiten.KeyPeriod: {0x34, false}, ebiten.KeySlash: {0x35, false}, ebiten.KeyShiftRight: {0x36, false},
	ebiten.KeyNumpadMultiply: {0x37, false}, ebiten.KeyAltLeft: {0x38, false}, ebiten.KeySpace: {0x39, false},
	ebiten.KeyCapsLock: {0x3a, false},
	ebiten.KeyF1:       {0x3b, false}, ebiten.KeyF2: {0x3c, false}, ebiten.KeyF3: {0x3d, false}, ebiten.KeyF4: {0x3e, false},
	ebiten.KeyF5: {0x3f, false}, ebiten.KeyF6: {0x40, false}, ebiten.KeyF7: {0x41, false}, ebiten.KeyF8: {0x42, false},
	ebiten.KeyF9: {0x43, false}, ebiten.KeyF10: {0x44, false},
	ebiten.KeyNumLock: {0x45, false}, ebiten.KeyScrollLock: {0x46, false},
	ebiten.KeyNumpad7: {0x47, false}, ebiten.KeyNumpad8: {0x48, false}, ebiten.KeyNumpad9: {0x49, false},
	ebiten.KeyNumpadSubtract: {0x4a, false}, ebiten.KeyNumpad4: {0x4b, false}, ebiten.KeyNumpad5: {0x4c, false},
	ebiten.KeyNumpad6: {0x4d, false}, ebiten.KeyNumpadAdd: {0x4e, false}, ebiten.KeyNumpad1: {0x4f, false},
	ebiten.KeyNumpad2: {0x50, false}, ebiten.KeyNumpad3: {0x51, false}, ebiten.KeyNumpad0: {0x52, false},
	ebiten.KeyNumpadDecimal: {0x53, false},
	ebiten.KeyNumpadEnter:   {0x1c, true}, ebiten.KeyControlRight: {0x1d, true}, ebiten.KeyNumpadDivide: {0x35, true},
	ebiten.KeyAltRight: {0x38, true},
	ebiten.KeyHome:     {0x47, true}, ebiten.KeyArrowUp: {0x48, true}, ebiten.KeyPageUp: {0x49, true},
	ebiten.KeyArrowLeft: {0x4b, true}, ebiten.KeyArrowRight: {0x4d, true}, ebiten.KeyEnd: {0x4f, true},
	ebiten.KeyArrowDown: {0x50, true}, ebiten.KeyPageDown: {0x51, true}, ebiten.KeyInsert: {0x52, true},
	ebiten.KeyDelete: {0x53, true},
}
