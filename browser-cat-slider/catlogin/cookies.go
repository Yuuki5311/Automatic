package catlogin

import (
	"encoding/json"
	"strings"

	"github.com/go-rod/rod/lib/proto"

	"reference/browser-cat-slider/browser"
)

// loginInjectCookieNames 允许注入浏览器的登录身份 Cookie 名称。
// 风控 Cookie（isg、tfstk、cna 等） intentionally 不注入，留给浏览器在 profile 下自行生成，
// 避免多任务/多店铺共用风控 Cookie 导致滑块冲突。
var loginInjectCookieNames = map[string]struct{}{
	"ctoken":              {},
	"ieu_member_sid":      {},
	"ieu_member_sid.sig":  {},
	"ieu_member_uid":      {},
	"ieu_member_uid.sig":  {},
	"jym_session_id":      {},
	"jym_session_id.sig":  {},
	"_m_h5_tk":            {},
	"_m_h5_tk_enc":        {},
}

// isLoginInjectCookie 判断 Cookie 名是否属于可注入的登录身份字段。
func isLoginInjectCookie(name string) bool {
	_, ok := loginInjectCookieNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// filterInjectCookies 从解析结果中只保留登录身份 Cookie。
func filterInjectCookies(cookies []*proto.NetworkCookieParam) []*proto.NetworkCookieParam {
	if len(cookies) == 0 {
		return nil
	}
	out := make([]*proto.NetworkCookieParam, 0, len(cookies))
	for _, c := range cookies {
		if c != nil && isLoginInjectCookie(c.Name) {
			out = append(out, c)
		}
	}
	return out
}

// filterNetworkCookies 从浏览器读出的 Cookie 列表中只保留登录身份字段。
func filterNetworkCookies(cookies []*proto.NetworkCookie) []*proto.NetworkCookie {
	if len(cookies) == 0 {
		return nil
	}
	out := make([]*proto.NetworkCookie, 0, len(cookies))
	for _, c := range cookies {
		if c != nil && isLoginInjectCookie(c.Name) {
			copyCookie := *c
			out = append(out, &copyCookie)
		}
	}
	return out
}

// InjectCookies 将历史 Cookie 注入浏览器，只注入身份字段。
func InjectCookies(sess *browser.Session, rawCookie, cookieSnapshot string) error {
	if sess == nil {
		return nil
	}
	// 优先用 JSON 快照（字段更完整），否则解析 Cookie 文本。
	cookies := filterInjectCookies(parseCookieSnapshot(cookieSnapshot, MerchantCookieDomain))
	if len(cookies) == 0 {
		cookies = filterInjectCookies(parseCookiePairs(rawCookie, MerchantCookieDomain))
	}
	if len(cookies) == 0 {
		sess.LogAction("交易猫：无可注入的登录 Cookie，将走账号密码登录")
		return nil
	}
	names := make([]string, 0, len(cookies))
	for _, c := range cookies {
		names = append(names, c.Name)
	}
	sess.LogAction("交易猫：注入登录 Cookie count=%d names=%s", len(cookies), strings.Join(names, ","))
	return sess.SetCookies(cookies)
}

// CollectLoginCookies 登录成功后从浏览器读取 Cookie，过滤为身份字段并返回文本与快照。
func CollectLoginCookies(sess *browser.Session) (header, snapshot string) {
	if sess == nil {
		return "", ""
	}
	fullHeader, fullSnapshot := collectBrowserCookies(sess)
	header = filterLoginCookieText(fullHeader)
	snapshot = filterLoginCookieSnapshot(fullSnapshot)
	if snapshot == "" && header != "" {
		snapshot = header
	}
	if header != "" {
		sess.LogAction("交易猫：登录 Cookie 已过滤 full_len=%d cookie_len=%d", len(fullHeader), len(header))
	}
	return strings.TrimSpace(header), strings.TrimSpace(snapshot)
}

func filterLoginCookieText(cookieHeader string) string {
	return joinCookieParams(filterInjectCookies(parseCookiePairs(cookieHeader, MerchantCookieDomain)))
}

func filterLoginCookieSnapshot(cookieSnapshot string) string {
	normalized := strings.TrimSpace(cookieSnapshot)
	if normalized == "" {
		return ""
	}
	var cookies []*proto.NetworkCookie
	if err := json.Unmarshal([]byte(normalized), &cookies); err == nil {
		filtered := filterNetworkCookies(cookies)
		if len(filtered) == 0 {
			return ""
		}
		data, err := json.Marshal(filtered)
		if err != nil {
			return ""
		}
		return string(data)
	}
	return filterLoginCookieText(normalized)
}

func collectBrowserCookies(sess *browser.Session) (header, snapshot string) {
	if cookies, err := sess.Cookies(MerchantBaseURL); err == nil && len(cookies) > 0 {
		header = joinNetworkCookies(cookies)
		snapshot = marshalCookieSnapshot(cookies)
	}
	if jsErr, docCookie := sess.JavaScript(`return document.cookie;`); jsErr == nil {
		docCookie = strings.TrimSpace(docCookie)
		if docCookie != "" {
			if header == "" {
				header = docCookie
			} else {
				header = mergeCookieHeader(header, docCookie)
			}
			if snapshot == "" {
				snapshot = docCookie
			}
		}
	}
	return header, snapshot
}

// parseCookiePairs 将 "a=1;b=2" 文本解析为 rod 可注入的 CookieParam。
func parseCookiePairs(rawCookie, domain string) []*proto.NetworkCookieParam {
	normalized := strings.NewReplacer("\r\n", ";", "\n", ";", "\r", ";", "\t", " ").Replace(strings.TrimSpace(rawCookie))
	if normalized == "" {
		return nil
	}
	result := make([]*proto.NetworkCookieParam, 0, 8)
	for _, segment := range strings.Split(normalized, ";") {
		part := strings.TrimSpace(segment)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" {
			continue
		}
		switch strings.ToLower(name) {
		case "path", "domain", "expires", "max-age", "secure", "httponly", "samesite":
			continue
		}
		result = append(result, &proto.NetworkCookieParam{
			Name: name, Value: value, Domain: domain, Path: "/",
		})
	}
	return result
}

