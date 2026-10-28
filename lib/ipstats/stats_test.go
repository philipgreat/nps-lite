package ipstats

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRollingRanking(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 30, 30, 0, time.UTC)
	s := New("")
	s.Record("192.0.2.2:123", now.Add(-25*time.Hour))
	s.Record("192.0.2.1:123", now.Add(-time.Hour))
	s.Record("[::ffff:192.0.2.1]:456", now)
	s.Record("[2001:db8::1]:7", now)
	s.Record("192.0.2.3:8", now)
	s.Record("invalid", now)
	got := s.Snapshot(now, "")
	if len(got.Rows) != 3 || got.Connections != 4 || got.Rows[0].IP != "192.0.2.1" || got.Rows[0].Count != 2 {
		t.Fatalf("wrong ranking: %+v", got)
	}
	if !got.Rows[0].First.Equal(now.Add(-time.Hour)) || !got.Rows[0].Last.Equal(now) {
		t.Fatal("incorrect first/last")
	}
	if got.Rows[1].IP != "192.0.2.3" {
		t.Fatal("tie break order")
	}
	if got := s.Snapshot(now, "2001:DB8"); len(got.Rows) != 1 || got.Connections != 1 {
		t.Fatal("IPv6 search")
	}
	if got := s.Snapshot(now.Add(24*time.Hour+time.Minute), ""); len(got.Rows) != 0 {
		t.Fatal("expired entries retained")
	}
}
func TestMinuteBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute).Add(30 * time.Second)
	s := New("")
	s.Record("192.0.2.1:1", now.Add(-24*time.Hour-31*time.Second))
	s.Record("192.0.2.2:1", now.Add(-24*time.Hour-29*time.Second))
	got := s.Snapshot(now, "")
	if len(got.Rows) != 1 || got.Rows[0].IP != "192.0.2.2" {
		t.Fatal("minute retention boundary")
	}
}
func TestPersistenceConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "stats.json")
	s := New(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				s.Record("192.0.2.1:10", now)
				if j%100 == 0 {
					s.Snapshot(now, "")
					if err := s.Save(); err != nil {
						t.Error(err)
					}
				}
			}
		}()
	}
	wg.Wait()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := New(path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	got := loaded.Snapshot(now, "")
	if got.Connections != 8000 || !got.Started.Equal(s.state.Started) {
		t.Fatal("restart lost counts or collection start")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0600 {
		t.Fatal("statistics file permissions")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Load(); err == nil {
		t.Fatal("corrupt state was accepted")
	}
	// Expired history is removed from the persisted snapshot as well.
	s.Record("192.0.2.9:1", now.Add(-25*time.Hour))
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range state.Buckets {
		if _, ok := bucket["192.0.2.9"]; ok {
			t.Fatal("expired history saved")
		}
	}
}

type testConn struct{ net.Conn }

func (testConn) RemoteAddr() net.Addr { return &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 1234} }

type testListener struct {
	net.Listener
	conn   net.Conn
	closed bool
}

func (l *testListener) Accept() (net.Conn, error) {
	if l.closed {
		return nil, net.ErrClosed
	}
	return l.conn, nil
}
func (l *testListener) Close() error { l.closed = true; return nil }
func TestCountingListener(t *testing.T) {
	original := Default
	Default = New("")
	defer func() { Default = original }()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	l := Listener(&testListener{conn: testConn{a}})
	if _, err := l.Accept(); err != nil {
		t.Fatal(err)
	}
	if got := Default.Snapshot(time.Now(), ""); got.Connections != 1 {
		t.Fatal("accepted connection not counted exactly once")
	}
	l.Close()
	if _, err := l.Accept(); err == nil {
		t.Fatal("closed listener accepted")
	}
	if got := Default.Snapshot(time.Now(), ""); got.Connections != 1 {
		t.Fatal("failed accept counted")
	}
}
