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
	ID           string    `json:"id"`
	Username     string    `json:"username"` // 展示用 mobile
	Password     string    `json:"password"`
	CookiePath   string    `json:"cookie_path"`
	Enabled      bool      `json:"enabled"`
	LastStatus   string    `json:"last_status"`
	LastError    string    `json:"last_error"`
	LastRunAt    time.Time `json:"last_run_at"`
	ExternalID   int       `json:"external_id,omitempty"` // leyoo 店铺 id
	SupplierID   int       `json:"supplier_id,omitempty"`
	ShopName     string    `json:"shop_name,omitempty"`
	PlatformKey  string    `json:"platform_key,omitempty"`
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

// PathFromCookie returns accounts.json beside the cookie file, or ./data/accounts.json.
func PathFromCookie(cookiePath string) string {
	cookiePath = strings.TrimSpace(cookiePath)
	if cookiePath == "" {
		return filepath.Join("data", "accounts.json")
	}
	return filepath.Join(filepath.Dir(cookiePath), "accounts.json")
}

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

// FindByExternalID 按 leyoo 店铺 id 查找账户。
func (s *Store) FindByExternalID(externalID int) (Account, bool) {
	if externalID <= 0 {
		return Account{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Accounts {
		if a.ExternalID == externalID {
			return a, true
		}
	}
	return Account{}, false
}

// FindByUsername 按用户名（手机号）精确查找。
func (s *Store) FindByUsername(username string) (Account, bool) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Account{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Accounts {
		if a.Username == username {
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

func (s *Store) SetEnabled(id string, enabled bool, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].Enabled = enabled
			if reason != "" {
				s.data.Accounts[i].LastError = reason
			}
			if !enabled {
				s.data.Accounts[i].LastStatus = "disabled"
			} else if s.data.Accounts[i].LastStatus == "disabled" {
				s.data.Accounts[i].LastStatus = "idle"
			}
			s.data.Accounts[i].LastRunAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("账户不存在")
}

// IsCNMobile 判断是否为 11 位纯数字手机号。
func IsCNMobile(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 11 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// AccountPhoneFromRemote 取账户手机号：优先已解密 third_account，其次 mobile；均须为 11 位。
func AccountPhoneFromRemote(mobile, thirdAccount string) string {
	if IsCNMobile(thirdAccount) {
		return strings.TrimSpace(thirdAccount)
	}
	if IsCNMobile(mobile) {
		return strings.TrimSpace(mobile)
	}
	return ""
}

// AccountKeyFromRemote 兼容旧调用；仅返回合法 11 位手机号，否则空字符串。
func AccountKeyFromRemote(mobile, thirdAccount string, externalID int) string {
	_ = externalID
	return AccountPhoneFromRemote(mobile, thirdAccount)
}

// UpsertFromRemote 按 mobile 更新或新增；已存在时保留 Enabled 状态。
func (s *Store) UpsertFromRemote(mobile, password, shopName string, externalID, supplierID int, platformKey string) (Account, bool, error) {
	return s.UpsertFromRemoteKey(AccountPhoneFromRemote(mobile, ""), password, shopName, externalID, supplierID, platformKey)
}

// UpsertFromRemoteKey 按手机号合并账户：相同手机号只保留一条；非 11 位用户名拒绝写入。
func (s *Store) UpsertFromRemoteKey(username, password, shopName string, externalID, supplierID int, platformKey string) (Account, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	username = strings.TrimSpace(username)
	if !IsCNMobile(username) {
		return Account{}, false, fmt.Errorf("账户手机号无效（须为11位数字）")
	}
	dir := filepath.Join(filepath.Dir(s.path), "cookies")

	// 1) 同手机号直接去重合并（含历史 phone_54 后缀残留）
	if idx := s.findPhoneIndexLocked(username); idx >= 0 {
		keepID := s.data.Accounts[idx].ID
		s.dropOtherPhoneLocked(username, keepID)
		if externalID > 0 {
			s.dropOtherExternalLocked(externalID, keepID)
		}
		idx = s.indexByIDLocked(keepID)
		return s.updateRemoteLocked(&s.data.Accounts[idx], username, password, shopName, externalID, supplierID, platformKey, dir)
	}

	// 2) 按 external_id 更新旧密文账户，并清掉同 id / 同号残留
	if externalID > 0 {
		keep := -1
		for i := range s.data.Accounts {
			if s.data.Accounts[i].ExternalID == externalID {
				keep = i
				break
			}
		}
		if keep >= 0 {
			keepID := s.data.Accounts[keep].ID
			s.dropOtherExternalLocked(externalID, keepID)
			s.dropOtherPhoneLocked(username, keepID)
			keep = s.indexByIDLocked(keepID)
			return s.updateRemoteLocked(&s.data.Accounts[keep], username, password, shopName, externalID, supplierID, platformKey, dir)
		}
	}

	a := Account{
		ID:          newID(),
		Username:    username,
		Password:    password,
		CookiePath:  filepath.Join(dir, SafeUsername(username)+".json"),
		Enabled:     true,
		LastStatus:  "idle",
		ExternalID:  externalID,
		SupplierID:  supplierID,
		ShopName:    shopName,
		PlatformKey: platformKey,
	}
	s.data.Accounts = append(s.data.Accounts, a)
	if err := s.saveLocked(); err != nil {
		return Account{}, false, err
	}
	return a, true, nil
}

func (s *Store) findPhoneIndexLocked(phone string) int {
	for i := range s.data.Accounts {
		u := s.data.Accounts[i].Username
		if u == phone || strings.HasPrefix(u, phone+"_") {
			return i
		}
	}
	return -1
}

func (s *Store) indexByIDLocked(id string) int {
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) dropOtherPhoneLocked(phone, keepID string) {
	out := s.data.Accounts[:0]
	for _, a := range s.data.Accounts {
		if a.ID != keepID && (a.Username == phone || strings.HasPrefix(a.Username, phone+"_")) {
			continue
		}
		out = append(out, a)
	}
	s.data.Accounts = out
}

func (s *Store) dropOtherExternalLocked(externalID int, keepID string) {
	out := s.data.Accounts[:0]
	for _, a := range s.data.Accounts {
		if a.ID != keepID && a.ExternalID == externalID {
			continue
		}
		out = append(out, a)
	}
	s.data.Accounts = out
}

func (s *Store) updateRemoteLocked(a *Account, username, password, shopName string, externalID, supplierID int, platformKey string, dir string) (Account, bool, error) {
	a.Username = username
	a.Password = password
	a.ShopName = shopName
	a.ExternalID = externalID
	a.SupplierID = supplierID
	a.PlatformKey = platformKey
	wantPath := filepath.Join(dir, SafeUsername(username)+".json")
	if a.CookiePath == "" || looksLikeHexCipherUsername(filepath.Base(a.CookiePath)) {
		a.CookiePath = wantPath
	}
	if err := s.saveLocked(); err != nil {
		return Account{}, false, err
	}
	return *a, false, nil
}

// PruneInvalidPhones 删除用户名不是 11 位手机号的账户（密文/shop_id 残留）。
func (s *Store) PruneInvalidPhones() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	out := s.data.Accounts[:0]
	for _, a := range s.data.Accounts {
		if IsCNMobile(a.Username) {
			out = append(out, a)
			continue
		}
		n++
	}
	s.data.Accounts = out
	if n == 0 {
		return 0, nil
	}
	return n, s.saveLocked()
}

func looksLikeHexCipherUsername(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 32 || len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
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
