package captcha

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/browser"
)

const baxiaDetectJS = `(() => {
	if (document.querySelector('.baxia-dialog')) return true;
	const iframes = [...document.querySelectorAll('iframe')];
	for (const f of iframes) {
		const src = f.src || '';
		if (src.includes('_____tmd_____') || src.includes('action=captcha') || src.includes('/punish')) return true;
	}
	const t = (document.body && document.body.innerText) || '';
	return t.includes('拖动下方滑块') || t.includes('拖动到最右边');
})()`

const baxiaSliderRectJS = `(() => {
	const pickIframe = () => {
		const dialog = document.querySelector('.baxia-dialog iframe');
		if (dialog) return dialog;
		for (const f of document.querySelectorAll('iframe')) {
			const src = f.src || '';
			if (src.includes('_____tmd_____') || src.includes('action=captcha') || src.includes('/punish')) return f;
		}
		return null;
	};
	const iframe = pickIframe();
	if (!iframe) return { ok: false, reason: 'no-iframe' };
	let doc = null;
	try { doc = iframe.contentDocument || (iframe.contentWindow && iframe.contentWindow.document); } catch (e) {
		return { ok: false, reason: 'cross-origin' };
	}
	if (!doc || !doc.body) return { ok: false, reason: 'iframe-empty' };

	const btn = doc.querySelector(
		'#nc_1_n1z, #nc_2_n1z, .btn_slide, .nc_iconfont.btn_slide, .slidetounlock, [class*="btn_slide"]'
	);
	if (!btn) {
		return { ok: false, reason: 'no-btn', snippet: (doc.body.innerText || '').slice(0, 200) };
	}

	const candidates = [];
	for (const sel of ['.nc_scale', '.nc_wrapper', '.nc-container', '#nc_1_n1t', '.nc_bg']) {
		const el = doc.querySelector(sel);
		if (el) candidates.push(el);
	}
	let el = btn.parentElement;
	for (let i = 0; i < 6 && el; i++, el = el.parentElement) {
		candidates.push(el);
	}
	for (const n of doc.querySelectorAll('span, div, p')) {
		const t = (n.textContent || '').trim();
		if (t.includes('拖动到最右边') || t.includes('请按住滑块')) candidates.push(n);
	}

	let track = null;
	let bestW = 0;
	for (const c of candidates) {
		const r = c.getBoundingClientRect();
		if (r.width > bestW) {
			bestW = r.width;
			track = c;
		}
	}
	const br = btn.getBoundingClientRect();
	const ir = iframe.getBoundingClientRect();
	let tr = track ? track.getBoundingClientRect() : br;
	if (tr.width < br.width + 80) {
		tr = { x: ir.x + 20, y: br.y, width: Math.max(ir.width - 40, br.width + 200), height: Math.max(br.height, 40) };
	}
	if (br.width <= 0 || tr.width <= 0) {
		return { ok: false, reason: 'zero-size' };
	}
	return {
		ok: true,
		btnX: br.x, btnY: br.y, btnW: br.width, btnH: br.height,
		trackX: tr.x, trackY: tr.y, trackW: tr.width, trackH: tr.height
	};
})()`

type baxiaRect struct {
	OK      bool    `json:"ok"`
	Reason  string  `json:"reason"`
	Snippet string  `json:"snippet"`
	BtnX    float64 `json:"btnX"`
	BtnY    float64 `json:"btnY"`
	BtnW    float64 `json:"btnW"`
	BtnH    float64 `json:"btnH"`
	TrackX  float64 `json:"trackX"`
	TrackY  float64 `json:"trackY"`
	TrackW  float64 `json:"trackW"`
	TrackH  float64 `json:"trackH"`
}

func detectBaxiaCaptcha(ctx context.Context) (bool, error) {
	var hit bool
	if err := browser.Eval(ctx, baxiaDetectJS, &hit); err != nil {
		return false, err
	}
	return hit, nil
}

