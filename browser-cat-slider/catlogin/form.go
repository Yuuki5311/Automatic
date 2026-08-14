package catlogin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"reference/browser-cat-slider/browser"
	"reference/browser-cat-slider/catslider"
)

// LoginWithPassword 在登录页完成：切换密码登录 → 填表 → 勾选协议 → 提交。
func LoginWithPassword(sess *browser.Session, account, password string) error {
	if sess == nil {
		return fmt.Errorf("浏览器会话为空")
	}
	account = strings.TrimSpace(account)
	password = strings.TrimSpace(password)
	if account == "" || password == "" {
		return fmt.Errorf("账号或密码为空")
	}

	// 1. 部分页面默认展示验证码登录，需先点「密码登录」。
	if clicked, err := clickDivExactText(sess, "密码登录", PasswordWaitTime); err != nil {
		return fmt.Errorf("点击密码登录失败: %w", err)
	} else if clicked {
		sess.LogAction("交易猫：已切换到密码登录")
	}

	// 2. 等待输入框出现。
	if _, err := sess.WaitElementVisible("input[placeholder=请输入手机号]", 5*time.Second); err != nil {
		return fmt.Errorf("等待手机号输入框失败: %w", err)
	}
	if _, err := sess.WaitElementVisible("input[placeholder=请输入密码]", 5*time.Second); err != nil {
		return fmt.Errorf("等待密码输入框失败: %w", err)
	}

	// 3. 填写账号密码（使用原生 value setter 以兼容前端框架）。
	if err := sess.Input("input[placeholder=请输入手机号]", account, 5*time.Second); err != nil {
		return fmt.Errorf("填写账号失败: %w", err)
	}
	if err := sess.Input("input[placeholder=请输入密码]", password, 5*time.Second); err != nil {
		return fmt.Errorf("填写密码失败: %w", err)
	}

	// 4. 勾选用户协议（class 含 warningIconBox 的 div）。
	if clicked, err := clickDivClassContains(sess, "warningIconBox", 1200*time.Millisecond); err != nil {
		return fmt.Errorf("勾选协议失败: %w", err)
	} else if clicked {
		sess.LogAction("交易猫：已勾选登录协议")
	}

	// 5. 点击「立即登录」。
	if clicked, err := clickButtonExactText(sess, "立即登录", 3*time.Second); err != nil {
		return fmt.Errorf("点击立即登录失败: %w", err)
	} else if clicked {
		sess.LogAction("交易猫：已点击立即登录")
	}
	return nil
}

// passwordLoginFilled 检查表单中的账号密码是否与期望值一致。
func passwordLoginFilled(sess *browser.Session, account, password string) bool {
	err, result := sess.JavaScript(`return String((function () {
		const account = String(document.querySelector('input[placeholder=请输入手机号]')?.value || '').trim();
		const password = String(document.querySelector('input[placeholder=请输入密码]')?.value || '').trim();
		return JSON.stringify({ account, password });
	})())`)
	if err != nil {
		return false
	}
	var values struct {
		Account  string `json:"account"`
		Password string `json:"password"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &values) != nil {
		return false
	}
	return values.Account == strings.TrimSpace(account) && values.Password == strings.TrimSpace(password)
}

// fillPasswordLoginIfNeeded 滑块通过或页面刷新后，若表单未填则立即补填。
func fillPasswordLoginIfNeeded(sess *browser.Session, account, password string) error {
	account = strings.TrimSpace(account)
	password = strings.TrimSpace(password)
	if account == "" || password == "" {
		return nil
	}
	// 滑块可见时不填表，等滑块处理完。
	if _, visible, err := catsliderReadRect(sess); err != nil {
		return err
	} else if visible {
		return nil
	}
	if !hasPasswordLoginForm(sess) && !looksLikeLoginURL(currentURL(sess)) {
		return nil
	}
	if passwordLoginFilled(sess, account, password) {
		return nil
	}
	sess.LogAction("交易猫：登录页账号密码未就绪，立即补填")
	return LoginWithPassword(sess, account, password)
}

// retrySubmitLogin 滑块刷新后若表单已填则只重新点登录按钮。
func retrySubmitLogin(sess *browser.Session, account, password string) error {
	if !passwordLoginFilled(sess, account, password) {
		return fillPasswordLoginIfNeeded(sess, account, password)
	}
	clicked, err := clickButtonExactText(sess, "立即登录", 3*time.Second)
	if err != nil {
		return fmt.Errorf("重新点击登录失败: %w", err)
	}
	if !clicked {
		return fmt.Errorf("重新点击登录失败：未找到按钮")
	}
	return nil
}

// catsliderReadRect 包装 catslider.ReadSliderRect，避免 catlogin 与 catslider 循环依赖时的类型转换。
func catsliderReadRect(sess *browser.Session) (catslider.SliderRect, bool, error) {
	return catslider.ReadSliderRect(sess)
}

// --- 以下 DOM 点击辅助：通过 JS 在页面内查找并 click，比 rod Element 更耐动态渲染 ---

func clickDivExactText(sess *browser.Session, target string, timeout time.Duration) (bool, error) {
	return clickByJS(sess, "div", "textContent", target, timeout)
}

func clickButtonExactText(sess *browser.Session, target string, timeout time.Duration) (bool, error) {
	return clickByJS(sess, "button", "textContent", target, timeout)
}

func clickDivClassContains(sess *browser.Session, classFragment string, timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err, result := sess.JavaScript(fmt.Sprintf(`return String((function () {
			const target = %s;
			for (const el of document.querySelectorAll("div")) {
				if (String(el?.className || "").indexOf(target) >= 0) {
					el.scrollIntoView?.({ behavior: "smooth", block: "center" });
					el.click?.();
					return "true";
				}
			}
			return "false";
		})())`, strconv.Quote(classFragment)))
		if err == nil && strings.TrimSpace(result) == "true" {
			return true, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false, nil
}

func clickByJS(sess *browser.Session, tag, prop, target string, timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err, result := sess.JavaScript(fmt.Sprintf(`return String((function () {
			const target = %s;
			for (const el of document.querySelectorAll(%q)) {
				const str = String(el?.%s || "").trim();
				const rect = el?.getBoundingClientRect?.();
				if (str === target && rect && rect.x > 0) {
					el.scrollIntoView?.({ behavior: "smooth", block: "center" });
					el.click?.();
					return "true";
				}
			}
			return "false";
		})())`, strconv.Quote(target), tag, prop))
		if err == nil && strings.TrimSpace(result) == "true" {
			return true, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false, nil
}

// failIfSMSIdentityVerification 检测 #upgrade iframe 是否指向短信/身份验证页。
func failIfSMSIdentityVerification(sess *browser.Session) error {
	if sess == nil {
		return nil
	}
	err, result := sess.JavaScript(`return String((function () {
		const el = document.querySelector("#upgrade");
		if (!el) return "";
		return String(el.getAttribute("src") || el.src || "");
	})())`)
	if err != nil {
		return err
	}
	src := strings.TrimSpace(result)
	if src == "" || src == "undefined" || src == "null" {
		return nil
	}
	if strings.Contains(src, "id-verification") {
		return fmt.Errorf("交易猫登录需要短信/身份验证，无法自动完成 iframe=%s", src)
	}
	return nil
}
