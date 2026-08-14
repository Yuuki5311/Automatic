package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const profileEstablishedMarker = ".thirdpartysync-established"

// 启动前清理的 Chrome 单例锁文件名。
var chromeProfileSingletonLockNames = []string{
	"SingletonLock", "SingletonCookie", "SingletonSocket", "lockfile",
}

// 启动前可安全删除的会话恢复文件（保留 Cookie/登录态）。
var browserSessionRestoreRelativePaths = []string{
	"Default/Sessions",
	"Default/Current Session",
	"Default/Current Tabs",
	"Default/Last Session",
	"Default/Last Tabs",
	"Last Browser",
}

// Default 子目录下可删的缓存（关闭后 slim，保留 Cookie）。
var browserProfileDefaultCacheDirNames = []string{
	"Cache", "Code Cache", "GPUCache", "DawnCache", "DawnWebGPUCache",
	"Service Worker", "blob_storage", "File System",
	"optimization_guide_hint_cache_store", "Shared Dictionary", "Media Cache",
}

// Profile 根目录下可删的缓存。
var browserProfileRootCacheDirNames = []string{
	"GrShaderCache", "ShaderCache", "GraphiteDawnCache", "BrowserMetrics",
	"Crashpad", "component_crx_cache", "extensions_crx_cache",
	"optimization_guide_model_store", "segmentation_platform", "Safe Browsing",
}

// StoreProfileDir 返回店铺复用 profile 路径。
// 单槽：profile；多槽：profile-0、profile-1...
func StoreProfileDir(profileRoot, platform string, storeID int64, slot, totalSlots int) string {
	name := "profile"
	if totalSlots > 1 {
		name = "profile-" + strconv.Itoa(slot)
	}
	return filepath.Join(profileRoot, platform, strconv.FormatInt(storeID, 10), name)
}

// TaskProfileDir 返回按任务隔离的 profile 路径。
func TaskProfileDir(profileRoot, platform string, storeID, taskID int64) string {
	taskSegment := strconv.FormatInt(taskID, 10)
	if taskID == 0 {
		taskSegment = "runtime-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return filepath.Join(profileRoot, platform, strconv.FormatInt(storeID, 10), "tasks", taskSegment)
}

// PrepareProfileForLaunch 启动前清理会话恢复残留与单例锁。
func PrepareProfileForLaunch(profileDir string) {
	clearBrowserSessionRestoreArtifacts(profileDir)
	clearBrowserProfileSingletonLocks(profileDir)
}

func clearBrowserProfileSingletonLocks(profileDir string) int {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return 0
	}
	removed := 0
	for _, name := range chromeProfileSingletonLockNames {
		path := filepath.Join(profileDir, name)
		if err := os.Remove(path); err == nil {
			removed++
			continue
		}
		if err := os.RemoveAll(path); err == nil {
			removed++
		}
	}
	return removed
}

func clearBrowserSessionRestoreArtifacts(profileDir string) {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return
	}
	for _, rel := range browserSessionRestoreRelativePaths {
		_, _ = removeBrowserProfileCachePath(filepath.Join(profileDir, filepath.FromSlash(rel)))
	}
	_ = markBrowserProfileExitNormal(filepath.Join(profileDir, "Default", "Preferences"))
	_ = markBrowserProfileExitNormal(filepath.Join(profileDir, "Local State"))
}

func markBrowserProfileExitNormal(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		return err
	}
	changed := false
	profile, _ := root["profile"].(map[string]any)
	if profile == nil {
		profile = map[string]any{}
		root["profile"] = profile
	}
	if exitType, _ := profile["exit_type"].(string); !strings.EqualFold(strings.TrimSpace(exitType), "Normal") {
		profile["exit_type"] = "Normal"
		changed = true
	}
	session, _ := root["session"].(map[string]any)
	if session == nil {
		if strings.EqualFold(filepath.Base(path), "Preferences") {
			root["session"] = map[string]any{"restore_on_startup": float64(5)}
			changed = true
		}
	} else if restore, ok := session["restore_on_startup"].(float64); !ok || int(restore) == 1 {
		session["restore_on_startup"] = float64(5)
		changed = true
	}
	if !changed {
		return nil
	}
	out, err := json.Marshal(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// SlimProfileDir 删除 profile 大体积缓存，保留 Cookie 与 Local Storage。
func SlimProfileDir(profileDir string) (int, error) {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return 0, nil
	}
	removed := 0
	for _, name := range browserProfileRootCacheDirNames {
		if n, _ := removeBrowserProfileCachePath(filepath.Join(profileDir, name)); n > 0 {
			removed += n
		}
	}
	defaultDir := filepath.Join(profileDir, "Default")
	for _, name := range browserProfileDefaultCacheDirNames {
		if n, _ := removeBrowserProfileCachePath(filepath.Join(defaultDir, name)); n > 0 {
			removed += n
		}
	}
	return removed, nil
}

func removeBrowserProfileCachePath(path string) (int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if info.IsDir() {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return 1, nil
}

// IsProfileEstablished 判断 profile 是否已成功登录过。
func IsProfileEstablished(profileDir string) bool {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(profileDir, profileEstablishedMarker)); err == nil {
		return true
	}
	info, err := os.Stat(filepath.Join(profileDir, "Default", "Cookies"))
	return err == nil && info.Size() > 0
}

// MarkProfileEstablished 标记 profile 已成功登录。
func MarkProfileEstablished(profileDir string) {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return
	}
	_ = os.MkdirAll(profileDir, 0o755)
	f, err := os.OpenFile(filepath.Join(profileDir, profileEstablishedMarker), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString("1\n")
	_ = f.Close()
}

// MapBrowserLaunchError 把 profile 占用类启动失败转成可读中文错误。
func MapBrowserLaunchError(err error, profileDir string) error {
	if err == nil || !IsBrowserProfileSessionBusyError(err) {
		return err
	}
	if strings.TrimSpace(profileDir) == "" {
		profileDir = "-"
	}
	return fmt.Errorf(
		"浏览器启动失败：当前 Profile 仍被其他浏览器进程占用。profile=%s；原始错误: %w",
		profileDir, err,
	)
}
