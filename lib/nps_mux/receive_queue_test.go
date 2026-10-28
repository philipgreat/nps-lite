package nps_mux

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReceiveQueueConcurrentDrain(t *testing.T) {
	q := newReceiveWindowQueue()
	const total = 20000
	var seen [total]atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				e, err := q.Pop()
				if err == io.EOF {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				id := int(e.Buf[0]) | int(e.Buf[1])<<8
				seen[id].Add(1)
			}
		}()
	}
	for i := 0; i < total; i++ {
		q.Push(&listElement{Buf: []byte{byte(i), byte(i >> 8)}, L: 2})
		// CloseWindow drains through TryPop while another goroutine reads Pop.
		if i%3 == 0 {
			if e := q.TryPop(); e != nil {
				id := int(e.Buf[0]) | int(e.Buf[1])<<8
				seen[id].Add(1)
			}
		}
	}
	q.Stop()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("readers failed to stop")
	}
	if q.Len() != 0 {
		t.Fatalf("stale byte count: %d", q.Len())
	}
	for i := range seen {
		if n := seen[i].Load(); n != 1 {
			t.Fatalf("element %d received %d times", i, n)
		}
	}
}

func TestReceiveQueueStopWakesAllReaders(t *testing.T) {
	q := newReceiveWindowQueue()
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := q.Pop(); done <- err }()
	}
	q.Stop()
	q.Stop() // Closing is idempotent, including concurrent close paths.
	for i := 0; i < 8; i++ {
		select {
		case err := <-done:
			if err != io.EOF {
				t.Fatalf("got %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked reader after Stop")
		}
	}
}

func TestReceiveQueueDeadlineUpdate(t *testing.T) {
	q := newReceiveWindowQueue()
	done := make(chan error, 1)
	go func() { _, err := q.Pop(); done <- err }()
	q.SetTimeOut(time.Now().Add(20 * time.Millisecond))
	select {
	case err := <-done:
		if err == nil || err == io.EOF {
			t.Fatalf("expected timeout, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not wake reader")
	}
	q.SetTimeOut(time.Now().Add(-time.Second))
	if _, err := q.Pop(); err == nil {
		t.Fatal("expired deadline should fail immediately")
	}
	q.SetTimeOut(time.Time{})
	q.Push(&listElement{Buf: []byte{1}, L: 1})
	if e, err := q.Pop(); err != nil || e.L != 1 {
		t.Fatal("reset deadline prevented reading")
	}
}
