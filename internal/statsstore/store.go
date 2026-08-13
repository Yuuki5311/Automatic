package statsstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func Save(dir string, snap models.BoardStatsSnapshot) (string, error) {
	if snap.Date == "" {
		return "", fmt.Errorf("snapshot date empty")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, snap.Date+".json")
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func Load(dir, date string) (*models.BoardStatsSnapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, date+".json"))
	if err != nil {
		return nil, err
	}
	var snap models.BoardStatsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
