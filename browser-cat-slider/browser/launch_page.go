package browser

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// pickLaunchPage 优先复用 AppMode 启动后已有页面，避免额外空白标签。
func pickLaunchPage(browserClient *rod.Browser, targetURL string) (*rod.Page, error) {
	if browserClient == nil {
		return nil, fmt.Errorf("browser is not connected")
	}
	normalizedTargetURL := strings.TrimSpace(targetURL)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pages, err := browserClient.Pages()
		if err == nil && len(pages) > 0 {
			if normalizedTargetURL == "" {
				return pages[0], nil
			}
			for _, page := range pages {
				info, infoErr := page.Info()
				if infoErr != nil {
					continue
				}
				if strings.TrimSpace(info.URL) == normalizedTargetURL {
					return page, nil
				}
			}
			return pages[0], nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return browserClient.Page(proto.TargetCreateTarget{URL: targetURL})
}

// closeExtraLaunchPages 关闭会话恢复出来的多余标签，只保留主页面。
func closeExtraLaunchPages(browserClient *rod.Browser, keep *rod.Page) {
	if browserClient == nil || keep == nil {
		return
	}
	pages, err := browserClient.Pages()
	if err != nil || len(pages) <= 1 {
		return
	}
	keepID := ""
	if info, err := keep.Info(); err == nil && info != nil {
		keepID = string(info.TargetID)
	}
	for _, page := range pages {
		if page == nil {
			continue
		}
		if keepID != "" {
			info, err := page.Info()
			if err == nil && info != nil && string(info.TargetID) == keepID {
				continue
			}
		} else if page == keep {
			continue
		}
		_ = page.Close()
	}
}

func ensureLaunchPageURL(page *rod.Page, targetURL string) error {
	normalized := strings.TrimSpace(targetURL)
	if page == nil || normalized == "" {
		return nil
	}
	info, err := page.Info()
	if err != nil {
		return err
	}
	if strings.TrimSpace(info.URL) == normalized {
		return nil
	}
	return page.Navigate(normalized)
}
