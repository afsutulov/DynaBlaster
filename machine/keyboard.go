package machine

import "sync"

// Keyboard emulates the 8042 keyboard controller as seen through ports
// 60h/61h/64h and IRQ 1. Scan codes (set 1) are queued by the frontend.
type Keyboard struct {
	m      *M
	mu     sync.Mutex
	queue  []uint8
	data   uint8
	port61 uint8
	next   int64
	ready  bool
	// Buffer is the BIOS type-ahead buffer (scan code << 8 | ASCII).
	Buffer []uint16
	Shift  uint8
	e0     bool
}

func NewKeyboard(m *M) *Keyboard { return &Keyboard{m: m} }

// Push queues a scan code (thread safe).
func (k *Keyboard) Push(codes ...uint8) {
	k.mu.Lock()
	k.queue = append(k.queue, codes...)
	k.mu.Unlock()
}

// Pending returns the number of queued scan codes.
func (k *Keyboard) Pending() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.queue)
}

func (k *Keyboard) Next() int64 {
	if k.Pending() > 0 {
		return k.m.Now() + 500000 // poll every 0.5 ms while keys are queued
	}
	return 1 << 62
}

func (k *Keyboard) Tick(m *M, now int64) {
	if m.PIC.Busy(1) {
		return
	}
	k.mu.Lock()
	if len(k.queue) > 0 && now >= k.next {
		k.data = k.queue[0]
		k.queue = k.queue[1:]
		k.ready = true
		k.next = now + 1000000 // 1 ms between bytes
		k.mu.Unlock()
		m.RaiseIRQ(1)
		return
	}
	k.mu.Unlock()
}

func (k *Keyboard) In(port uint16) uint8 {
	switch port {
	case 0x60:
		k.ready = false
		return k.data
	case 0x61:
		k.port61 ^= 0x10 // refresh toggle
		return k.port61
	case 0x64:
		if k.ready {
			return 0x1d
		}
		return 0x1c
	}
	return 0xff
}

func (k *Keyboard) Out(port uint16, v uint8) {
	if port == 0x61 {
		k.port61 = v
	}
}
