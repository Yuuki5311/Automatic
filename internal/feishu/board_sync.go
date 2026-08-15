package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// 看板统计多维表格字段（与飞书列名一致）
const (
	boardFieldDate         = "日期"
	boardFieldAccount      = "账号"
	boardFieldShopName     = "店铺名"
	boardFieldUID          = "UID"
	boardFieldGame         = "游戏名称"
	boardFieldConsult      = "咨询量"
	boardFieldQuote        = "发起报价量"
	boardFieldOrderCount   = "回收成功订单数"
	boardFieldAmount       = "回收成功金额"
	boardFieldSuccessRate  = "回收成功率"
	boardFieldSatisfaction = "回收满意度"
	boardFieldHomeConsult  = "回收主页咨询量"
	boardFieldHomeOrders   = "回收主页成功订单数"
	boardFieldScrapedAt    = "抓取时间"
	boardFieldError        = "错误信息"
)

// Feishu field type
const (
	fieldTypeText     = 1
	fieldTypeNumber   = 2
	fieldTypeDatetime = 5
)

type boardFieldDef struct {
	Name string
	Type int
}

var boardFieldDefs = []boardFieldDef{
	{boardFieldDate, fieldTypeDatetime},
	{boardFieldAccount, fieldTypeText},
	{boardFieldShopName, fieldTypeText},
	{boardFieldUID, fieldTypeText},
	{boardFieldGame, fieldTypeText},
	{boardFieldConsult, fieldTypeNumber},
	{boardFieldQuote, fieldTypeNumber},
	{boardFieldOrderCount, fieldTypeNumber},
	{boardFieldAmount, fieldTypeNumber},
	{boardFieldSuccessRate, fieldTypeNumber},
	{boardFieldSatisfaction, fieldTypeNumber},
	{boardFieldHomeConsult, fieldTypeNumber},
	{boardFieldHomeOrders, fieldTypeNumber},
	{boardFieldScrapedAt, fieldTypeText},
	{boardFieldError, fieldTypeText},
}

// metric title → 飞书列名
var metricTitleToField = map[string]string{
	"咨询量":       boardFieldConsult,
	"发起报价量":     boardFieldQuote,
	"带看量":       boardFieldQuote, // 旧列名别名，避免历史映射落空
	"回收成功订单数":   boardFieldOrderCount,
	"回收成功金额":    boardFieldAmount,
	"回收成功率":     boardFieldSuccessRate,
	"回收满意度":     boardFieldSatisfaction,
	"回收主页咨询量":   boardFieldHomeConsult,
	"回收主页成功订单数": boardFieldHomeOrders,
}

// EnsureBoardFields 列出表字段，缺失则创建。
func (b *BitableOps) EnsureBoardFields(ctx context.Context, tableID string) error {
	existing, err := b.listFieldNames(ctx, tableID)
	if err != nil {
		return err
	}
	for _, def := range boardFieldDefs {
		if existing[def.Name] {
			continue
		}
		path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/fields", b.bitableID, tableID)
		body := map[string]interface{}{
			"field_name": def.Name,
			"type":       def.Type,
		}
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := b.client.doRequest(ctx, http.MethodPost, path, body, &result); err != nil {
			return fmt.Errorf("创建字段 %q 失败: %w", def.Name, err)
		}
		slog.Info("已创建飞书字段", "component", "feishu", "field", def.Name, "type", def.Type)
		existing[def.Name] = true
	}
	return nil
}

func (b *BitableOps) listFieldNames(ctx context.Context, tableID string) (map[string]bool, error) {
	path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/fields?page_size=100", b.bitableID, tableID)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items []struct {
				FieldName string `json:"field_name"`
			} `json:"items"`
			HasMore   bool   `json:"has_more"`
			PageToken string `json:"page_token"`
		} `json:"data"`
	}
	names := make(map[string]bool)
	pageToken := ""
	for {
		p := path
		if pageToken != "" {
			p += "&page_token=" + pageToken
		}
		if err := b.client.doRequest(ctx, http.MethodGet, p, nil, &result); err != nil {
			return nil, fmt.Errorf("列出字段失败: %w", err)
		}
		for _, it := range result.Data.Items {
			if it.FieldName != "" {
				names[it.FieldName] = true
			}
		}
		if !result.Data.HasMore || result.Data.PageToken == "" || result.Data.PageToken == pageToken {
			break
		}
		pageToken = result.Data.PageToken
	}
	return names, nil
}

