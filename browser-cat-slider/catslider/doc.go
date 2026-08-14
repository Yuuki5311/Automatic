// Package catslider 实现交易猫滑块/验证码的检测、锁定、重试与拖动。
//
// # 两种滑块 + punish 整页
//
//   - dialog：弹窗 #baxia-dialog-content
//   - inline：页面内 .nc_scale + .btn_slide
//   - punish_page：URL 含 punish 或 ____tmd____，失败后可能需重启浏览器
//
// # 拖动策略
//
//  1. 优先从 runtime/data/cat-slider-traces/*.txt 加载人工轨迹并缩放回放
//     参考模块内置目录：reference/browser-cat-slider/runtime/data/cat-slider-traces/（84 条）
//  2. 无轨迹时使用 cubic ease-out + 微抖动算法（easing fallback）
//  3. 全局 mutex 串行化拖动，避免多协程共用风控 Cookie
//
// 主项目对应：internal/platform/cat_slider_*.go
package catslider
