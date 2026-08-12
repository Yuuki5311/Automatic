package scraper

import (
	"strings"
	"testing"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

// 选择器构建不依赖真实页面DOM，可在无Chrome环境单测验证。

func TestBuildGameTabSelector(t *testing.T) {
	b := &browserScraper{}
	game := config.GameConfig{Name: "原神", URL: "/workbench/recycle/genshin"}

	sel := b.buildGameTabSelector(game)
	// 必须是合法的CSS选择器列表（逗号分隔），chromedp ByQuery 可解析
	if !strings.Contains(sel, `[class*="原神"]`) {
		t.Errorf("selector missing class match: %s", sel)
	}
	if !strings.Contains(sel, `[href*="workbench/recycle/genshin"]`) {
		t.Errorf("selector missing href match: %s", sel)
	}

	// URL为空时只保留 class 匹配
	selNoURL := b.buildGameTabSelector(config.GameConfig{Name: "火影忍者"})
	if strings.Contains(selNoURL, "href") {
		t.Errorf("selector should not contain href when URL empty: %s", selNoURL)
	}
}

func TestBuildSubTabSelector(t *testing.T) {
	b := &browserScraper{}

	// 已知游戏按 tableIndex 命中子标签
	sel := b.buildSubTabSelector(config.GameConfig{Name: "原神"}, 0)
	if sel == "" || !strings.Contains(sel, "官服") {
		t.Errorf("index 0 should map to 官服, got: %q", sel)
	}
	sel1 := b.buildSubTabSelector(config.GameConfig{Name: "原神"}, 1)
	if sel1 == "" || !strings.Contains(sel1, "渠道服") {
		t.Errorf("index 1 should map to 渠道服, got: %q", sel1)
	}

	// 越界索引 → 空选择器（不点击子标签）
	if sel := b.buildSubTabSelector(config.GameConfig{Name: "原神"}, 2); sel != "" {
		t.Errorf("out-of-range index should return empty selector, got: %q", sel)
	}

	// 未知游戏 → 空选择器
	if sel := b.buildSubTabSelector(config.GameConfig{Name: "绝区零"}, 0); sel != "" {
		t.Errorf("unknown game should return empty selector, got: %q", sel)
	}
}
