package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// Navigate 导航到 URL 并等待加载（兼容重定向，避免 WaitLoad 竞态报错）。
func Navigate(ctx context.Context, url string) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	wait := page.WaitNavigation(proto.PageLifecycleEventNameNetworkAlmostIdle)
	if err := page.Navigate(url); err != nil {
		// 快速跳转时 CDP 偶发 -32000，后续 wait 仍可等到最终页
		if !isNavRaceErr(err) {
			return err
		}
	}
	wait()
	// 再等一轮 load，失败可忽略（SPA/二次跳转）
	_ = page.WaitLoad()
	return nil
}

func isNavRaceErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "navigated or closed") || strings.Contains(s, "-32000")
}

// WaitReady 等待 document.readyState == complete。
func WaitReady(ctx context.Context) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	return page.WaitLoad()
}

// WaitVisible 等待选择器可见。
func WaitVisible(ctx context.Context, selector string) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = page.Timeout(30 * time.Second).Element(selector)
	return err
}

// Click 点击元素。
func Click(ctx context.Context, selector string) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	el, err := page.Element(selector)
	if err != nil {
		return err
	}
	return el.Click(proto.InputMouseButtonLeft, 1)
}

// Input 清空并输入文本。
func Input(ctx context.Context, selector, text string) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	el, err := page.Element(selector)
	if err != nil {
		return err
	}
	if err := el.SelectAllText(); err != nil {
		_ = el.Click(proto.InputMouseButtonLeft, 1)
	}
	return el.Input(text)
}

// Eval 执行 JS 表达式（支持 IIFE），结果写入 dest（可为 nil）。
func Eval(ctx context.Context, js string, dest any) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	res, err := proto.RuntimeEvaluate{
		Expression:    js,
		ReturnByValue: true,
	}.Call(page)
	if err != nil {
		return err
	}
	if dest == nil || res == nil || res.Result == nil {
		return nil
	}
	return assignRemoteObject(res.Result, dest)
}

func assignRemoteObject(obj *proto.RuntimeRemoteObject, dest any) error {
	if obj == nil {
		return nil
	}
	// undefined / null 时 Type 为 object 且无 Value 可用
	if obj.Type == proto.RuntimeRemoteObjectTypeUndefined || obj.Type == proto.RuntimeRemoteObjectTypeObject && obj.Subtype == proto.RuntimeRemoteObjectSubtypeNull {
		return nil
	}
	return assignGson(obj.Value, dest)
}

func assignGson(v gson.JSON, dest any) error {
	switch d := dest.(type) {
	case *string:
		*d = v.Str()
		return nil
	case *bool:
		*d = v.Bool()
		return nil
	case *int:
		*d = v.Int()
		return nil
	case *float64:
		*d = v.Num()
		return nil
	default:
		return v.Unmarshal(dest)
	}
}

// Location 返回当前 URL。
func Location(ctx context.Context) (string, error) {
	var href string
	if err := Eval(ctx, `window.location.href`, &href); err != nil {
		return "", err
	}
	return href, nil
}

// Sleep 等待。
func Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// ExtractHTML 提取 outerHTML。
func ExtractHTML(ctx context.Context, selector string) (string, error) {
	page, err := PageFromContext(ctx)
	if err != nil {
		return "", err
	}
	el, err := page.Element(selector)
	if err != nil {
		return "", err
	}
	return el.HTML()
}

// ExtractText 提取文本。
func ExtractText(ctx context.Context, selector string) (string, error) {
	page, err := PageFromContext(ctx)
	if err != nil {
		return "", err
	}
	el, err := page.Element(selector)
	if err != nil {
		return "", err
	}
	return el.Text()
}

// Screenshot 全页截图写文件。
func Screenshot(ctx context.Context, path string) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	bin, err := page.Screenshot(true, nil)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, bin, 0o600)
}

// ScreenshotSelector 元素截图。
func ScreenshotSelector(ctx context.Context, selector string) ([]byte, error) {
	page, err := PageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	el, err := page.Element(selector)
	if err != nil {
		return nil, err
	}
	return el.Screenshot(proto.PageCaptureScreenshotFormatPng, 0)
}

// HasElement 判断选择器是否存在至少一个节点。
func HasElement(ctx context.Context, selector string) (bool, error) {
	page, err := PageFromContext(ctx)
	if err != nil {
		return false, err
	}
	els, err := page.Elements(selector)
	if err != nil {
		return false, err
	}
	return len(els) > 0, nil
}

// ScrollIntoView 滚动到元素。
func ScrollIntoView(ctx context.Context, selector string) error {
	expr := fmt.Sprintf(
		`(() => { const el = document.querySelector(%s); if (el) el.scrollIntoView({behavior:'instant',block:'center'}); })()`,
		strconv.Quote(selector),
	)
	return Eval(ctx, expr, nil)
}

// WaitForNetworkIdle 粗略等待网络空闲。
func WaitForNetworkIdle(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		var pending int
		_ = Eval(ctx, `performance.getEntriesByType('resource').filter(r => !r.responseEnd).length`, &pending)
		if pending == 0 {
			return nil
		}
		if err := Sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

// SetCookies 注入 Cookie（需已有页面；会先导航 about:blank 若尚无文档）。
func SetCookies(ctx context.Context, cookies []models.CookieEntry) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	params := make([]*proto.NetworkCookieParam, 0, len(cookies))
	for _, c := range cookies {
		p := &proto.NetworkCookieParam{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
		}
		if c.Expires > 0 {
			p.Expires = proto.TimeSinceEpoch(c.Expires)
		}
		params = append(params, p)
	}
	return page.SetCookies(params)
}

// GetCookies 读取与 urls 相关的 Cookie。
func GetCookies(ctx context.Context, urls []string) ([]models.CookieEntry, error) {
	page, err := PageFromContext(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := page.Cookies(urls)
	if err != nil {
		return nil, err
	}
	out := make([]models.CookieEntry, 0, len(raw))
	for _, c := range raw {
		if c == nil {
			continue
		}
		out = append(out, models.CookieEntry{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  float64(c.Expires),
			HTTPOnly: c.HTTPOnly,
			Secure:   c.Secure,
		})
	}
	return out, nil
}

// MouseMove / MouseDown / MouseUp 视口坐标鼠标操作（验证码拖拽用）。
func MouseMove(ctx context.Context, x, y float64) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	return page.Mouse.MoveTo(proto.Point{X: x, Y: y})
}

func MouseDown(ctx context.Context) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	return page.Mouse.Down(proto.InputMouseButtonLeft, 1)
}

func MouseUp(ctx context.Context) error {
	page, err := PageFromContext(ctx)
	if err != nil {
		return err
	}
	return page.Mouse.Up(proto.InputMouseButtonLeft, 1)
}

// MustPage 仅供测试辅助；生产请用 PageFromContext。
func MustPage(ctx context.Context) *rod.Page {
	p, err := PageFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return p
}
