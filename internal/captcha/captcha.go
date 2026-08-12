// Package captcha 提供登录页面验证码的识别与处理能力。
//
// 目前支持两种策略：
//   - SliderSolver：基于图像边缘检测的滑块验证码求解（纯 Go 实现，不依赖 gocv）
//   - ThirdPartySolver：第三方打码平台备用方案（超级鹰 / 2captcha）
//
// 两者均实现 Solver 接口，调用方可根据 config.CaptchaConfig.Provider 选择。
package captcha

import "context"

// Solver 验证码识别器接口。
type Solver interface {
	// Detect 检测页面是否出现了验证码。
	Detect(ctx context.Context) (bool, error)
	// Solve 识别并解决验证码。
	Solve(ctx context.Context) error
	// Type 返回验证码类型（如 "slider"、"thirdparty:chaojiying"）。
	Type() string
}
