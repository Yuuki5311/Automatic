// 独立参考模块：浏览器启动 + 交易猫登录 + 滑块验证。
//
// 详见各子包 doc 注释：
//   - browser/   浏览器与 Profile
//   - catlogin/  交易猫登录（Cookie 注入、填表、滑块）
//   - catslider/ 交易猫滑块检测与拖动
//
// 滑块轨迹：参考模块已内置 84 条 *.txt，路径为
// reference/browser-cat-slider/runtime/data/cat-slider-traces/
// （从主项目 runtime/data/cat-slider-traces 复制；主项目 .gitignore 未入库）
//
// 快速示例：
//
//	import (
//	    "context"
//	    refbrowser "reference/browser-cat-slider/browser"
//	    "reference/browser-cat-slider/catlogin"
//	    "reference/browser-cat-slider/catslider"
//	)
//
//	func run(ctx context.Context) error {
//	    mgr := refbrowser.NewManager(refbrowser.DefaultConfig())
//	    sess, err := mgr.Launch(ctx, "cat", storeID, 0, catlogin.MerchantWorkBenchURL)
//	    if err != nil { return err }
//	    defer sess.Close()
//
//	    punishState := &catslider.PunishRestartState{}
//	    ctx = catslider.WithPunishRestartState(ctx, punishState)
//	    ctx = catslider.WithBrowserRestart(ctx, func(restartCtx context.Context) error {
//	        sess.Close()
//	        newSess, err := mgr.Launch(restartCtx, "cat", storeID, 0, catlogin.MerchantWorkBenchURL)
//	        if err != nil { return err }
//	        *sess = *newSess
//	        return nil
//	    })
//
//	    result, err := catlogin.Login(ctx, sess, catlogin.Credentials{
//	        Account: "13800138000", Password: "secret",
//	        Cookie: "ieu_member_uid=...", // 可选
//	    })
//	    if err != nil { return err }
//	    _ = result.Cookie // 保存到本地配置供下次注入
//	    return nil
//	}
package browsercatsliderref