// SyncBoardStats 将看板快照写入多维表格：先建字段，再按「年月+账号+游戏」upsert。
// 同月覆盖更新（「日期」写为本次抓取日）；跨月新增一行。
func (b *BitableOps) SyncBoardStats(ctx context.Context, tableID string, snap models.BoardStatsSnapshot) (newCount, updCount int, err error) {
	if tableID == "" {
		return 0, 0, fmt.Errorf("board_table_id 为空")
	}
	if err := b.EnsureBoardFields(ctx, tableID); err != nil {
		return 0, 0, err
	}
	existing, err := b.listAllRecords(ctx, tableID)
	if err != nil {
		slog.Warn("拉取飞书记录失败，全部按新增处理", "component", "feishu", "error", err)
	}
	keyToID := make(map[string]string, len(existing))
	for _, rec := range existing {
		dateKey := boardDateKey(rec.Fields[boardFieldDate])
		account := fieldAsString(rec.Fields[boardFieldAccount])
		game := fieldAsString(rec.Fields[boardFieldGame])
		if dateKey == "" || game == "" {
			continue
		}
		keyToID[boardMonthRecordKey(dateKey, account, game)] = rec.RecordID
	}

	var toCreate []map[string]interface{}
	skipped := 0
	for _, g := range snap.Games {
		if !shouldSyncBoardGame(g) {
			skipped++
			slog.Info("跳过无数据游戏，不写入飞书", "component", "feishu", "account", snap.Account, "game", g.GameName, "error", g.Error, "metrics", len(g.Metrics))
			continue
		}
		fields := boardGameToFields(snap, g)
		key := boardMonthRecordKey(snap.Date, snap.Account, g.GameName)
		if rid, ok := keyToID[key]; ok {
			path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/%s", b.bitableID, tableID, rid)
			var upd struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			}
			if err := b.client.doRequest(ctx, http.MethodPut, path, map[string]interface{}{"fields": fields}, &upd); err != nil {
				slog.Warn("更新看板行失败", "component", "feishu", "game", g.GameName, "error", err)
				continue
			}
			updCount++
			continue
		}
		toCreate = append(toCreate, map[string]interface{}{"fields": fields})
	}

	for i := 0; i < len(toCreate); i += batchCreateLimit {
		end := min(i+batchCreateLimit, len(toCreate))
		batch := toCreate[i:end]
		path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/batch_create", b.bitableID, tableID)
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := b.client.doRequest(ctx, http.MethodPost, path, map[string]interface{}{"records": batch}, &result); err != nil {
			return newCount, updCount, fmt.Errorf("批量写入看板失败: %w", err)
		}
		newCount += len(batch)
	}

	slog.Info("看板飞书同步完成", "component", "feishu", "new", newCount, "updated", updCount, "skipped", skipped, "date", snap.Date)
	return newCount, updCount, nil
}

// ManualBoardMetricFields 手工补充可写的数字指标列（飞书列名）。
func ManualBoardMetricFields() []string {
	return []string{
		boardFieldConsult,
		boardFieldQuote,
		boardFieldOrderCount,
		boardFieldAmount,
		boardFieldSuccessRate,
		boardFieldSatisfaction,
		boardFieldHomeConsult,
		boardFieldHomeOrders,
	}
}

// UpsertBoardManualRow 手工写入/整行覆盖一条看板记录。
// metrics 的 key 为飞书列名；值为 nil 或未出现在允许列表中的键表示该列清空。
// 返回 action 为 "created" 或 "updated"。
func (b *BitableOps) UpsertBoardManualRow(
	ctx context.Context,
	tableID, date, account, shopName, uid, game string,
	metrics map[string]*float64,
	scrapedAt time.Time,
) (action string, err error) {
	if tableID == "" {
		return "", fmt.Errorf("board_table_id 为空")
	}
	date = strings.TrimSpace(date)
	account = strings.TrimSpace(account)
	game = strings.TrimSpace(game)
	if date == "" || account == "" || game == "" {
		return "", fmt.Errorf("日期、账号、游戏均不能为空")
	}
	if err := b.EnsureBoardFields(ctx, tableID); err != nil {
		return "", err
	}
	fields := manualBoardFields(date, account, shopName, uid, game, metrics, scrapedAt)
	existing, listErr := b.listAllRecords(ctx, tableID)
	if listErr != nil {
		slog.Warn("拉取飞书记录失败，手工行按新增处理", "component", "feishu", "error", listErr)
	}
	key := boardMonthRecordKey(date, account, game)
	for _, rec := range existing {
		dateKey := boardDateKey(rec.Fields[boardFieldDate])
		acc := fieldAsString(rec.Fields[boardFieldAccount])
		g := fieldAsString(rec.Fields[boardFieldGame])
		if boardMonthRecordKey(dateKey, acc, g) != key {
			continue
		}
		path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/%s", b.bitableID, tableID, rec.RecordID)
		var upd struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := b.client.doRequest(ctx, http.MethodPut, path, map[string]interface{}{"fields": fields}, &upd); err != nil {
			return "", fmt.Errorf("更新手工看板行失败: %w", err)
		}
		return "updated", nil
	}

	path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/batch_create", b.bitableID, tableID)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	body := map[string]interface{}{
		"records": []map[string]interface{}{{"fields": fields}},
	}
	if err := b.client.doRequest(ctx, http.MethodPost, path, body, &result); err != nil {
		return "", fmt.Errorf("新建手工看板行失败: %w", err)
	}
	return "created", nil
}

