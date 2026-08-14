package catlogin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"

	"reference/browser-cat-slider/browser"
)

const (
	loginByPasswordURLFragment   = "api-proxy/loginByPassword"
	loginValidateSkipRet         = "FAIL_SYS_USER_VALIDATE"
	loginByPasswordHijackPattern = "*loginByPassword*"
)

// credentialError 表示 loginByPassword 接口返回的账号密码类错误。
type credentialError struct {
	Code string
	Msg  string
}

func (e *credentialError) Error() string {
	if e == nil {
		return ""
	}
	if msg := strings.TrimSpace(e.Msg); msg != "" {
		return msg
	}
	if code := strings.TrimSpace(e.Code); code != "" {
		return code
	}
	return "交易猫密码登录失败"
}

func loginPasswordFatalCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "INVALID_PASSWORD", "ERROR_PASSWORD_NEVER_REGISTER", "ERROR_PASSWORD_NEVER_SET", "ACCOUNT_NOT_ALLOWED_LOGIN_LOCKED":
		return true
	default:
		return false
	}
}

var loginByPasswordHTTPClient = &http.Client{Timeout: 15 * time.Second}

// PasswordWatcher 在浏览器内拦截 loginByPassword 响应，提前发现账号密码错误。
// 这是 CDP 网络劫持，不涉及后端 API。
type PasswordWatcher struct {
	cancel context.CancelFunc
	done   chan struct{}
	router *rod.HijackRouter
	mu     sync.Mutex
	failure error
}

// StartPasswordWatcher 启动 loginByPassword 响应监听；登录流程结束必须调用 Stop。
func StartPasswordWatcher(ctx context.Context, sess *browser.Session) (*PasswordWatcher, error) {
	if sess == nil {
		return nil, fmt.Errorf("缺少浏览器会话")
	}
	page, err := sess.MainPage()
	if err != nil {
		return nil, err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	w := &PasswordWatcher{cancel: cancel, done: make(chan struct{})}
	router := page.HijackRequests()
	if err := router.Add(loginByPasswordHijackPattern, proto.NetworkResourceType(""), func(h *rod.Hijack) {
		w.handleHijack(sess, h)
	}); err != nil {
		cancel()
		_ = router.Stop()
		return nil, fmt.Errorf("注册 loginByPassword 拦截失败: %w", err)
	}
	w.router = router
	go router.Run()
	go func() {
		defer close(w.done)
		<-watchCtx.Done()
		if w.router != nil {
			_ = w.router.Stop()
		}
	}()
	sess.LogAction("交易猫：loginByPassword 拦截已启动")
	return w, nil
}

func (w *PasswordWatcher) Stop() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
	<-w.done
}

// Failure 返回拦截到的密码类错误；无错误时返回 nil。
func (w *PasswordWatcher) Failure() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.failure
}

func (w *PasswordWatcher) handleHijack(sess *browser.Session, hijack *rod.Hijack) {
	if hijack == nil || hijack.Request == nil || hijack.Request.URL() == nil {
		return
	}
	requestURL := strings.TrimSpace(hijack.Request.URL().String())
	if !strings.Contains(strings.ToLower(requestURL), loginByPasswordURLFragment) {
		hijack.Skip = true
		return
	}
	sess.LogAction("交易猫：拦截 loginByPassword url=%s", requestURL)
	if err := hijack.LoadResponse(loginByPasswordHTTPClient, true); err != nil {
		hijack.ContinueRequest(&proto.FetchContinueRequest{})
		return
	}
	body := ""
	if hijack.Response != nil {
		body = strings.TrimSpace(hijack.Response.Body())
	}
	w.applyBody(sess, body)
}

func (w *PasswordWatcher) applyBody(sess *browser.Session, rawBody string) {
	failure, skipped := evaluateLoginByPasswordBody(rawBody)
	w.mu.Lock()
	defer w.mu.Unlock()
	if skipped {
		sess.LogAction("交易猫：loginByPassword 命中滑块校验跳过码，继续等待")
		return
	}
	if failure != nil && w.failure == nil {
		w.failure = failure
		sess.LogAction("交易猫：loginByPassword 判定失败 %v", failure)
	}
}

func evaluateLoginByPasswordBody(raw string) (error, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil, false
	}
	if retRaw, ok := payload["ret"]; ok {
		var retItems []string
		if err := json.Unmarshal(retRaw, &retItems); err == nil {
			for _, item := range retItems {
				if strings.Contains(strings.TrimSpace(item), loginValidateSkipRet) {
					return nil, true // 滑块校验中，不算失败
				}
			}
		}
	}
	msg, code := "", ""
	if msgRaw, ok := payload["msg"]; ok {
		_ = json.Unmarshal(msgRaw, &msg)
	}
	if codeRaw, ok := payload["code"]; ok {
		_ = json.Unmarshal(codeRaw, &code)
	}
	msg, code = strings.TrimSpace(msg), strings.TrimSpace(code)
	if loginPasswordFatalCode(code) {
		return &credentialError{Code: code, Msg: msg}, false
	}
	if msg == "" {
		return nil, false
	}
	dataNull := true
	if dataRaw, ok := payload["data"]; ok {
		dataNull = strings.TrimSpace(string(dataRaw)) == "" || strings.TrimSpace(string(dataRaw)) == "null"
	}
	if !dataNull {
		return nil, false
	}
	return &credentialError{Code: code, Msg: msg}, false
}
