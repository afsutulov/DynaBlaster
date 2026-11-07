package machine

import "testing"

func TestALUFlags(t *testing.T) {
	var c CPU
	if r := c.Add8(0x7f, 1); r != 0x80 || !c.OF() || !c.SF() || c.CF() || c.ZF() {
		t.Errorf("add8 overflow: r=%02x flags=%04x", r, c.Flags)
	}
	if r := c.Sub16(0, 1); r != 0xffff || !c.CF() || !c.SF() || c.OF() {
		t.Errorf("sub16 borrow: r=%04x flags=%04x", r, c.Flags)
	}
	c.SetCF(true)
	if r := c.Inc16(0xffff); r != 0 || !c.ZF() || !c.CF() {
		t.Errorf("inc16 must keep CF: r=%04x flags=%04x", r, c.Flags)
	}
	if r := c.Shift16(5, 0x8001, 1); r != 0x4000 || !c.CF() || !c.OF() {
		t.Errorf("shr16: r=%04x flags=%04x", r, c.Flags)
	}
	c.SetCF(true)
	if r := c.Shift8(2, 0x80, 1); r != 0x01 || !c.CF() {
		t.Errorf("rcl8: r=%02x flags=%04x", r, c.Flags)
	}
	c.R[AX] = 0x1234
	c.R[DX] = 0
	if !c.Div16(0x10) || c.R[AX] != 0x123 || c.R[DX] != 4 {
		t.Errorf("div16: ax=%04x dx=%04x", c.R[AX], c.R[DX])
	}
	c.R[AX] = 0x0100
	if c.Div8(1) {
		t.Errorf("div8 overflow not detected")
	}
}

func TestDecode(t *testing.T) {
	code := []byte{0x26, 0x8b, 0x47, 0x10} // mov ax,[es:bx+0x10]
	in := Decode(func(i int) byte { return code[i] })
	if in.Len != 4 || in.Op != 0x8b || in.Seg != ES || in.Mod != 1 || in.RM != 7 || in.Disp != 0x10 {
		t.Errorf("decode: %+v", in)
	}
}
