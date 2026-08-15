package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DailyCronExpr 生成「每天 HH:MM」对应的 cron 表达式（分 时 * * *）。
func DailyCronExpr(hour, minute int) (string, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return "", fmt.Errorf("时间无效: %02d:%02d", hour, minute)
	}
	return fmt.Sprintf("%d %d * * *", minute, hour), nil
}

// ParseDailyCron 解析每天一次的 cron；非该格式返回 ok=false。
func ParseDailyCron(expr string) (hour, minute int, ok bool) {
	parts := strings.Fields(strings.TrimSpace(expr))
	if len(parts) != 5 {
		return 0, 0, false
	}
	if parts[2] != "*" || parts[3] != "*" || parts[4] != "*" {
		return 0, 0, false
	}
	minute, err1 := strconv.Atoi(parts[0])
	hour, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// FormatDailyLabel 返回 UI 文案，如「每天 09:00」。
func FormatDailyLabel(hour, minute int) string {
	return fmt.Sprintf("每天 %02d:%02d", hour, minute)
}

// CronLabelFromExpr 从 cron 生成展示文案；无法识别则原样返回。
func CronLabelFromExpr(expr string) string {
	if h, m, ok := ParseDailyCron(expr); ok {
		return FormatDailyLabel(h, m)
	}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return FormatDailyLabel(9, 0)
	}
	return expr
}

// HHMMFromCron 返回 "HH:MM"；非每天格式则默认 09:00。
func HHMMFromCron(expr string) string {
	if h, m, ok := ParseDailyCron(expr); ok {
		return fmt.Sprintf("%02d:%02d", h, m)
	}
	return "09:00"
}

// ParseHHMM 解析 "HH:MM" 或 "H:MM"。
func ParseHHMM(s string) (hour, minute int, err error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("时间格式应为 HH:MM")
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("时间格式应为 HH:MM")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("时间无效: %s", s)
	}
	return hour, minute, nil
}

// SaveCronExpr 就地更新配置文件中的 cron_expr 行（尽量保留其它内容）。
func SaveCronExpr(path, expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("cron 表达式为空")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "cron_expr:") {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + `cron_expr: "` + expr + `"`
		found = true
		break
	}
	if !found {
		return fmt.Errorf("配置中未找到 cron_expr: %s", path)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
