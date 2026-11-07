package main

import (
	"github.com/hajimehoshi/ebiten/v2"

	"dynablaster/machine"
)

// Gamepads are mapped onto the game's two keyboard control sets. The scan
// codes are read from the game's own key table, so keys redefined in the
// SETUP menu keep working with a gamepad.
//
// Key table in the game's data segment: set 1 at 205Dh (up, down, left,
// right, fire, remote control; break/make code pairs), set 2 at 2069h
// (the same without the last key).
const (
	gameDS  = 0x1175
	keySet1 = 0x205d
	keySet2 = 0x2069
	stickDZ = 0.5
	numPads = 2
	numBtns = 6
)

type padState struct {
	held [numBtns]bool
}

type padMapper struct {
	ids   []ebiten.GamepadID
	state [numPads]padState
}

func (p *padMapper) update(m *machine.M) {
	p.ids = ebiten.AppendGamepadIDs(p.ids[:0])
	for i := 0; i < numPads; i++ {
		var want [numBtns]bool
		if i < len(p.ids) {
			want = readPad(p.ids[i])
		}
		base := uint32(gameDS)<<4 + keySet1
		n := numBtns
		if i == 1 {
			base = uint32(gameDS)<<4 + keySet2
			n = 5
		}
		for b := 0; b < n; b++ {
			if want[b] == p.state[i].held[b] {
				continue
			}
			p.state[i].held[b] = want[b]
			code := m.Mem[base+uint32(b)*2+1] & 0x7f
			if code == 0 {
				continue
			}
			if want[b] {
				m.Kbd.Push(code)
			} else {
				m.Kbd.Push(code | 0x80)
			}
		}
	}
}

func readPad(id ebiten.GamepadID) (s [numBtns]bool) {
	if ebiten.IsStandardGamepadLayoutAvailable(id) {
		x := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickHorizontal)
		y := ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickVertical)
		btn := func(b ebiten.StandardGamepadButton) bool { return ebiten.IsStandardGamepadButtonPressed(id, b) }
		s[0] = btn(ebiten.StandardGamepadButtonLeftTop) || y < -stickDZ
		s[1] = btn(ebiten.StandardGamepadButtonLeftBottom) || y > stickDZ
		s[2] = btn(ebiten.StandardGamepadButtonLeftLeft) || x < -stickDZ
		s[3] = btn(ebiten.StandardGamepadButtonLeftRight) || x > stickDZ
		s[4] = btn(ebiten.StandardGamepadButtonRightBottom) || btn(ebiten.StandardGamepadButtonRightLeft)
		s[5] = btn(ebiten.StandardGamepadButtonRightRight) || btn(ebiten.StandardGamepadButtonRightTop)
		return
	}
	if ebiten.GamepadAxisCount(id) >= 2 {
		x := ebiten.GamepadAxisValue(id, 0)
		y := ebiten.GamepadAxisValue(id, 1)
		s[0], s[1], s[2], s[3] = y < -stickDZ, y > stickDZ, x < -stickDZ, x > stickDZ
	}
	if ebiten.GamepadButtonCount(id) >= 1 {
		s[4] = ebiten.IsGamepadButtonPressed(id, 0)
	}
	if ebiten.GamepadButtonCount(id) >= 2 {
		s[5] = ebiten.IsGamepadButtonPressed(id, 1)
	}
	return
}
