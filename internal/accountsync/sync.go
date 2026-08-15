// Package accountsync 从 leyoo 列表同步店铺 Cookie 到本地账户库。
package accountsync

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/example/jiaoyimao-scraper/credentialcrypto"
	"github.com/example/jiaoyimao-scraper/internal/accounts"
	"github.com/example/jiaoyimao-scraper/internal/auth"
	"github.com/example/jiaoyimao-scraper/internal/config"
	"github.com/example/jiaoyimao-scraper/internal/leyoo"
	"github.com/example/jiaoyimao-scraper/internal/models"
)

// CryptoFromConfig 按配置或环境变量创建解密客户端；未配置返回 nil。
func CryptoFromConfig(cfg *config.Config) *credentialcrypto.Client {
	if cfg != nil {
		if key := strings.TrimSpace(cfg.Credential.AESKey); key != "" {
			c, err := credentialcrypto.New([]byte(key))
			if err != nil {
				slog.Warn("credential.aes_key 无效", "component", "accountsync", "error", err)
			} else {
				return c
			}
		}
	}
	c, err := credentialcrypto.NewFromEnv()
	if err != nil {
		return nil
	}
	return c
}

// SyncCatBySupplier 拉取 leyoo cat 列表并写入账户库（解密手机号、导入 Cookie）。
func SyncCatBySupplier(store *accounts.Store, client *leyoo.Client, crypto *credentialcrypto.Client, supplierID int) (kept int, err error) {
	if store == nil {
		return 0, fmt.Errorf("账户库为空")
	}
	if client == nil {
		client = leyoo.NewClient("")
	}
	if supplierID <= 0 {
		supplierID = 1
	}
	list, err := client.ListCatBySupplier(supplierID)
	if err != nil {
		return 0, err
	}
	for _, remote := range list {
		thirdAcct := remote.ThirdAccount
		thirdPass := remote.ThirdPassword
		if crypto != nil {
			thirdAcct = crypto.TryDecrypt(remote.ThirdAccount)
			thirdPass = crypto.TryDecrypt(remote.ThirdPassword)
		}
		phone := accounts.AccountPhoneFromRemote(remote.Mobile, thirdAcct)
		if phone == "" {
			slog.Info("跳过非11位手机号店铺", "component", "accountsync", "external_id", remote.ID, "name", remote.Name)
			continue
		}
		acct, _, upsertErr := store.UpsertFromRemoteKey(
			phone, thirdPass, remote.Name,
			remote.ID, remote.SupplierID, remote.PlatformKey,
		)
		if upsertErr != nil {
			slog.Warn("同步店铺失败", "component", "accountsync", "phone", phone, "external_id", remote.ID, "error", upsertErr)
			continue
		}
		if err := auth.ImportFromHeader(acct.CookiePath, remote.Cookie, ".jiaoyimao.com"); err != nil {
			_ = store.SetEnabled(acct.ID, false, "Cookie 导入失败: "+err.Error())
			continue
		}
		cookies, _ := auth.LoadCookies(acct.CookiePath)
		if !auth.IsCookieValid(cookies) {
			_ = store.SetEnabled(acct.ID, false, "Cookie 无效")
			continue
		}
		// 拉取到有效 Cookie 时，仅恢复因 Cookie 问题自动禁用的账户（保留手动禁用）
		if !acct.Enabled && strings.Contains(acct.LastError, "Cookie") {
			_ = store.SetEnabled(acct.ID, true, "")
		}
		kept++
	}
	if n, err := store.PruneInvalidPhones(); err != nil {
		slog.Warn("清理无效手机号账户失败", "component", "accountsync", "error", err)
	} else if n > 0 {
		slog.Info("已清理非11位手机号账户", "component", "accountsync", "removed", n)
	}
	return kept, nil
}

// SyncExistingSuppliers 对账户库中出现过的 supplier_id（默认含 1）各拉一次列表。
func SyncExistingSuppliers(store *accounts.Store, client *leyoo.Client, crypto *credentialcrypto.Client) (total int, err error) {
	if store == nil {
		return 0, fmt.Errorf("账户库为空")
	}
	ids := map[int]struct{}{1: {}}
	for _, a := range store.List() {
		if a.SupplierID > 0 {
			ids[a.SupplierID] = struct{}{}
		}
	}
	var firstErr error
	for sid := range ids {
		n, e := SyncCatBySupplier(store, client, crypto, sid)
		total += n
		if e != nil && firstErr == nil {
			firstErr = e
			slog.Warn("启动拉取店铺列表失败", "component", "accountsync", "supplier_id", sid, "error", e)
		} else {
			slog.Info("已同步店铺 Cookie", "component", "accountsync", "supplier_id", sid, "kept", n)
		}
	}
	return total, firstErr
}

// RefreshAccountCookie 针对单账户重新拉取其 supplier 列表并更新该店 Cookie。
func RefreshAccountCookie(store *accounts.Store, client *leyoo.Client, crypto *credentialcrypto.Client, acct accounts.Account) (*models.CookieData, error) {
	if store == nil {
		return nil, fmt.Errorf("账户库为空")
	}
	sid := acct.SupplierID
	if sid <= 0 {
		sid = 1
	}
	if _, err := SyncCatBySupplier(store, client, crypto, sid); err != nil {
		return nil, err
	}
	// 同步后按 id 重新取路径（用户名可能变化，但 ExternalID 稳定）
	fresh, ok := store.FindByExternalID(acct.ExternalID)
	if !ok {
		fresh, ok = store.Get(acct.ID)
		if !ok {
			return nil, fmt.Errorf("重新拉取后找不到账户")
		}
	}
	cookies, err := auth.LoadCookies(fresh.CookiePath)
	if err != nil {
		return nil, err
	}
	if !auth.IsCookieValid(cookies) {
		return cookies, fmt.Errorf("重新拉取后 Cookie 仍无效")
	}
	return cookies, nil
}
