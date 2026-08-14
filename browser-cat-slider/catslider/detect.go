package catslider

// detect.go：交易猫滑块 DOM 检测 JavaScript 与 punish URL 判断。
//
// 检测脚本在页面内执行，返回 JSON 或模式字符串，不依赖后端接口。

import (
	"fmt"
	"strings"
	"time"

	"reference/browser-cat-slider/browser"
)

// 交易猫滑块检测脚本：兼容弹窗 #baxia-dialog-content 与 punish 页直出 .nc_scale/.btn_slide。
const sliderRectJS = `return String((function () {
	const isVisible = (el) => {
		if (!el) return false;
		const style = window.getComputedStyle(el);
		if (!style || style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0) {
			return false;
		}
		const rect = el.getBoundingClientRect();
		return rect.width > 0 && rect.height > 0;
	};
	const dialog = document.querySelector("#baxia-dialog-content");
	if (isVisible(dialog)) {
		const rect = dialog.getBoundingClientRect();
		return JSON.stringify({
			x: Math.round(rect.x),
			y: Math.round(rect.y),
			width: Math.round(rect.width),
			height: Math.round(rect.height)
		});
	}
	const handle = document.querySelector(".btn_slide, .nc_iconfont.btn_slide, [class*='btn_slide'], span.nc_iconfont, [id*='n1z']");
	const track = document.querySelector(".nc_scale, [class*='nc_scale'], .nc_wrapper, [id*='n1t']");
	if (!isVisible(handle) || !isVisible(track)) {
		return "";
	}
	const hr = handle.getBoundingClientRect();
	const tr = track.getBoundingClientRect();
	const x = Math.min(hr.x, tr.x);
	const y = Math.min(hr.y, tr.y);
	const right = Math.max(hr.right, tr.right);
	const bottom = Math.max(hr.bottom, tr.bottom);
	return JSON.stringify({
		x: Math.round(x),
		y: Math.round(y),
		width: Math.round(right - x),
		height: Math.round(bottom - y)
	});
})() || "")`

const sliderProbeJS = `return String((function () {
	const isVisible = (el) => {
		if (!el) return false;
		const style = window.getComputedStyle(el);
		if (!style || style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0) {
			return false;
		}
		const rect = el.getBoundingClientRect();
		return rect.width > 0 && rect.height > 0;
	};
	const root = document.querySelector("#baxia-dialog-content") || document.body;
	const text = String(root.innerText || "").replace(/\s+/g, " ").trim();
	let failureText = "";
	if (text.includes("验证失败")) {
		const match = text.match(/验证失败[^]*?(?=点我反馈|$)/);
		failureText = match ? match[0].trim() : "验证失败";
	}
	const handle = document.querySelector(".btn_slide, .nc_iconfont.btn_slide, [class*='btn_slide'], span.nc_iconfont, [id*='n1z']");
	const track = document.querySelector(".nc_scale, [class*='nc_scale'], .nc_wrapper, [id*='n1t']");
	const hasCanvas = !!document.querySelector("canvas");
	const hasQR = !!document.querySelector("img[src*='qr'], img[src*='QR'], .qrcode, [class*='qrcode']");
	const escalated = !!failureText ||
		/error[:：][A-Za-z0-9]+/i.test(text) ||
		((hasCanvas || hasQR) && !isVisible(handle));
	let payload = {
		failureText: failureText,
		escalated: escalated,
		hasHandle: isVisible(handle),
		mode: "fallback",
		startX: 0,
		startY: 0,
		distance: 0
	};
	if (isVisible(handle) && isVisible(track)) {
		const handleRect = handle.getBoundingClientRect();
		const trackRect = track.getBoundingClientRect();
		if (handleRect.width > 0 && handleRect.height > 0 && trackRect.width > 0) {
			payload.mode = "dynamic";
			payload.startX = handleRect.x + handleRect.width / 2;
			payload.startY = handleRect.y + handleRect.height / 2;
			payload.distance = Math.max(0, trackRect.right - handleRect.right - 4);
		}
	}
	return JSON.stringify(payload);
})() || "")`

const sliderModeJS = `return String((function () {
	const isVisible = (el) => {
		if (!el) return false;
		const style = window.getComputedStyle(el);
		if (!style || style.display === "none" || style.visibility === "hidden" || Number(style.opacity) === 0) {
			return false;
		}
		const rect = el.getBoundingClientRect();
		return rect.width > 0 && rect.height > 0;
	};
	const dialog = document.querySelector("#baxia-dialog-content");
	if (isVisible(dialog)) {
		return "dialog";
	}
	const href = String(location.href || "").toLowerCase();
	if (href.includes("punish") || href.includes("____tmd____")) {
		return "punish_page";
	}
	const handle = document.querySelector(".btn_slide, .nc_iconfont.btn_slide, [class*='btn_slide'], span.nc_iconfont, [id*='n1z']");
	const track = document.querySelector(".nc_scale, [class*='nc_scale'], .nc_wrapper, [id*='n1t']");
	if (isVisible(handle) && isVisible(track)) {
		return "inline";
	}
	return "";
})() || "")`

// LooksLikePunishURL 判断当前地址是否为交易猫 punish 风控页。
func LooksLikePunishURL(currentURL string) bool {
	normalized := strings.ToLower(strings.TrimSpace(currentURL))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "punish") || strings.Contains(normalized, "____tmd____")
}

func resolvePhaseTimeout(sess *browser.Session) time.Duration {
	if sess != nil && LooksLikePunishURL(currentURL(sess)) {
		return PunishPhaseTimeout
	}
	return PhaseTimeout
}

func formatPunishPageError() error {
	return fmt.Errorf("交易猫触发风控验证页，无法自动通过")
}

func formatPunishPagePersistentError() error {
	return fmt.Errorf("交易猫触发风控验证页，重启浏览器后仍无法通过滑块验证")
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

func readSliderMode(sess *browser.Session) (string, error) {
	if sess == nil {
		return "", nil
	}
	err, result := sess.JavaScript(sliderModeJS)
	if err != nil {
		return "", err
	}
	mode := strings.TrimSpace(result)
	if mode == "" || mode == "undefined" || mode == "null" {
		return ModeInline, nil
	}
	return mode, nil
}
