package catlogin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"reference/browser-cat-slider/browser"
	"reference/browser-cat-slider/catslider"
)

// Login 执行完整的交易猫浏览器模拟登录。
//
// 典型调用方式：
//
//	ctx = catslider.WithBrowserRestart(ctx, restartFn)
//	ctx = catslider.WithPunishRestartState(ctx, &catslider.PunishRestartState{})
//	result, err := catlogin.Login(ctx, sess, catlogin.Credentials{...})
func Login(ctx context.Context, sess *browser.Session, creds Credentials) (LoginResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sess == nil {
		return LoginResult{}, fmt.Errorf("缺少浏览器会话")
	}

	account := strings.TrimSpace(creds.Account)
	password := strings.TrimSpace(creds.Password)
	cookieText := strings.TrimSpace(creds.Cookie)

	// 至少需要 Cookie 或账号密码之一。
	if cookieText == "" && creds.CookieSnapshot == "" && (account == "" || password == "") {
		return LoginResult{}, fmt.Errorf("缺少登录凭据：需要 Cookie 或账号密码")
	}

	sess.LogAction("交易猫：开始模拟登录")

	// 监听 loginByPassword 接口，提前捕获密码错误（浏览器内网络劫持，非后端调用）。
	watcher, err := StartPasswordWatcher(ctx, sess)
	if err != nil {
		return LoginResult{}, err
	}
	defer watcher.Stop()

	// --- 阶段 A：可选 Cookie 注入 + 打开工作台 ---
	if cookieText != "" || strings.TrimSpace(creds.CookieSnapshot) != "" {
		if err := InjectCookies(sess, cookieText, creds.CookieSnapshot); err != nil {
			return LoginResult{}, fmt.Errorf("注入 Cookie 失败: %w", err)
		}
	} else {
		sess.LogAction("交易猫：无历史 Cookie，将使用账号密码登录")
	}

	if err := OpenWorkBench(sess); err != nil {
		return LoginResult{}, fmt.Errorf("打开工作台失败: %w", err)
	}
	waitPageStable(sess)

	// --- 阶段 B：Cookie 已有效则直接成功 ---
	if result, ok, err := loginResultIfSuccess(sess); err != nil {
		return LoginResult{}, err
	} else if ok {
		sess.LogAction("交易猫：Cookie 注入后已登录 nickname=%s", result.Nickname)
		return result, nil
	}

	// --- 阶段 C：等待进入登录页或成功页（入口可能遇到滑块）---
	state, _, err := waitForEntryState(ctx, sess, account, password)
	if err != nil {
		return LoginResult{}, err
	}
	if state == pageStateSuccess {
		result, ok, err := loginResultIfSuccess(sess)
		if err != nil {
			return LoginResult{}, err
		}
		if ok {
			return result, nil
		}
	}
	if state != pageStateLoginForm {
		u := currentURL(sess)
		if browser.IsBlankPageURL(u) {
			return LoginResult{}, fmt.Errorf("未进入登录界面，当前为空白页")
		}
		if u == "" {
			u = MerchantWorkBenchURL
		}
		return LoginResult{}, fmt.Errorf("未进入登录界面，当前网址=%s", u)
	}

	if account == "" || password == "" {
		return LoginResult{}, fmt.Errorf("Cookie 已失效且缺少账号密码")
	}

	// --- 阶段 D：账号密码登录，最多整页重试 FullRetryCount 次 ---
	var result LoginResult
	var waitErr error
	for attempt := 1; attempt <= FullRetryCount; attempt++ {
		if attempt > 1 {
			sess.LogAction("交易猫：登录第 %d 次重头开始", attempt)
			if !shouldStayOnCurrentLoginFlowPage(sess) {
				if err := OpenWorkBench(sess); err != nil {
					return LoginResult{}, err
				}
				waitPageStable(sess)
			}
		}
		if err := LoginWithPassword(sess, account, password); err != nil {
			return LoginResult{}, err
		}
		if err := watcher.Failure(); err != nil {
			return LoginResult{}, fmt.Errorf("密码登录接口返回错误: %w", err)
		}
		result, waitErr = waitForLoginResult(ctx, sess, account, password, watcher)
		if waitErr == nil {
			break
		}
		if !shouldFullRetry(waitErr) || attempt >= FullRetryCount {
			return LoginResult{}, waitErr
		}
		sess.LogAction("交易猫：登录未成功，准备刷新重试 err=%v", waitErr)
	}
	if waitErr != nil {
		return LoginResult{}, waitErr
	}
	sess.LogAction("交易猫：登录成功 nickname=%s", result.Nickname)
	return result, nil
}

// loginResultIfSuccess 若页面已有 nickname，则收集 Cookie 并返回成功结果。
func loginResultIfSuccess(sess *browser.Session) (LoginResult, bool, error) {
	nickname := readNickname(sess)
	if strings.TrimSpace(nickname) == "" {
		return LoginResult{}, false, nil
	}
	result, err := buildLoginResult(sess, nickname)
	if err != nil {
		return LoginResult{}, false, err
	}
	return result, true, nil
}

func buildLoginResult(sess *browser.Session, nickname string) (LoginResult, error) {
	_ = sess.WaitStable(800 * time.Millisecond)
	header, snapshot := CollectLoginCookies(sess)
	if header == "" {
		return LoginResult{}, fmt.Errorf("登录成功但未读取到 Cookie")
	}
	remark := "交易猫模拟登录成功"
	if nickname = strings.TrimSpace(nickname); nickname != "" {
		remark = fmt.Sprintf("交易猫模拟登录成功 nickname=%s", nickname)
	}
	return LoginResult{
		Cookie: header, CookieSnapshot: snapshot, Nickname: nickname, Remark: remark,
	}, nil
}

