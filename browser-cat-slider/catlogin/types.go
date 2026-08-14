// Package catlogin 提供交易猫（jiaoyimao）浏览器模拟登录参考实现。
//
// 登录流程概览：
//
//  1. （可选）注入历史登录 Cookie，尝试免密进入工作台
//  2. 打开 merchant 工作台入口，站点可能重定向到登录页或风控页
//  3. 检测是否已登录（页面上出现 span.nickname）
//  4. 若未登录：等待进入密码登录表单，或先处理入口滑块
//  5. 填写手机号/密码 → 勾选协议 → 点击「立即登录」
//  6. 轮询等待：登录成功 / 吐司失败 / 登录滑块 → 滑块通过后补填并重试
//  7. 登录成功后从浏览器读取并过滤 Cookie，返回给调用方保存
//
// 不包含：后端 HTTP 接口、MQ、Redis 店铺锁、远端 Cookie 同步等主项目基础设施。
//
// 主项目对应：internal/platform/cat_login.go、cat_browser_cookies.go、cat_login_nav.go
package catlogin

import "time"

// 交易猫 merchant 站点常量。
const (
	MerchantBaseURL      = "https://merchant.jiaoyimao.com"
	MerchantWorkBenchURL = MerchantBaseURL + "/workbench"
	MerchantCookieDomain = ".jiaoyimao.com"
)

// 登录轮询与超时配置（与主项目 cat_login.go 保持一致）。
const (
	PollInterval     = 500 * time.Millisecond // 普通轮询间隔
	FastPollInterval = 150 * time.Millisecond // 已在登录页时的加速轮询
	LoginTimeout     = 20 * time.Second       // 单次等待登录结果的基础超时
	FullRetryCount   = 3                      // 整页刷新后重头登录的最大次数
	PasswordWaitTime = 3 * time.Second        // 点击「密码登录」后等待表单出现
)

// Credentials 登录所需凭据；Cookie 与账号密码可二选一或组合使用。
type Credentials struct {
	// Account 店铺登录手机号。
	Account string
	// Password 店铺登录密码。
	Password string
	// Cookie 历史登录 Cookie 请求头文本（name=value;name2=value2）。
	Cookie string
	// CookieSnapshot 浏览器 Cookie JSON 快照，优先于 Cookie 文本解析。
	CookieSnapshot string
}

// LoginResult 登录成功后的输出；调用方可将 Cookie 持久化到本地配置。
type LoginResult struct {
	// Cookie 过滤后的登录身份 Cookie 请求头（不含 isg/tfstk 等风控字段）。
	Cookie string
	// CookieSnapshot 同上内容的 JSON 快照，便于下次精确注入。
	CookieSnapshot string
	// Nickname 工作台显示的店铺昵称，用于判断登录成功。
	Nickname string
	// Remark 人类可读的登录结果说明。
	Remark string
}

// pageState 内部使用的页面状态枚举。
type pageState int

const (
	pageStateUnknown pageState = iota
	pageStateSuccess
	pageStateLoginForm
)
