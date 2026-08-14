package catlogin

import (
	"strings"
	"time"

	"reference/browser-cat-slider/browser"
	"reference/browser-cat-slider/catslider"
)

// shouldStayOnCurrentLoginFlowPage 若当前已在登录/风控流程页，则不应再打开工作台，
// 避免反复跳转加重风控或打断滑块验证。
func shouldStayOnCurrentLoginFlowPage(sess *browser.Session) bool {
	if sess == nil {
		return false
	}
	currentURL := currentURL(sess)
	if catslider.LooksLikePunishURL(currentURL) {
		return true
	}
	if hasPasswordLoginForm(sess) {
		return true
	}
	if looksLikeLoginURL(currentURL) {
		return true
	}
	if strings.TrimSpace(readNickname(sess)) != "" {
		return true
	}
	return false
}

// isMemberLoginFlowPage 是否已在 member 登录页（排除 punish 整页风控）。
func isMemberLoginFlowPage(sess *browser.Session) bool {
	if sess == nil {
		return false
	}
	currentURL := currentURL(sess)
	if catslider.LooksLikePunishURL(currentURL) {
		return false
	}
	return hasPasswordLoginForm(sess) || looksLikeLoginURL(currentURL)
}

// OpenWorkBench 导航到 merchant 工作台；允许重定向到登录页，不在此处反复刷新。
func OpenWorkBench(sess *browser.Session) error {
	if sess == nil {
		return nil
	}
	sess.LogAction("交易猫：打开工作台 %s", MerchantWorkBenchURL)
	if err := sess.OpenWithTimeout(MerchantWorkBenchURL, browser.DefaultPageLoadTimeout); err != nil {
		// rod 在页面跳转时可能报 target navigated，属于可忽略的竞争态。
		if browser.IsNavigationRaceError(err) {
			sess.LogAction("交易猫：工作台跳转时目标切换，继续等待页面状态 err=%v", err)
			return nil
		}
		return err
	}
	sess.LogAction("交易猫：工作台已打开 current_url=%s", currentURL(sess))
	return nil
}

// OpenWorkBenchIfNeeded 仅在尚未进入登录流程时才打开工作台。
func OpenWorkBenchIfNeeded(sess *browser.Session) error {
	if shouldStayOnCurrentLoginFlowPage(sess) {
		sess.LogAction("交易猫：已在登录流程页，跳过重复打开工作台")
		return nil
	}
	return OpenWorkBench(sess)
}

func currentURL(sess *browser.Session) string {
	if sess == nil {
		return ""
	}
	u, err := sess.CurrentURL()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u)
}

// looksLikeLoginURL 当前 URL 是否像登录页（排除 punish 风控页）。
func looksLikeLoginURL(rawURL string) bool {
	normalized := strings.ToLower(strings.TrimSpace(rawURL))
	if catslider.LooksLikePunishURL(normalized) {
		return false
	}
	return normalized != "" && strings.Contains(normalized, "login")
}

// readNickname 读取工作台 span.nickname；非空表示已登录。
func readNickname(sess *browser.Session) string {
	if sess == nil {
		return ""
	}
	err, result := sess.JavaScript(`return String((function () {
		const el = document.querySelector('span.nickname');
		return String(el?.textContent || '').trim();
	})())`)
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(result)
	if trimmed == "" || trimmed == "undefined" || trimmed == "null" {
		return ""
	}
	return trimmed
}

// hasPasswordLoginForm 页面 HTML 是否包含密码登录表单关键字。
func hasPasswordLoginForm(sess *browser.Session) bool {
	if sess == nil {
		return false
	}
	html, err := sess.HTML()
	if err != nil {
		return false
	}
	return strings.Contains(html, "请输入手机号") ||
		strings.Contains(html, "请输入密码") ||
		strings.Contains(html, "密码登录")
}

// waitPageStable 打开工作台后等待 DOM 稳定（避免在页面未渲染完时误判登录状态）。
func waitPageStable(sess *browser.Session) {
	if sess == nil {
		return
	}
	_ = sess.WaitStable(1200 * time.Millisecond)
}
