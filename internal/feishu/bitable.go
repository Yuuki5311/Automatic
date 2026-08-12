package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/example/jiaoyimao-scraper/internal/models"
)

// 多维表格字段名（需先在飞书中创建同名字段，与 models.RecycleOrder 字段一一对应）
const (
	fieldOrderID      = "订单编号"
	fieldGameName     = "游戏名称"
	fieldServerRegion = "区服"
	fieldAccountInfo  = "账号信息"
	fieldPrice        = "回收价格"
	fieldStatus       = "订单状态"
	fieldCreateTime   = "创建时间"
	fieldCompleteTime = "完成时间"
	fieldBuyerInfo    = "买家信息"
	fieldRemarks      = "备注"
	fieldUpdatedAt    = "数据更新时间"
)

const batchCreateLimit = 500 // 飞书单次批量创建记录上限

// BitableOps 多维表格（Bitable）操作
type BitableOps struct {
	client    *Client
	bitableID string
}

// NewBitableOps 创建多维表格操作对象
func NewBitableOps(client *Client, bitableID string) *BitableOps {
	return &BitableOps{client: client, bitableID: bitableID}
}

// BatchInsertOrders 批量写入回收订单到多维表格，返回新增和更新的记录数。
// 策略：先分页拉取现有记录，按 OrderID 判断是新增（batch_create）还是更新（PUT 单条记录）。
func (b *BitableOps) BatchInsertOrders(
	ctx context.Context,
	tableID string,
	orders []models.RecycleOrder,
) (newCount, updatedCount int, err error) {
	if len(orders) == 0 {
		return 0, 0, nil
	}

	// 1. 拉取现有记录用于去重（拉取失败时退化为全部按新增处理，保证数据不丢）
	existing, err := b.listAllRecords(ctx, tableID)
	if err != nil {
		slog.Warn("获取现有记录失败", "component", "feishu", "error", err)
	}

	// orderID → recordID 映射
	existingIDs := make(map[string]string, len(existing))
	for _, rec := range existing {
		if id, ok := rec.Fields[fieldOrderID].(string); ok && id != "" {
			existingIDs[id] = rec.RecordID
		}
	}

	// 2. 划分新增 / 更新
	var newRecords, updateRecords []models.RecycleOrder
	for _, order := range orders {
		if _, exists := existingIDs[order.OrderID]; exists {
			updateRecords = append(updateRecords, order)
		} else {
			newRecords = append(newRecords, order)
		}
	}

	// 3. 批量新增（每批最多 500 条，飞书限制）
	for i := 0; i < len(newRecords); i += batchCreateLimit {
		end := min(i+batchCreateLimit, len(newRecords))
		batch := newRecords[i:end]

		records := make([]map[string]interface{}, len(batch))
		for j, order := range batch {
			records[j] = map[string]interface{}{"fields": b.orderToFields(order)}
		}

		path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/batch_create", b.bitableID, tableID)
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Records []struct {
					RecordID string `json:"record_id"`
				} `json:"records"`
			} `json:"data"`
		}
		if err := b.client.doRequest(ctx, http.MethodPost, path,
			map[string]interface{}{"records": records}, &result); err != nil {
			return 0, 0, fmt.Errorf("批量写入记录失败: %w", err)
		}
		slog.Info("批量新增记录", "component", "feishu", "count", len(batch), "table", tableID)
	}

	// 4. 逐条更新已存在的记录（更新失败仅告警，不中断整体流程）
	if len(updateRecords) > 0 {
		for _, order := range updateRecords {
			recordID, ok := existingIDs[order.OrderID]
			if !ok {
				continue // 防御性跳过（第2步已保证存在）
			}
			path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records/%s",
				b.bitableID, tableID, recordID)
			body := map[string]interface{}{"fields": b.orderToFields(order)}
			var updateResult struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
			}
			if err := b.client.doRequest(ctx, http.MethodPut, path, body, &updateResult); err != nil {
				slog.Warn("更新记录失败", "component", "feishu", "order_id", order.OrderID, "error", err)
				continue
			}
		}
		slog.Info("批量更新记录", "component", "feishu", "count", len(updateRecords))
	}

	slog.Info("处理完毕", "component", "feishu", "new", len(newRecords), "updated", len(updateRecords))
	return len(newRecords), len(updateRecords), nil
}

// orderToFields 将回收订单转换为多维表格字段
func (b *BitableOps) orderToFields(order models.RecycleOrder) map[string]interface{} {
	return map[string]interface{}{
		fieldOrderID:      order.OrderID,
		fieldGameName:     order.GameName,
		fieldServerRegion: order.ServerRegion,
		fieldAccountInfo:  order.AccountInfo,
		fieldPrice:        order.Price,
		fieldStatus:       order.Status,
		fieldCreateTime:   formatTime(order.CreateTime),
		fieldCompleteTime: formatTime(order.CompleteTime),
		fieldBuyerInfo:    order.BuyerInfo,
		fieldRemarks:      order.Remarks,
		fieldUpdatedAt:    time.Now().Format("2006-01-02 15:04:05"),
	}
}

// formatTime 时间格式化；零值时间输出空串
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// listAllRecords 分页拉取表格中全部记录（每页最多 500 条），用于去重
func (b *BitableOps) listAllRecords(ctx context.Context, tableID string) ([]models.FeishuRecord, error) {
	var allRecords []models.FeishuRecord
	pageToken := ""

	for {
		path := fmt.Sprintf("/bitable/v1/apps/%s/tables/%s/records?page_size=%d",
			b.bitableID, tableID, batchCreateLimit)
		if pageToken != "" {
			path += "&page_token=" + url.QueryEscape(pageToken)
		}

		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Items     []models.FeishuRecord `json:"items"`
				HasMore   bool                  `json:"has_more"`
				PageToken string                `json:"page_token"`
			} `json:"data"`
		}

		if err := b.client.doRequest(ctx, http.MethodGet, path, nil, &result); err != nil {
			return nil, err
		}

		allRecords = append(allRecords, result.Data.Items...)

		// 防死循环：无更多数据 / 服务端未返回分页 token / token 未变化
		if !result.Data.HasMore || result.Data.PageToken == "" || result.Data.PageToken == pageToken {
			break
		}
		pageToken = result.Data.PageToken
	}

	return allRecords, nil
}
