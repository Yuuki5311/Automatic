package status

import (
	"sync"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// DaemonState 守护进程状态。
type DaemonState string

const (
	DaemonStopped DaemonState = "stopped"
	DaemonRunning DaemonState = "running"
)

// Phase 抓取流程当前阶段。
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseCookie   Phase = "cookie"
	PhaseScraping Phase = "scraping"
	PhaseSyncing  Phase = "syncing"
)

// AccountStatus 单账号运行与 Cookie 状态（供 UI 轮询）。
type AccountStatus struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Enabled     bool      `json:"enabled"`
	LastStatus  string    `json:"last_status"`
	LastError   string    `json:"last_error,omitempty"`
	LastRunAt   time.Time `json:"last_run_at,omitempty"`
	CookieValid bool      `json:"cookie_valid"`
	HasPassword bool      `json:"has_password"`
}

// Snapshot 状态快照（JSON 序列化给前端）。
type Snapshot struct {
	DaemonState DaemonState     `json:"daemon_state"`
	Phase       Phase           `json:"phase"`
	LoginPhase  LoginPhase      `json:"login_phase"` // 独立登录进度（与抓取 phase 解耦）
	LoginError  string          `json:"login_error,omitempty"`
	Cookie      CookieInfo      `json:"cookie"`
	CurrentRun  *RunInfo        `json:"current_run,omitempty"`
	LastRun     *RunInfo        `json:"last_run,omitempty"`
	Games       []GameResult    `json:"games"`
	Accounts    []AccountStatus `json:"accounts,omitempty"`
	ServerTime  time.Time       `json:"server_time"`
}

// LoginPhase UI 触发的登录进度。
type LoginPhase string

const (
	LoginIdle    LoginPhase = "idle"
	LoginRunning LoginPhase = "running"
	LoginSuccess LoginPhase = "success"
	LoginFailed  LoginPhase = "failed"
)

// CookieInfo Cookie 状态。
type CookieInfo struct {
	Present       bool      `json:"present"`
	Valid         bool      `json:"valid"`
	MaskedValue   string    `json:"masked_value"`
	ExpiresAt     time.Time `json:"expires_at"`
	RemainingSecs int64     `json:"remaining_seconds"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RunInfo 单次抓取运行信息。
type RunInfo struct {
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at"`
	DurationSec     float64   `json:"duration_seconds"`
	Success         bool      `json:"success"`
	TotalOrders     int       `json:"total_orders"` // 看板路径：成功游戏数
	OkAccounts      int       `json:"ok_accounts,omitempty"`
	SkippedAccounts int       `json:"skipped_accounts,omitempty"`
	StatsDate       string    `json:"stats_date,omitempty"`
	Error           string    `json:"error,omitempty"`
}

// GameResult 单表抓取+飞书写入的完整结果。
type GameResult struct {
	TableKey        string               `json:"table_key"`
	GameName        string               `json:"game_name"`
	Success         bool                 `json:"success"`
	RecordCount     int                  `json:"record_count"`
	Metrics         []models.BoardMetric `json:"metrics,omitempty"`
	Error           string               `json:"error,omitempty"`
	FeishuSynced    bool                 `json:"feishu_synced"`
	FeishuNewCount  int                  `json:"feishu_new,omitempty"`
	FeishuUpdCount  int                  `json:"feishu_updated,omitempty"`
	FeishuSyncError string               `json:"feishu_sync_error,omitempty"`
}

// FeishuSyncResult 单表飞书写入结果。
type FeishuSyncResult struct {
	NewCount int
	UpdCount int
	Err      error
}

// Store 线程安全的状态存储器。
type Store struct {
	mu   sync.RWMutex
	snap Snapshot
}

// NewStore 创建状态存储器。
func NewStore() *Store {
	return &Store{
		snap: Snapshot{
			DaemonState: DaemonStopped,
			Phase:       PhaseIdle,
			LoginPhase:  LoginIdle,
		},
	}
}

// SetLoginPhase 设置 UI 触发的登录进度。
func (s *Store) SetLoginPhase(p LoginPhase, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.LoginPhase = p
	s.snap.LoginError = errMsg
}

// SetDaemonState 设置守护进程状态。
func (s *Store) SetDaemonState(st DaemonState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.DaemonState = st
}

// SetPhase 设置当前阶段。
func (s *Store) SetPhase(p Phase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Phase = p
}

// SetCookie 更新 Cookie 状态（masked 值，不记录原始值）。
func (s *Store) SetCookie(data *models.CookieData, valid bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := CookieInfo{Valid: valid}
	if data != nil {
		c.Present = true
		c.MaskedValue = maskCookieValue(data)
		c.ExpiresAt = data.ExpiresAt
		c.UpdatedAt = data.UpdatedAt
	}
	s.snap.Cookie = c
}

// RunStarted 标记一轮抓取开始，清空上一轮的游戏结果。
func (s *Store) RunStarted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.CurrentRun = &RunInfo{StartedAt: time.Now()}
	s.snap.Games = nil
	s.snap.Phase = PhaseCookie
}