const baxiaClickRetryJS = `(() => {
	try {
		const f = document.querySelector('.baxia-dialog iframe, iframe[src*="_____tmd_____"]');
		const doc = f && (f.contentDocument || f.contentWindow.document);
		if (!doc) return false;
		const text = (doc.body && doc.body.innerText) || '';
		const refresh = doc.querySelector('#nc_1_refresh1, .nc_iconfont.btn_refresh, [class*="refresh"], .errloading, #nc_1_wrapper');
		if (refresh) { refresh.click(); return true; }
		if (text.includes('验证失败') || text.includes('点击框体')) {
			const box = doc.querySelector('.nc_wrapper, .nc-container, #nc_1_n1t, .nc_scale') || doc.body;
			box.click();
			return true;
		}
		return false;
	} catch (e) { return false; }
})()`

const baxiaStatusJS = `(() => {
	const dialog = document.querySelector('.baxia-dialog');
	const iframes = [...document.querySelectorAll('iframe')];
	const captchaFrame = (() => {
		const d = document.querySelector('.baxia-dialog iframe');
		if (d) return d;
		for (const f of iframes) {
			const src = f.src || '';
			if (src.includes('_____tmd_____') || src.includes('action=captcha') || src.includes('/punish')) return f;
		}
		return null;
	})();
	if (!dialog && !captchaFrame) {
		const t = (document.body && document.body.innerText) || '';
		if (t.includes('拖动下方滑块') || t.includes('拖动到最右边') || t.includes('验证失败')) return 'pending';
		return 'gone';
	}
	try {
		const doc = captchaFrame && (captchaFrame.contentDocument || captchaFrame.contentWindow.document);
		const text = ((doc && doc.body && doc.body.innerText) || '') + ((dialog && dialog.innerText) || '');
		if (text.includes('验证失败') || text.includes('点击框体重试') || /error:/i.test(text)) return 'failed';
		if (doc && doc.querySelector('#nc_1_n1z, .btn_slide')) return 'pending';
		if (text.includes('验证通过') || text.includes('成功')) return 'passed';
		return 'pending';
	} catch (e) {
		return 'pending';
	}
})()`

