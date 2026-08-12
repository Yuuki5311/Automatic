package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// Navigate 导航到指定URL并等待页面加载。
func Navigate(url string) chromedp.Action {
	return chromedp.Navigate(url)
}

// WaitVisible 等待元素在DOM中可见。
func WaitVisible(selector string, opts ...chromedp.QueryOption) chromedp.Action {
	return chromedp.WaitVisible(selector, opts...)
}

// WaitNotVisible 等待元素从DOM中消失。
func WaitNotVisible(selector string, opts ...chromedp.QueryOption) chromedp.Action {
	return chromedp.WaitNotVisible(selector, opts...)
}

// WaitReady 等待文档就绪状态为complete。
func WaitReady() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var state string
		if err := chromedp.Evaluate(`document.readyState`, &state).Do(ctx); err != nil {
			return err
		}
		if state != "complete" {
			return fmt.Errorf("页面未完全加载，当前状态: %s", state)
		}
		return nil
	})
}

// Click 点击元素（在headless模式下执行，不抢占用户鼠标）。
func Click(selector string) chromedp.Action {
	return chromedp.Click(selector, chromedp.ByQuery)
}

// Input 在输入框中输入文本。
func Input(selector, text string) chromedp.Action {
	return chromedp.SendKeys(selector, text, chromedp.ByQuery)
}

// ExtractHTML 提取元素的outerHTML。
func ExtractHTML(selector string, result *string) chromedp.Action {
	return chromedp.OuterHTML(selector, result, chromedp.ByQuery)
}

// ExtractText 提取元素的文本内容。
func ExtractText(selector string, result *string) chromedp.Action {
	return chromedp.TextContent(selector, result, chromedp.ByQuery)
}

// Screenshot 全页面截图保存到文件（调试用）。
func Screenshot(path string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var buf []byte
		if err := chromedp.FullScreenshot(&buf, 90).Do(ctx); err != nil {
			return err
		}
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		return os.WriteFile(path, buf, 0o600)
	})
}

// ScreenshotBytes 截图返回字节数据（用于验证码识别）。
func ScreenshotBytes(selector string, result *[]byte) chromedp.Action {
	return chromedp.Screenshot(selector, result, chromedp.ByQuery)
}

// Sleep 等待指定时间。
func Sleep(d time.Duration) chromedp.Action {
	return chromedp.Sleep(d)
}

// buildCookieParams 将配置中的Cookie转换为CDP协议参数。
// CookieEntry.Expires 为 0 时生成会话级 Cookie。
func buildCookieParams(c models.CookieEntry) *network.SetCookieParams {
	params := network.SetCookie(c.Name, c.Value).
		WithDomain(c.Domain).
		WithPath(c.Path).
		WithHTTPOnly(c.HTTPOnly).
		WithSecure(c.Secure)
	if c.Expires > 0 {
		expiry := cdp.TimeSinceEpoch(time.Unix(int64(c.Expires), 0))
		params = params.WithExpires(&expiry)
	}
	return params
}

// SetCookies 通过CDP协议设置浏览器Cookie。
//
// 与 document.cookie 注入方式不同，CDP 的 Network.SetCookie 支持
// HTTPOnly 与 Secure Cookie，与持久化的登录态 Cookie 完整兼容。
func SetCookies(cookies []models.CookieEntry) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for _, c := range cookies {
			if err := buildCookieParams(c).Do(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}

// ScrollIntoView 滚动到指定元素。
func ScrollIntoView(selector string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var res interface{}
		// 通过 strconv.Quote 转义选择器，避免注入非法JS语法
		expr := fmt.Sprintf(
			`document.querySelector(%s).scrollIntoView({behavior: 'instant', block: 'center'})`,
			strconv.Quote(selector),
		)
		return chromedp.Evaluate(expr, &res).Do(ctx)
	})
}

// WaitForNetworkIdle 等待网络空闲（无活跃请求）。
func WaitForNetworkIdle(timeout time.Duration) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			var pending int
			_ = chromedp.Evaluate(
				`performance.getEntriesByType('resource').filter(r => !r.responseEnd).length`,
				&pending,
			).Do(ctx)
			if pending == 0 {
				return nil
			}
			time.Sleep(500 * time.Millisecond)
		}
		return nil
	})
}
