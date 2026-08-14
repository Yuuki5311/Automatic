package catslider

import (
	"context"
)

// BrowserRestartFunc 关闭并重新拉起浏览器会话（punish 页风控用）。
type BrowserRestartFunc func(ctx context.Context) error

type browserRestartKey struct{}

// PunishRestartState 记录 punish 整页风控是否已尝试过浏览器重启。
type PunishRestartState struct {
	Attempted bool
}

type punishRestartStateKey struct{}

// WithBrowserRestart 注入 punish 页浏览器重启回调。
func WithBrowserRestart(ctx context.Context, fn BrowserRestartFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, browserRestartKey{}, fn)
}

// WithPunishRestartState 注入 punish 页浏览器重启状态，供同一任务内复用。
func WithPunishRestartState(ctx context.Context, state *PunishRestartState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, punishRestartStateKey{}, state)
}

func browserRestartFromContext(ctx context.Context) BrowserRestartFunc {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(browserRestartKey{}).(BrowserRestartFunc)
	return fn
}

func punishRestartStateFromContext(ctx context.Context) *PunishRestartState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(punishRestartStateKey{}).(*PunishRestartState)
	return state
}