// RunFinished 标记抓取完成，CurrentRun 移至 LastRun。
// okAccounts / skippedAccounts 为本轮账户结果；无账户语义的路径传 0, 0。
func (s *Store) RunFinished(err error, totalOrders, okAccounts, skippedAccounts int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap.CurrentRun == nil {
		return
	}
	r := s.snap.CurrentRun
	r.EndedAt = time.Now()
	r.DurationSec = r.EndedAt.Sub(r.StartedAt).Seconds()
	r.TotalOrders = totalOrders
	r.OkAccounts = okAccounts
	r.SkippedAccounts = skippedAccounts
	if err != nil {
		r.Success = false
		r.Error = err.Error()
	} else {
		r.Success = true
	}
	s.snap.LastRun = r
	s.snap.CurrentRun = nil
	s.snap.Phase = PhaseIdle
}

// SetStatsDate 写入当前轮次的看板统计日期（昨日 YYYY-MM-DD）。
func (s *Store) SetStatsDate(date string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap.CurrentRun != nil {
		s.snap.CurrentRun.StatsDate = date
	}
}

// RecordGame 记录单个表格的抓取结果。
func (s *Store) RecordGame(tableKey string, count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gr := GameResult{
		TableKey:    tableKey,
		GameName:    gameName(tableKey),
		RecordCount: count,
	}
	if err != nil {
		gr.Error = err.Error()
	} else {
		gr.Success = true
	}
	s.snap.Games = append(s.snap.Games, gr)
}

// RecordGameStats 按游戏名 upsert 看板指标结果。
func (s *Store) RecordGameStats(gameName string, metrics []models.BoardMetric, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gr := GameResult{
		TableKey:    gameName,
		GameName:    gameName,
		RecordCount: len(metrics),
		Metrics:     metrics,
	}
	if err != nil {
		gr.Error = err.Error()
	} else {
		gr.Success = true
	}
	for i := range s.snap.Games {
		if s.snap.Games[i].GameName == gameName {
			s.snap.Games[i] = gr
			return
		}
	}
	s.snap.Games = append(s.snap.Games, gr)
}

// SetAccounts 更新账号列表快照（由 pipeline/web 刷新写入）。
func (s *Store) SetAccounts(list []AccountStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if list == nil {
		s.snap.Accounts = nil
		return
	}
	accounts := make([]AccountStatus, len(list))
	copy(accounts, list)
	s.snap.Accounts = accounts
}

// RecordGameSync 更新某张表的飞书写入结果（按 tableKey 匹配）。
func (s *Store) RecordGameSync(tableKey string, result FeishuSyncResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.snap.Games {
		if s.snap.Games[i].TableKey == tableKey {
			s.snap.Games[i].FeishuSynced = true
			s.snap.Games[i].FeishuNewCount = result.NewCount
			s.snap.Games[i].FeishuUpdCount = result.UpdCount
			if result.Err != nil {
				s.snap.Games[i].FeishuSyncError = result.Err.Error()
			}
			return
		}
	}
}

// Snapshot 返回当前状态快照（实时计算 RemainingSecs）。
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := s.snap
	snap.ServerTime = time.Now()
	snap.Cookie.RemainingSecs = int64(snap.Cookie.ExpiresAt.Sub(snap.ServerTime).Seconds())
	// 深拷贝 Games 切片
	if snap.Games != nil {
		games := make([]GameResult, len(snap.Games))
		copy(games, snap.Games)
		snap.Games = games
	}
	if snap.CurrentRun != nil {
		cr := *snap.CurrentRun
		snap.CurrentRun = &cr
	}
	if snap.LastRun != nil {
		lr := *snap.LastRun
		snap.LastRun = &lr
	}
	if snap.Accounts != nil {
		accounts := make([]AccountStatus, len(snap.Accounts))
		copy(accounts, snap.Accounts)
		snap.Accounts = accounts
	}
	return snap
}

// maskCookieValue 返回关键 Cookie 的脱敏值（前6 + … + 后4）。
func maskCookieValue(data *models.CookieData) string {
	if data == nil || len(data.Cookies) == 0 {
		return ""
	}
	// 优先取 session cookie（与 auth.IsCookieValid 同逻辑）
	names := []string{"_m_h5_tk", "_m_h5_tk_enc", "token", "SESSION", "jym_token"}
	for _, name := range names {
		for _, c := range data.Cookies {
			if c.Name == name && c.Value != "" {
				return mask(c.Value)
			}
		}
	}
	// 兜底：取第一个有值的 cookie
	for _, c := range data.Cookies {
		if c.Value != "" {
			return mask(c.Value)
		}
	}
	return ""
}

func mask(s string) string {
	if len(s) <= 10 {
		return s[:1] + "…"
	}
	return s[:6] + "…" + s[len(s)-4:]
}

// gameName 从 tableKey 提取游戏名（去掉 _tableN 后缀）。
func gameName(tableKey string) string {
	for i := len(tableKey) - 1; i >= 0; i-- {
		if tableKey[i] == '_' {
			return tableKey[:i]
		}
	}
	return tableKey
}