// solveBaxiaSlideToEnd 无头模式下自动拖到最右；失败直接返回错误（无人工兜底）。
func solveBaxiaSlideToEnd(ctx context.Context, maxRetry int) error {
	if maxRetry <= 0 {
		maxRetry = 3
	}
	for attempt := 0; attempt < maxRetry; attempt++ {
		_ = clickBaxiaRetry(ctx)
		if _, err := waitBaxiaSliderRect(ctx, 25*time.Second); err != nil {
			slog.Warn("等待百炼滑块失败", "component", "captcha", "attempt", attempt+1, "error", err)
			_ = clickBaxiaRetry(ctx)
			if err := browser.Sleep(ctx, time.Second); err != nil {
				return err
			}
			continue
		}

		slog.Info("开始拖动百炼滑块", "component", "captcha", "attempt", attempt+1)
		if err := dragBaxiaInsideIframe(ctx); err != nil {
			slog.Warn("百炼滑块拖动失败", "component", "captcha", "error", err)
			_ = clickBaxiaRetry(ctx)
			if err := browser.Sleep(ctx, 1500*time.Millisecond); err != nil {
				return err
			}
			continue
		}

		if err := browser.Sleep(ctx, 2800*time.Millisecond); err != nil {
			return err
		}

		st := baxiaStatus(ctx)
		slog.Info("百炼滑块结果", "component", "captcha", "status", st, "attempt", attempt+1)
		if st == "passed" || st == "gone" {
			slog.Info("百炼滑块验证通过", "component", "captcha")
			return nil
		}
		if st == "failed" {
			slog.Warn("百炼判定验证失败", "component", "captcha")
			_ = clickBaxiaRetry(ctx)
			if err := browser.Sleep(ctx, 1200*time.Millisecond); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("百炼滑块验证失败：已重试%d次", maxRetry)
}

func clickBaxiaRetry(ctx context.Context) error {
	var ok bool
	return browser.Eval(ctx, baxiaClickRetryJS, &ok)
}

func baxiaStatus(ctx context.Context) string {
	var st string
	if err := browser.Eval(ctx, baxiaStatusJS, &st); err != nil || st == "" {
		return "pending"
	}
	return st
}

func baxiaPassed(ctx context.Context) (bool, error) {
	st := baxiaStatus(ctx)
	return st == "passed" || st == "gone", nil
}

const measureBaxiaDragJS = `(() => {
	const pickIframe = () => {
		const dialog = document.querySelector('.baxia-dialog iframe');
		if (dialog) return dialog;
		for (const f of document.querySelectorAll('iframe')) {
			const src = f.src || '';
			if (src.includes('_____tmd_____') || src.includes('action=captcha') || src.includes('/punish')) return f;
		}
		return null;
	};
	const iframe = pickIframe();
	if (!iframe) return { ok: false, reason: 'no-iframe' };
	let doc;
	try { doc = iframe.contentDocument || iframe.contentWindow.document; } catch (e) {
		return { ok: false, reason: 'cross-origin' };
	}
	const btn = doc.querySelector('#nc_1_n1z, #nc_2_n1z, .btn_slide, .nc_iconfont.btn_slide');
	if (!btn) return { ok: false, reason: 'no-btn' };
	const track = doc.querySelector('.nc_scale') || doc.querySelector('.nc_wrapper') || btn.parentElement;
	const br = btn.getBoundingClientRect();
	const tr = track.getBoundingClientRect();
	const ir = iframe.getBoundingClientRect();

	let travel = Math.ceil(tr.right - br.right);
	const byOffset = Math.ceil((track.offsetWidth || tr.width) - (btn.offsetWidth || br.width));
	if (byOffset > travel) travel = byOffset;
	const iframeTravel = Math.floor(ir.width - 48 - (btn.offsetWidth || br.width));
	if (travel < 220 && iframeTravel > travel) travel = iframeTravel;
	travel = Math.ceil(travel + 3);
	if (travel < 200) travel = 260;

	return {
		ok: true,
		startX: ir.x + br.x + br.width / 2,
		startY: ir.y + br.y + br.height / 2,
		distance: travel,
		trackW: track.offsetWidth || tr.width,
		btnW: btn.offsetWidth || br.width,
		iframeW: ir.width,
		remain: Math.ceil(tr.right - br.right)
	};
})()`

func dragBaxiaInsideIframe(ctx context.Context) error {
	var m struct {
		OK       bool    `json:"ok"`
		Reason   string  `json:"reason"`
		StartX   float64 `json:"startX"`
		StartY   float64 `json:"startY"`
		Distance int     `json:"distance"`
		TrackW   float64 `json:"trackW"`
		BtnW     float64 `json:"btnW"`
		IframeW  float64 `json:"iframeW"`
		Remain   float64 `json:"remain"`
	}
	if err := browser.Eval(ctx, measureBaxiaDragJS, &m); err != nil {
		return err
	}
	if !m.OK {
		return fmt.Errorf("测量滑块失败: %s", m.Reason)
	}
	slog.Info("百炼滑块行程", "component", "captcha",
		"distance", m.Distance, "remain", m.Remain, "trackW", m.TrackW, "btnW", m.BtnW, "iframeW", m.IframeW)
	return dragWithHumanTrace(ctx, m.StartX, m.StartY, m.Distance)
}

func waitBaxiaSliderRect(ctx context.Context, timeout time.Duration) (baxiaRect, error) {
	deadline := time.Now().Add(timeout)
	var last baxiaRect
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return baxiaRect{}, err
		}
		_ = clickBaxiaRetry(ctx)
		var rect baxiaRect
		if err := browser.Eval(ctx, baxiaSliderRectJS, &rect); err != nil {
			last.Reason = err.Error()
		} else if rect.OK {
			return rect, nil
		} else {
			last = rect
		}
		if err := browser.Sleep(ctx, 500*time.Millisecond); err != nil {
			return baxiaRect{}, err
		}
	}
	return baxiaRect{}, fmt.Errorf("滑块未就绪: %s %s", last.Reason, last.Snippet)
}