// waitForEntryState 打开工作台后等待：已登录 / 登录表单 / 入口滑块。
func waitForEntryState(ctx context.Context, sess *browser.Session, account, password string) (pageState, string, error) {
	deadline := time.NewTimer(LoginTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(FastPollInterval)
	defer ticker.Stop()

	for {
		if err := failIfContextDone(ctx); err != nil {
			return pageStateUnknown, "", err
		}
		if err := failIfSMSIdentityVerification(sess); err != nil {
			return pageStateUnknown, "", err
		}
		if nickname := readNickname(sess); nickname != "" {
			return pageStateSuccess, nickname, nil
		}
		if isMemberLoginFlowPage(sess) {
			sess.LogAction("交易猫：已进入登录页")
			return pageStateLoginForm, "", nil
		}
		// 入口滑块：常见于 Cookie 失效或风控时打开工作台
		if _, visible, err := catsliderReadRect(sess); err != nil {
			return pageStateUnknown, "", err
		} else if visible {
			resume := func() error { return OpenWorkBenchIfNeeded(sess) }
			if err := catslider.HandleSliderChallenge(ctx, sess, catslider.ChallengeOptions{
				Phase: "入口", AfterRefresh: resume, AfterRestart: resume,
			}); err != nil {
				return pageStateUnknown, "", err
			}
			if hasPasswordLoginForm(sess) || looksLikeLoginURL(currentURL(sess)) {
				return pageStateLoginForm, "", nil
			}
			continue
		}
		if hasPasswordLoginForm(sess) || looksLikeLoginURL(currentURL(sess)) {
			return pageStateLoginForm, "", nil
		}
		select {
		case <-deadline.C:
			return pageStateUnknown, "", fmt.Errorf("等待登录页超时")
		case <-ticker.C:
		}
	}
}

// waitForLoginResult 提交登录后轮询：成功 / 吐司失败 / 登录滑块。
func waitForLoginResult(ctx context.Context, sess *browser.Session, account, password string, watcher *PasswordWatcher) (LoginResult, error) {
	// 总超时 = 基础超时 × 滑块最大刷新轮数（与主项目一致）
	deadline := time.Now().Add(LoginTimeout * time.Duration(catslider.RefreshCycleCount))

	resumeLogin := func() error {
		if err := OpenWorkBenchIfNeeded(sess); err != nil {
			return err
		}
		return fillPasswordLoginIfNeeded(sess, account, password)
	}
	retrySubmit := func() error {
		return retrySubmitLogin(sess, account, password)
	}

	for {
		if err := failIfContextDone(ctx); err != nil {
			return LoginResult{}, err
		}
		if time.Now().After(deadline) {
			if err := failIfSMSIdentityVerification(sess); err != nil {
				return LoginResult{}, err
			}
			u := currentURL(sess)
			if u == "" {
				u = MerchantWorkBenchURL
			}
			if looksLikeLoginURL(u) || hasPasswordLoginForm(sess) {
				return LoginResult{}, fmt.Errorf("登录界面超过 %s 仍未成功", LoginTimeout*catslider.RefreshCycleCount)
			}
			return LoginResult{}, fmt.Errorf("登录超时，当前网址=%s", u)
		}

		if err := watcher.Failure(); err != nil {
			return LoginResult{}, fmt.Errorf("密码登录失败: %w", err)
		}

		if result, ok, err := loginResultIfSuccess(sess); err != nil {
			return LoginResult{}, err
		} else if ok {
			return result, nil
		}

		// 吐司提示（除「网络异常」外视为失败）
		if toast, ok, err := catslider.ReadToastMessage(sess); err != nil {
			return LoginResult{}, err
		} else if ok && !strings.Contains(toast, "网络异常") {
			return LoginResult{}, fmt.Errorf("登录失败，吐司=%s", strings.TrimSpace(toast))
		}

		if err := failIfSMSIdentityVerification(sess); err != nil {
			return LoginResult{}, err
		}

		// 登录滑块：拖动 → 刷新后重提交 / punish 页重启浏览器
		if _, visible, err := catsliderReadRect(sess); err != nil {
			return LoginResult{}, err
		} else if visible {
			if err := catslider.HandleSliderChallenge(ctx, sess, catslider.ChallengeOptions{
				Phase: "登录", AfterRefresh: retrySubmit, AfterRestart: resumeLogin,
			}); err != nil {
				return LoginResult{}, err
			}
			_ = fillPasswordLoginIfNeeded(sess, account, password)
			continue
		}

		_ = fillPasswordLoginIfNeeded(sess, account, password)

		sleep := PollInterval
		if hasPasswordLoginForm(sess) || looksLikeLoginURL(currentURL(sess)) {
			sleep = FastPollInterval
		}
		select {
		case <-ctx.Done():
			return LoginResult{}, ctx.Err()
		case <-time.After(sleep):
		}
	}
}

func failIfContextDone(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// shouldFullRetry 判断是否应在刷新页面后重头登录（排除滑块/密码/身份验证类错误）。
func shouldFullRetry(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.TrimSpace(err.Error())
	if strings.Contains(msg, "身份验证") || strings.Contains(msg, "吐司") ||
		strings.Contains(msg, "滑块") || strings.Contains(msg, "密码") {
		return false
	}
	return strings.Contains(msg, "仍未成功") || strings.Contains(msg, "登录超时")
}
