// Package browser 基于 go-rod 封装 Chrome/Edge 任务浏览器。
//
// 启动流程：Manager.Launch → 申请 Profile 槽 → reservePort → NewAppMode → Connect
// Profile 关闭后：保留模式下 slim 缓存（删 Cache，保留 Cookie）
//
// 主项目对应：internal/browser/
package browser
