package captcha

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/config"
)

// 编译期断言：ThirdPartySolver 实现 Solver 接口。
var _ Solver = (*ThirdPartySolver)(nil)

// ThirdPartySolver 通过第三方打码平台（超级鹰 / 2captcha）解决滑块验证码，
// 作为本地图像识别的备用方案。
type ThirdPartySolver struct {
	apiKey   string // 打码平台密钥
	provider string // "chaojiying" | "2captcha"
	client   *http.Client
}

// NewThirdPartySolver 创建第三方打码平台求解器。
func NewThirdPartySolver(cfg *config.CaptchaConfig) *ThirdPartySolver {
	provider := "chaojiying"
	apiKey := ""
	if cfg != nil {
		if cfg.Provider != "" {
			provider = cfg.Provider
		}
		apiKey = cfg.APIKey
	}
	return &ThirdPartySolver{
		provider: provider,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Type 返回验证码类型。
func (t *ThirdPartySolver) Type() string { return "thirdparty:" + t.provider }

// Detect 复用 SliderSolver 的检测逻辑，判断页面是否出现滑块验证码。
func (t *ThirdPartySolver) Detect(ctx context.Context) (bool, error) {
	return detectSliderCaptcha(ctx)
}

// Solve 根据 provider 调用对应打码平台完成验证码求解。
func (t *ThirdPartySolver) Solve(ctx context.Context) error {
	switch t.provider {
	case "chaojiying":
		return t.solveChaojiying(ctx)
	case "2captcha":
		return t.solve2Captcha(ctx)
	default:
		return fmt.Errorf("不支持的验证码平台: %s", t.provider)
	}
}

// solveChaojiying 超级鹰 API 调用：上传背景图+缺口图，获取滑动距离。
//
// 接口骨架。完整调用需按超级鹰最新文档以 multipart/form-data 提交
// user/pass/softid/userfile 参数（codetype=9101 为滑块验证码类型），
// 并从响应 JSON 的 err_no==0 结果中解析出滑动距离。当前配置仅提供
// api_key，缺少 user/pass/softid 凭据，故显式返回错误而非误报成功。
func (t *ThirdPartySolver) solveChaojiying(ctx context.Context) error {
	if t.apiKey == "" {
		return fmt.Errorf("未配置第三方打码平台 api_key")
	}
	// POST https://upload.chaojiying.net/Upload/Processing.php
	// 参数: user, pass, softid, codetype, userfile
	// 具体实现需根据超级鹰最新文档调整
	return fmt.Errorf("超级鹰打码平台集成待实现: 需要 user/pass/softid 凭据（当前配置仅提供 api_key）")
}

// solve2Captcha 2captcha API 调用（预留）。
func (t *ThirdPartySolver) solve2Captcha(ctx context.Context) error {
	return fmt.Errorf("2captcha 集成待实现")
}
