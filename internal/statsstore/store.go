package statsstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func safeFile(u string) string {
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

func Save(dir string, snap models.BoardStatsSnapshot) (string, error) {
	if snap.Date == "" {
		return "", fmt.Errorf("snapshot date empty")
	}
	account := snap.Account
	if account == "" {
		account = "_default"
	}
	sub := filepath.Join(dir, snap.Date)
	if err := os.MkdirAll(sub, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(sub, safeFile(account)+".json")
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func Load(dir, date, account string) (*models.BoardStatsSnapshot, error) {
	if account == "" {
		account = "_default"
	}
	b, err := os.ReadFile(filepath.Join(dir, date, safeFile(account)+".json"))
	if err != nil {
		return nil, err
	}
	var snap models.BoardStatsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
