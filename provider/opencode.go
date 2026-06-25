package provider

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// opencodeProvider OpenCode Go 套餐用量 provider，复用浏览器已登录的 auth cookie
// 请求用量页面并解析 SolidJS SSR 注入的 hydration 数据
type opencodeProvider struct {
	workspaceID string
	authCookie  string // hapi Iron 加密的 auth 值，会过期，过期时只改这一项
	client      *http.Client
}

// openCodeWindow 单个用量窗口（滚动/每周/每月）
type openCodeWindow struct {
	status       string // "ok" / "rate-limited" 等
	resetInSec   int    // 剩余重置秒数
	usagePercent int    // 使用率百分比
}

// openCodeUsage 三窗口用量聚合
type openCodeUsage struct {
	rolling openCodeWindow // 滚动用量
	weekly  openCodeWindow // 每周用量
	monthly openCodeWindow // 每月用量
}

// newOpenCodeProvider 从配置创建 opencode provider 实例
// params: workspace_id（wrk_xxx）、auth_cookie（Iron 加密的 auth 值）
func newOpenCodeProvider(cfg ProviderConfig) (Provider, error) {
	workspaceID := GetStringParam(cfg.Params, "workspace_id")
	authCookie := GetStringParam(cfg.Params, "auth_cookie")
	if workspaceID == "" || authCookie == "" {
		return nil, fmt.Errorf("opencode provider 缺少 workspace_id 或 auth_cookie 参数")
	}

	return &opencodeProvider{
		workspaceID: workspaceID,
		authCookie:  authCookie,
		client:      &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Fetch 请求 OpenCode Go 用量页面并解析返回 ProviderResult
func (o *opencodeProvider) Fetch() (*ProviderResult, error) {
	body, err := o.doGet()
	if err != nil {
		return nil, err
	}

	data, err := parseOpenCodeUsage(body)
	if err != nil {
		return nil, err
	}

	return buildOpenCodeResult(data), nil
}

// doGet 请求用量页面，仅带最小必需请求头（auth cookie + user-agent）
func (o *opencodeProvider) doGet() ([]byte, error) {
	url := fmt.Sprintf("https://opencode.ai/workspace/%s/go", o.workspaceID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", fmt.Sprintf("oc_locale=zh; auth=%s", o.authCookie))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, fmt.Errorf("auth cookie 无效或已过期")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// usageObjRe 匹配单个用量对象块，容忍 SolidJS 的 $R[xx]= 引用占位
// 形如 rollingUsage:$R[30]={status:"ok",resetInSec:18000,usagePercent:0}
var usageObjRe = regexp.MustCompile(`(rollingUsage|weeklyUsage|monthlyUsage):[^\{]*\{([^}]*)\}`)

// 字段子正则：字段顺序无关，逐个提取
var statusRe = regexp.MustCompile(`status:"([^"]*)"`)
var resetInSecRe = regexp.MustCompile(`resetInSec:(\d+)`)
var usagePercentRe = regexp.MustCompile(`usagePercent:(\d+)`)

// parseOpenCodeUsage 从 HTML 内嵌 hydration 数据提取三窗口用量
// hydration 是 JSON-ish 但 key 无引号，不能用 json.Unmarshal，故走正则
func parseOpenCodeUsage(html []byte) (*openCodeUsage, error) {
	matches := usageObjRe.FindAllSubmatch(html, -1)
	if len(matches) < 3 {
		return nil, fmt.Errorf("解析用量数据失败：未找到 hydration 数据")
	}

	data := &openCodeUsage{}
	// 三对象可能乱序出现，按 key 名分发到对应窗口
	// 真实 HTML 里 billing 对象也含 monthlyUsage:null 字段，正则会误匹配到其后首个 {
	// （如 lite:$R[36]={useBalance:!0}），这类块无 status 字段，据此跳过，避免覆盖真正的用量对象
	for _, m := range matches {
		key := string(m[1])
		w := parseWindowBlock(string(m[2]))
		if w.status == "" {
			continue
		}
		switch key {
		case "rollingUsage":
			data.rolling = w
		case "weeklyUsage":
			data.weekly = w
		case "monthlyUsage":
			data.monthly = w
		}
	}

	if data.rolling.status == "" && data.weekly.status == "" && data.monthly.status == "" {
		return nil, fmt.Errorf("解析用量数据失败：未找到 hydration 数据")
	}
	return data, nil
}

// parseWindowBlock 解析单个对象块内的 status/resetInSec/usagePercent
func parseWindowBlock(block string) openCodeWindow {
	w := openCodeWindow{}
	if sm := statusRe.FindStringSubmatch(block); len(sm) > 1 {
		w.status = sm[1]
	}
	if rm := resetInSecRe.FindStringSubmatch(block); len(rm) > 1 {
		w.resetInSec, _ = strconv.Atoi(rm[1])
	}
	if pm := usagePercentRe.FindStringSubmatch(block); len(pm) > 1 {
		w.usagePercent, _ = strconv.Atoi(pm[1])
	}
	return w
}

// buildOpenCodeResult 将三窗口用量组装为 ProviderResult
func buildOpenCodeResult(data *openCodeUsage) *ProviderResult {
	result := &ProviderResult{
		Title:    fmt.Sprintf("R:%d%% | W:%d%% | M:%d%%", data.rolling.usagePercent, data.weekly.usagePercent, data.monthly.usagePercent),
		Subtitle: fmt.Sprintf("重置 %s | %s | %s", formatRemainingSubtitle(data.rolling.resetInSec), formatRemainingSubtitle(data.weekly.resetInSec), formatRemainingSubtitle(data.monthly.resetInSec)),
		Items:    []InfoItem{},
	}

	result.Items = append(result.Items, buildWindowItems("滚动用量", data.rolling)...)
	result.Items = append(result.Items, buildWindowItems("每周用量", data.weekly)...)
	result.Items = append(result.Items, buildWindowItems("每月用量", data.monthly)...)

	return result
}

// buildWindowItems 构建单个窗口的菜单项：标题行（按 status 决定高亮）+ 使用率/状态/重置
func buildWindowItems(label string, w openCodeWindow) []InfoItem {
	// status 非 ok 时强制 Highlight=100（红色高亮）；ok 时按实际百分比走水位阈值
	highlight := float64(w.usagePercent)
	if w.status != "ok" && w.status != "" {
		highlight = 100
	}

	items := []InfoItem{
		{Label: label, Value: "", Highlight: highlight},
		{Label: "    使用率", Value: fmt.Sprintf("%d%%", w.usagePercent)},
		{Label: "    状态", Value: w.status},
		{Label: "    重置", Value: formatRemaining(w.resetInSec)},
	}
	return items
}

// formatRemaining 将剩余秒数格式化为 opencode 网页一致的相对时长
// 天>0 → "X天Y小时"；否则 → "X小时Y分钟"；否则 → "X分钟"
func formatRemaining(sec int) string {
	if sec <= 0 {
		return "0分钟"
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%d天%d小时", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%d小时%d分钟", h, m)
	}
	return fmt.Sprintf("%d分钟", m)
}

// formatRemainingSubtitle 状态栏 Subtitle 用的紧凑相对时长（d/h/m 单字母）
func formatRemainingSubtitle(sec int) string {
	if sec <= 0 {
		return "0m"
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd%dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