// parseCookieSnapshot 将 JSON Cookie 快照转为可注入的 CookieParam。
func parseCookieSnapshot(cookieSnapshot, domain string) []*proto.NetworkCookieParam {
	normalized := strings.TrimSpace(cookieSnapshot)
	if normalized == "" {
		return nil
	}
	var cookies []*proto.NetworkCookie
	if err := json.Unmarshal([]byte(normalized), &cookies); err != nil {
		return parseCookiePairs(normalized, domain)
	}
	result := make([]*proto.NetworkCookieParam, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || strings.TrimSpace(cookie.Name) == "" {
			continue
		}
		param := &proto.NetworkCookieParam{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path,
			Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly, SameSite: cookie.SameSite,
			Expires: cookie.Expires, Priority: cookie.Priority,
		}
		if strings.TrimSpace(param.Domain) == "" {
			param.Domain = domain
			param.Path = "/"
		}
		result = append(result, param)
	}
	return result
}

func joinCookieParams(cookies []*proto.NetworkCookieParam) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c != nil && strings.TrimSpace(c.Name) != "" {
			parts = append(parts, c.Name+"="+strings.TrimSpace(c.Value))
		}
	}
	return strings.Join(parts, ";")
}

func joinNetworkCookies(cookies []*proto.NetworkCookie) string {
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c != nil && strings.TrimSpace(c.Name) != "" {
			parts = append(parts, c.Name+"="+strings.TrimSpace(c.Value))
		}
	}
	return strings.Join(parts, ";")
}

func marshalCookieSnapshot(cookies []*proto.NetworkCookie) string {
	filtered := filterNetworkCookies(cookies)
	if len(filtered) == 0 {
		return ""
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return ""
	}
	return string(data)
}

// mergeCookieHeader 合并两段 Cookie 文本，后者覆盖同名项。
func mergeCookieHeader(base, browserCookie string) string {
	merged := make([]*proto.NetworkCookieParam, 0, 16)
	index := make(map[string]int)
	appendPairs := func(raw string, replace bool) {
		for _, pair := range parseCookiePairs(raw, MerchantCookieDomain) {
			if pair == nil {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(pair.Name))
			if idx, ok := index[key]; ok {
				if replace {
					merged[idx].Value = pair.Value
				}
				continue
			}
			index[key] = len(merged)
			merged = append(merged, &proto.NetworkCookieParam{Name: pair.Name, Value: pair.Value})
		}
	}
	appendPairs(base, false)
	appendPairs(browserCookie, true)
	return joinCookieParams(merged)
}

// HasMemberIdentityCookie 判断是否包含真正的成员登录 Cookie（非 ctoken 占位）。
func HasMemberIdentityCookie(cookieHeader string) bool {
	lower := strings.ToLower(strings.TrimSpace(cookieHeader))
	return strings.Contains(lower, "ieu_member_uid=") ||
		strings.Contains(lower, "ieu_member_sid=") ||
		strings.Contains(lower, "jym_session_id=")
}
