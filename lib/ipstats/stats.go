// Package ipstats counts accepted public proxy TCP connections in minute buckets.
package ipstats

import (
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	IP    string    `json:"ip"`
	Count uint64    `json:"count"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}
type diskState struct {
	Started time.Time                  `json:"started"`
	Buckets map[int64]map[string]Entry `json:"buckets"`
}
type Snapshot struct {
	Rows        []Entry   `json:"rows"`
	Connections uint64    `json:"connections"`
	Started     time.Time `json:"started"`
	WindowStart time.Time `json:"window_start"`
	Updated     time.Time `json:"updated"`
}
type Store struct {
	mu     sync.Mutex
	saveMu sync.Mutex
	state  diskState
	path   string
}

var Default = New("")

func New(path string) *Store {
	return &Store{path: path, state: diskState{Started: time.Now().UTC(), Buckets: make(map[int64]map[string]Entry)}}
}
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state diskState
	if err = json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Buckets == nil || state.Started.IsZero() {
		return os.ErrInvalid
	}
	s.mu.Lock()
	s.state = state
	s.prune(time.Now())
	s.mu.Unlock()
	return nil
}
func (s *Store) prune(now time.Time) {
	cutoff := now.Add(-24*time.Hour).Unix() / 60
	for minute := range s.state.Buckets {
		if minute < cutoff {
			delete(s.state.Buckets, minute)
		}
	}
}
func (s *Store) Record(remote string, now time.Time) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return
	}
	key := ip.Unmap().WithZone("").String()
	now = now.UTC()
	minute := now.Unix() / 60
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.state.Buckets[minute]
	if bucket == nil {
		bucket = make(map[string]Entry)
		s.state.Buckets[minute] = bucket
	}
	row := bucket[key]
	row.IP = key
	row.Count++
	if row.First.IsZero() || now.Before(row.First) {
		row.First = now
	}
	if now.After(row.Last) {
		row.Last = now
	}
	bucket[key] = row
}
func (s *Store) Snapshot(now time.Time, search string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	result := Snapshot{Rows: []Entry{}, Started: s.state.Started, WindowStart: now.UTC().Add(-24 * time.Hour).Truncate(time.Minute), Updated: now.UTC()}
	rows := make(map[string]Entry)
	search = strings.ToLower(strings.TrimSpace(search))
	for _, bucket := range s.state.Buckets {
		for ip, row := range bucket {
			if !strings.Contains(strings.ToLower(ip), search) {
				continue
			}
			sum := rows[ip]
			sum.IP = ip
			sum.Count += row.Count
			if sum.First.IsZero() || row.First.Before(sum.First) {
				sum.First = row.First
			}
			if row.Last.After(sum.Last) {
				sum.Last = row.Last
			}
			rows[ip] = sum
		}
	}
	for _, row := range rows {
		result.Rows = append(result.Rows, row)
		result.Connections += row.Count
	}
	sort.Slice(result.Rows, func(i, j int) bool {
		if result.Rows[i].Count == result.Rows[j].Count {
			return result.Rows[i].IP < result.Rows[j].IP
		}
		return result.Rows[i].Count > result.Rows[j].Count
	})
	return result
}

// Save uses a private atomic snapshot; readers and accept loops never wait for disk I/O.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	s.prune(time.Now())
	data, err := json.Marshal(s.state)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

type listener struct{ net.Listener }

func (l listener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		Default.Record(c.RemoteAddr().String(), time.Now())
	}
	return c, err
}
func Listener(l net.Listener) net.Listener { return listener{l} }
