package meter

import (
	"sync"
	"testing"
)

func TestMeterBasic(t *testing.T) {
	m := New()

	m.AddUp(100)
	m.AddDown(200)

	if m.BytesUp() != 100 {
		t.Errorf("BytesUp = %d, want 100", m.BytesUp())
	}
	if m.BytesDown() != 200 {
		t.Errorf("BytesDown = %d, want 200", m.BytesDown())
	}
	if m.BytesTotal() != 300 {
		t.Errorf("BytesTotal = %d, want 300", m.BytesTotal())
	}
}

func TestMeterSnapshot(t *testing.T) {
	m := New()

	m.AddUp(100)
	m.AddDown(200)

	up, down := m.Snapshot()
	if up != 100 || down != 200 {
		t.Errorf("Snapshot = (%d, %d), want (100, 200)", up, down)
	}

	// After snapshot, counters should be reset
	if m.BytesUp() != 0 {
		t.Errorf("BytesUp after snapshot = %d, want 0", m.BytesUp())
	}
	if m.BytesDown() != 0 {
		t.Errorf("BytesDown after snapshot = %d, want 0", m.BytesDown())
	}
}

func TestMeterConcurrent(t *testing.T) {
	m := New()
	var wg sync.WaitGroup

	n := 1000
	wg.Add(n * 2)
	for range n {
		go func() {
			m.AddUp(1)
			wg.Done()
		}()
		go func() {
			m.AddDown(1)
			wg.Done()
		}()
	}
	wg.Wait()

	if m.BytesUp() != int64(n) {
		t.Errorf("concurrent BytesUp = %d, want %d", m.BytesUp(), n)
	}
	if m.BytesDown() != int64(n) {
		t.Errorf("concurrent BytesDown = %d, want %d", m.BytesDown(), n)
	}
}
