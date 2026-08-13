// Package auth 提供登录相关的 Cookie 持久化与管理能力。
package auth

import (
	"bytes"
	"encoding/json"
	"os"
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
	if time.Now().After(data.ExpiresAt) {
		return false
	}
	// 至少需要有登录态的关键Cookie
	hasSessionCookie := false
	for _, c := range data.Cookies {
		if c.Name == "_m_h5_tk" || c.Name == "_m_h5_tk_enc" || c.Name == "token" || c.Name == "SESSION" || c.Name == "jym_token" {
			if c.Value != "" {
				hasSessionCookie = true
				break
			}
		}
	}
	return hasSessionCookie
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

func getDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