func manualBoardFields(date, account, shopName, uid, game string, metrics map[string]*float64, scrapedAt time.Time) map[string]interface{} {
	fields := map[string]interface{}{
		boardFieldAccount: account,
		boardFieldGame:    game,
		boardFieldError:   nil, // 手工补充清空错误信息
	}
	if ms := dateStringToMillis(date); ms > 0 {
		fields[boardFieldDate] = ms
	}
	if shopName != "" {
		fields[boardFieldShopName] = shopName
	} else {
		fields[boardFieldShopName] = nil
	}
	if uid != "" {
		fields[boardFieldUID] = uid
	} else {
		fields[boardFieldUID] = nil
	}
	if scrapedAt.IsZero() {
		scrapedAt = time.Now()
	}
	fields[boardFieldScrapedAt] = scrapedAt.Format("2006-01-02 15:04:05")

	allowed := make(map[string]struct{}, len(ManualBoardMetricFields()))
	for _, name := range ManualBoardMetricFields() {
		allowed[name] = struct{}{}
		fields[name] = nil // 默认清空，再填有值的
	}
	for k, v := range metrics {
		if _, ok := allowed[k]; !ok {
			continue
		}
		if v == nil {
			fields[k] = nil
			continue
		}
		fields[k] = *v
	}
	return fields
}

// shouldSyncBoardGame 仅同步有可写入指标的游戏；失败/无权限/空数据跳过。
func shouldSyncBoardGame(g models.GameBoardStats) bool {
	if strings.TrimSpace(g.Error) != "" {
		return false
	}
	for _, m := range g.Metrics {
		if _, ok := metricTitleToField[m.Title]; !ok {
			continue
		}
		if _, ok := parseMetricNumber(m.Value); ok {
			return true
		}
	}
	return false
}

func boardRecordKey(date, account, game string) string {
	return date + "|" + account + "|" + game
}

// boardYearMonth 取 YYYY-MM；用于同月覆盖判断。
func boardYearMonth(date string) string {
	date = strings.TrimSpace(date)
	if len(date) >= 7 {
		return date[:7]
	}
	return date
}

// boardMonthRecordKey 同月同账号同游戏视为同一行。
func boardMonthRecordKey(date, account, game string) string {
	return boardYearMonth(date) + "|" + account + "|" + game
}

func boardGameToFields(snap models.BoardStatsSnapshot, g models.GameBoardStats) map[string]interface{} {
	fields := map[string]interface{}{
		boardFieldGame:    g.GameName,
		boardFieldAccount: snap.Account,
	}
	if snap.ShopName != "" {
		fields[boardFieldShopName] = snap.ShopName
	}
	if snap.UID != "" {
		fields[boardFieldUID] = snap.UID
	}
	if ms := dateStringToMillis(snap.Date); ms > 0 {
		fields[boardFieldDate] = ms
	}
	if !snap.ScrapedAt.IsZero() {
		fields[boardFieldScrapedAt] = snap.ScrapedAt.Format("2006-01-02 15:04:05")
	}
	if g.Error != "" {
		fields[boardFieldError] = g.Error
	}
	for _, m := range g.Metrics {
		col, ok := metricTitleToField[m.Title]
		if !ok {
			continue
		}
		if n, ok := parseMetricNumber(m.Value); ok {
			fields[col] = n
		}
	}
	return fields
}

func dateStringToMillis(date string) int64 {
	date = strings.TrimSpace(date)
	if date == "" {
		return 0
	}
	loc := time.Local
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func boardDateKey(v interface{}) string {
	switch x := v.(type) {
	case float64:
		return time.UnixMilli(int64(x)).In(time.Local).Format("2006-01-02")
	case int64:
		return time.UnixMilli(x).In(time.Local).Format("2006-01-02")
	case string:
		if len(x) >= 10 {
			return x[:10]
		}
		return x
	default:
		return ""
	}
}

func fieldAsString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func parseMetricNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimSuffix(s, "元")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
