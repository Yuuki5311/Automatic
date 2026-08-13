package browser

import (
	"testing"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

func TestPageHelpersRequireSession(t *testing.T) {
	ctx := t.Context()
	if err := Navigate(ctx, "https://example.com"); err == nil {
		t.Error("Navigate should fail without session")
	}
	if err := WaitReady(ctx); err == nil {
		t.Error("WaitReady should fail without session")
	}
	if err := Click(ctx, "#x"); err == nil {
		t.Error("Click should fail without session")
	}
	if err := Input(ctx, "#x", "a"); err == nil {
		t.Error("Input should fail without session")
	}
	if err := Eval(ctx, "1", nil); err == nil {
		t.Error("Eval should fail without session")
	}
	if err := SetCookies(ctx, []models.CookieEntry{{Name: "a", Value: "b"}}); err == nil {
		t.Error("SetCookies should fail without session")
	}
	if err := Sleep(ctx, time.Millisecond); err != nil {
		t.Errorf("Sleep on plain context should still work: %v", err)
	}
}
