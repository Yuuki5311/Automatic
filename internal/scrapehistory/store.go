package scrapehistory

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

const Retention = 30 * 24 * time.Hour
const maxErrorRunes = 200

// Entry 单次账号抓取操作记录。
type Entry struct {
	ID      string    `json:"id"`
	At      time.Time `json:"at"`
	Account string    `json:"account"`
	Status  string    `json:"status"` // ok | skipped | error
	Error   string    `json:"error,omitempty"`
}

type fileData struct {
	Entries []Entry `json:"entries"`
}

// Store 持久化抓取操作历史。
type Store struct {
	path string
	mu   sync.Mutex
	data fileData
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// PathFromAccounts 返回与 accounts.json 同目录的 scrape_history.json。
func PathFromAccounts(accountsPath string) string {
	dir := filepath.Dir(accountsPath)
	if dir == "" || dir == "." {
		dir = "data"
	}
	return filepath.Join(dir, "scrape_history.json")
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.data = fileData{}
			return nil
		}
		return err
	}
	var fd fileData
	if err := json.Unmarshal(raw, &fd); err != nil {
		s.data = fileData{}
		return nil
	}
	s.data = fd
	s.pruneLocked(time.Now())
	return nil
}

// Append 追加一条记录并裁剪超过 Retention 的条目。
func (s *Store) Append(account, status, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Entries = append(s.data.Entries, Entry{
		ID:      newID(),
		At:      time.Now(),
		Account: account,
		Status:  status,
		Error:   truncateRunes(errMsg, maxErrorRunes),
	})
	s.pruneLocked(time.Now())
	return s.saveLocked()
}

// List 返回按时间降序的副本。
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.data.Entries))
	copy(out, s.data.Entries)
	sort.Slice(out, func(i, j int) bool {
		return out[i].At.After(out[j].At)
	})
	return out
}

func (s *Store) pruneLocked(now time.Time) {
	cut := now.Add(-Retention)
	kept := s.data.Entries[:0]
	for _, e := range s.data.Entries {
		if e.At.After(cut) || e.At.Equal(cut) {
			kept = append(kept, e)
		}
	}
	s.data.Entries = kept
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0600)
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
