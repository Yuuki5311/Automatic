// Package auth 提供登录相关的 Cookie 持久化与管理能力。
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// LoadCookies 从JSON文件加载Cookie
func LoadCookies(path string) (*models.CookieData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &models.CookieData{}, nil
		}
		return nil, err
	}
	var cookies models.CookieData
	if err := json.Unmarshal(data, &cookies); err != nil {
		return nil, err
	}
	return &cookies, nil
}

// SaveCookies 将Cookie保存到JSON文件
func SaveCookies(path string, data *models.CookieData) error {
	data.UpdatedAt = time.Now()
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	// 确保目录存在
	if err := os.MkdirAll(getDir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, bytes, 0600)
}

// IsCookieValid 检查Cookie是否仍在有效期内
func IsCookieValid(data *models.CookieData) bool {
	if data == nil || len(data.Cookies) == 0 {
		return false
	}
	if !data.ExpiresAt.IsZero() && time.Now().After(data.ExpiresAt) {
		return false
	}
	// 至少需要有登录态的关键Cookie（含交易猫会员会话）
	sessionNames := map[string]struct{}{
		"_m_h5_tk": {}, "_m_h5_tk_enc": {}, "token": {}, "SESSION": {}, "jym_token": {},
		"ieu_member_uid": {}, "ieu_member_token": {}, "jym_session_id": {}, "jym_session": {},
	}
	for _, c := range data.Cookies {
		if _, ok := sessionNames[c.Name]; ok && c.Value != "" {
			return true
		}
		if strings.HasPrefix(c.Name, "ieu_member_") && c.Value != "" {
			return true
		}
	}
	return false
}

// ImportFromHeader 将 Cookie 请求头风格字符串（name=value; name2=value2）保存为本地 Cookie 文件。
func ImportFromHeader(path, header string, domain string) error {
	header = strings.TrimSpace(header)
	if header == "" {
		return fmt.Errorf("cookie 为空")
	}
	if domain == "" {
		domain = ".jiaoyimao.com"
	}
	parts := strings.Split(header, ";")
	entries := make([]models.CookieEntry, 0, len(parts))
	expires := float64(time.Now().Add(30 * 24 * time.Hour).Unix())
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, val, ok := strings.Cut(p, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		entries = append(entries, models.CookieEntry{
			Name:    name,
			Value:   val,
			Domain:  domain,
			Path:    "/",
			Expires: expires,
			Secure:  true,
		})
	}
	if len(entries) == 0 {
		return fmt.Errorf("未能解析任何 cookie")
	}
	data := &models.CookieData{
		Cookies:   entries,
		ExpiresAt: time.Unix(int64(expires), 0),
	}
	return SaveCookies(path, data)
}

// ImportFromJSON 从浏览器导出的Cookie JSON（如EditThisCookie格式）导入
func ImportFromJSON(path string, browserJSON []byte) error {
	browserJSON = bytes.TrimPrefix(browserJSON, []byte("\xef\xbb\xbf"))
	var rawCookies []struct {
		Name     string  `json:"name"`
		Value    string  `json:"value"`
		Domain   string  `json:"domain"`
		Path     string  `json:"path"`
		Expires  float64 `json:"expirationDate"`
		HTTPOnly bool    `json:"httpOnly"`
		Secure   bool    `json:"secure"`
	}
	if err := json.Unmarshal(browserJSON, &rawCookies); err != nil {
		return err
	}

	data := &models.CookieData{
		Cookies: make([]models.CookieEntry, len(rawCookies)),
	}
	var maxExpiry float64
	for i, c := range rawCookies {
		data.Cookies[i] = models.CookieEntry{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
		}
		if c.Expires > maxExpiry {
			maxExpiry = c.Expires
		}
	}
	data.ExpiresAt = time.Unix(int64(maxExpiry), 0)

	return SaveCookies(path, data)
}

// MemberUID 从 Cookie 中读取交易猫会员 UID（ieu_member_uid）。
func MemberUID(data *models.CookieData) string {
	if data == nil {
		return ""
	}
	for _, c := range data.Cookies {
		if c.Name == "ieu_member_uid" && strings.TrimSpace(c.Value) != "" {
			return strings.TrimSpace(c.Value)
		}
	}
	return ""
}

func getDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
