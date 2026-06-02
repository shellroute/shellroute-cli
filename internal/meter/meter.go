package meter

import (
	"sync/atomic"
)

// Meter tracks bidirectional bandwidth usage.
type Meter struct {
	bytesUp   atomic.Int64
	bytesDown atomic.Int64
}

func New() *Meter {
	return &Meter{}
}

func (m *Meter) AddUp(n int64) {
	m.bytesUp.Add(n)
}

func (m *Meter) AddDown(n int64) {
	m.bytesDown.Add(n)
}

func (m *Meter) BytesUp() int64 {
	return m.bytesUp.Load()
}

func (m *Meter) BytesDown() int64 {
	return m.bytesDown.Load()
}

func (m *Meter) BytesTotal() int64 {
	return m.bytesUp.Load() + m.bytesDown.Load()
}

// Snapshot returns current counters and resets them. Used for periodic reporting.
func (m *Meter) Snapshot() (up, down int64) {
	up = m.bytesUp.Swap(0)
	down = m.bytesDown.Swap(0)
	return
}
