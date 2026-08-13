package accounts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

type Account struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Password   string    `json:"password"`
	CookiePath string    `json:"cookie_path"`
	Enabled    bool      `json:"enabled"`
	LastStatus string    `json:"last_status"`
	LastError  string    `json:"last_error"`
	LastRunAt  time.Time `json:"last_run_at"`
}

type fileData struct {
	Accounts []Account `json:"accounts"`
}

type Store struct {
	path string
	mu   sync.Mutex
	data fileData
}

func NewStore(path string) *Store { return &Store{path: path} }

func SafeUsername(u string) string {
	var b strings.Builder
	for _, r := range u {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		s = "account"
	}
	return s
}

func newID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.data = fileData{}
			return nil
		}
		return err
	}
	var d fileData
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	s.data = d
	return nil
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0600)
}

func (s *Store) List() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, len(s.data.Accounts))
	copy(out, s.data.Accounts)
	return out
}

func (s *Store) Get(id string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return Account{}, false
}

func (s *Store) Add(username, password string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	username = strings.TrimSpace(username)
	if username == "" {
		return Account{}, fmt.Errorf("账号不能为空")
	}
	for _, a := range s.data.Accounts {
		if a.Username == username {
			return Account{}, fmt.Errorf("账号已存在: %s", username)
		}
	}
	dir := filepath.Join(filepath.Dir(s.path), "cookies")
	a := Account{
		ID:         newID(),
		Username:   username,
		Password:   password,
		CookiePath: filepath.Join(dir, SafeUsername(username)+".json"),
		Enabled:    true,
		LastStatus: "idle",
	}
	s.data.Accounts = append(s.data.Accounts, a)
	if err := s.saveLocked(); err != nil {
		return Account{}, err
	}
	return a, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	var cookie string
	for i, a := range s.data.Accounts {
		if a.ID == id {
			idx, cookie = i, a.CookiePath
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("账户不存在")
	}
	s.data.Accounts = append(s.data.Accounts[:idx], s.data.Accounts[idx+1:]...)
	if err := s.saveLocked(); err != nil {
		return err
	}
	if cookie != "" {
		_ = os.Remove(cookie)
	}
	return nil
}

func (s *Store) UpdateStatus(id, status, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].LastStatus = status
			s.data.Accounts[i].LastError = errMsg
			s.data.Accounts[i].LastRunAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("账户不存在")
}

func (s *Store) Enabled() []Account {
	all := s.List()
	var out []Account
	for _, a := range all {
		if a.Enabled {
			out = append(out, a)
		}
	}
	return out
}

func (s *Store) MigrateFromConfig(jym config.JYMConfig) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Accounts) > 0 || strings.TrimSpace(jym.Username) == "" {
		return false, nil
	}
	cookie := jym.CookiePath
	if cookie == "" {
		cookie = filepath.Join(filepath.Dir(s.path), "cookies", SafeUsername(jym.Username)+".json")
	}
	a := Account{
		ID: newID(), Username: jym.Username, Password: jym.Password,
		CookiePath: cookie, Enabled: true, LastStatus: "idle",
	}
	s.data.Accounts = append(s.data.Accounts, a)
	if err := s.saveLocked(); err != nil {
		return false, err
	}
	return true, nil
}
