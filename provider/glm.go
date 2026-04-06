package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

// glmUsageData GLM API 内部数据聚合结构
type glmUsageData struct {
	// Token (5小时窗口)
	token5HourPct   float64 // 使用率百分比
	token5HourReset string  // 重置时间

	// Token (每周)
	tokenWeeklyPct   float64
	tokenWeeklyReset string

	// MCP (每月)
	mcpUsed  int64
	mcpTotal int64
	mcpPct   float64
	mcpReset string

	// 补充统计（来自 model-usage / tool-usage）
	weekReqs      int64
	weekTokens    int64
	monthReqs     int64
	monthTokens   int64
	toolWeekReqs  int64
	toolMonthReqs int64
}

// glmProvider GLM API 数据提供者（支持 ZHIPU / Z.AI 双平台）
type glmProvider struct {
	authToken  string
	baseDomain string
	client     *http.Client
}

// glmPlatformURLs 平台预设地址
var glmPlatformURLs = map[string]string{
	"zhipu": "https://open.bigmodel.cn",
	"z.ai":  "https://api.z.ai",
}

// newGLMProvider 从配置创建 GLM provider 实例
// 支持两种配置方式：
//   - platform: "zhipu" 或 "z.ai"（预设域名）
//   - base_url: 自定义域名（优先级高于 platform）
func newGLMProvider(cfg ProviderConfig) (Provider, error) {
	apiKey := GetStringParam(cfg.Params, "api_key")
	if apiKey == "" {
		return &mockProvider{}, nil
	}

	// 确定 baseDomain：base_url > platform > 默认 zhipu
	baseDomain := GetStringParam(cfg.Params, "base_url")
	if baseDomain == "" {
		platform := GetStringParam(cfg.Params, "platform")
		if platform == "" {
			platform = "zhipu"
		}
		var ok bool
		baseDomain, ok = glmPlatformURLs[platform]
		if !ok {
			return nil, fmt.Errorf("未知 platform: %s，可选: zhipu, z.ai", platform)
		}
	}

	// 确保 baseDomain 是 scheme+host 格式
	parsed, err := ParseBaseDomain(baseDomain)
	if err != nil {
		return nil, fmt.Errorf("解析 base_url: %w", err)
	}

	return &glmProvider{
		authToken:  apiKey,
		baseDomain: parsed,
		client:     &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Fetch 从 GLM API 获取用量数据并转换为 ProviderResult
func (g *glmProvider) Fetch() (*ProviderResult, error) {
	var data glmUsageData

	// 1. 查询 quota/limit 获取配额信息
	if err := g.fetchQuotaLimit(&data); err != nil {
		return nil, fmt.Errorf("查询配额限制: %w", err)
	}

	// 2. 查询 7 天和 30 天模型用量
	g.fetchModelUsage(7, &data.weekReqs, &data.weekTokens)
	g.fetchModelUsage(30, &data.monthReqs, &data.monthTokens)

	// 3. 查询 7 天和 30 天工具用量
	g.fetchToolUsage(7, &data.toolWeekReqs)
	g.fetchToolUsage(30, &data.toolMonthReqs)

	// 组装 ProviderResult
	result := &ProviderResult{
		Title: fmt.Sprintf("5H:%.0f%% | W:%.0f%%", data.token5HourPct, data.tokenWeeklyPct),
		Items: []InfoItem{},
	}

	// 5小时窗口
	result.Items = append(result.Items,
		InfoItem{Label: "5小时", Value: ""},
		InfoItem{Label: "  使用率", Value: fmt.Sprintf("%.2f%%", data.token5HourPct)},
	)
	if data.token5HourReset != "" {
		result.Items = append(result.Items, InfoItem{Label: "  重置", Value: data.token5HourReset})
		result.Subtitle = fmt.Sprintf("5小时窗口 | 重置 %s", data.token5HourReset)
	} else {
		result.Subtitle = "5小时窗口"
	}

	// 每周窗口
	result.Items = append(result.Items,
		InfoItem{Label: "每周", Value: ""},
		InfoItem{Label: "  使用率", Value: fmt.Sprintf("%.2f%%", data.tokenWeeklyPct)},
	)
	if data.tokenWeeklyReset != "" {
		result.Items = append(result.Items, InfoItem{Label: "  重置", Value: data.tokenWeeklyReset})
	}

	// MCP 每月用量（如果有数据）
	if data.mcpTotal > 0 || data.mcpUsed > 0 {
		result.Items = append(result.Items,
			InfoItem{Label: "MCP (每月)", Value: ""},
			InfoItem{Label: "  用量", Value: fmt.Sprintf("%s / %s", FormatCount(data.mcpUsed), FormatCount(data.mcpTotal))},
			InfoItem{Label: "  使用率", Value: fmt.Sprintf("%.2f%%", data.mcpPct)},
		)
		if data.mcpReset != "" {
			result.Items = append(result.Items, InfoItem{Label: "  重置", Value: data.mcpReset})
		}
	}

	// 补充统计
	result.Items = append(result.Items,
		InfoItem{Label: "7天统计", Value: ""},
		InfoItem{Label: "  请求", Value: FormatCount(data.weekReqs)},
		InfoItem{Label: "  Token", Value: FormatToken(data.weekTokens)},
		InfoItem{Label: "30天统计", Value: ""},
		InfoItem{Label: "  请求", Value: FormatCount(data.monthReqs)},
		InfoItem{Label: "  Token", Value: FormatToken(data.monthTokens)},
	)

	// MCP 工具用量（如果有数据）
	if data.toolWeekReqs > 0 || data.toolMonthReqs > 0 {
		result.Items = append(result.Items,
			InfoItem{Label: "MCP 工具用量", Value: ""},
			InfoItem{Label: "  7天调用", Value: FormatCount(data.toolWeekReqs)},
			InfoItem{Label: "  30天调用", Value: FormatCount(data.toolMonthReqs)},
		)
	}

	return result, nil
}

// doGet 发送 GET 请求并返回响应体
func (g *glmProvider) doGet(apiURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	// 认证方式与官方脚本一致：直接使用 token，不加 Bearer 前缀
	req.Header.Set("Authorization", g.authToken)
	req.Header.Set("Accept-Language", "en-US,en")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("API Key 无效或已过期")
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// fetchQuotaLimit 查询配额限制 API，获取各窗口用量
func (g *glmProvider) fetchQuotaLimit(data *glmUsageData) error {
	body, err := g.doGet(g.baseDomain + "/api/monitor/usage/quota/limit")
	if err != nil {
		return err
	}

	// 响应结构，字段与 Swift 参考代码一致
	var resp struct {
		Data struct {
			Limits []struct {
				Type          string  `json:"type"`
				Unit          int     `json:"unit"`
				Number        int     `json:"number"`
				Usage         int64   `json:"usage"`
				CurrentValue  int64   `json:"currentValue"`
				Remaining     int64   `json:"remaining"`
				Percentage    float64 `json:"percentage"`
				NextResetTime int64   `json:"nextResetTime"` // 毫秒时间戳
			} `json:"limits"`
			Level string `json:"level"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("解析响应: %w", err)
	}

	for _, limit := range resp.Data.Limits {
		resetTime := parseResetTime(limit.NextResetTime)

		switch limit.Type {
		case "TIME_LIMIT":
			// TIME_LIMIT: MCP 调用 (每月)
			data.mcpUsed = limit.CurrentValue
			data.mcpTotal = limit.Usage
			data.mcpPct = limit.Percentage
			data.mcpReset = resetTime

		case "TOKENS_LIMIT":
			// 根据 unit+number 区分窗口类型
			// unit=3, number=5 → 5小时窗口
			// unit=6, number=1 → 每周
			if limit.Unit == 3 && limit.Number == 5 {
				data.token5HourPct = limit.Percentage
				data.token5HourReset = resetTime
			} else if limit.Unit == 6 && limit.Number == 1 {
				data.tokenWeeklyPct = limit.Percentage
				data.tokenWeeklyReset = resetTime
			}
		}
	}
	return nil
}

// parseResetTime 将毫秒时间戳转为易读的时间字符串
func parseResetTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	t := time.Unix(ms/1000, 0)
	return t.Format("01/02 15:04")
}

// fetchModelUsage 查询指定天数的模型用量
func (g *glmProvider) fetchModelUsage(days int, outReqs, outTokens *int64) {
	now := time.Now()
	start := now.AddDate(0, 0, -days)

	apiURL := fmt.Sprintf(
		"%s/api/monitor/usage/model-usage?startTime=%s&endTime=%s",
		g.baseDomain,
		url.QueryEscape(FormatDateTime(start)),
		url.QueryEscape(FormatDateTime(now)),
	)

	body, err := g.doGet(apiURL)
	if err != nil {
		log.Printf("查询 %d 天模型用量: %v", days, err)
		return
	}

	var resp struct {
		Data struct {
			TotalUsage struct {
				TotalModelCallCount int64 `json:"totalModelCallCount"`
				TotalTokensUsage    int64 `json:"totalTokensUsage"`
			} `json:"totalUsage"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("解析 %d 天模型用量: %v", days, err)
		return
	}

	*outReqs = resp.Data.TotalUsage.TotalModelCallCount
	*outTokens = resp.Data.TotalUsage.TotalTokensUsage
}

// fetchToolUsage 查询指定天数的工具用量
func (g *glmProvider) fetchToolUsage(days int, outReqs *int64) {
	now := time.Now()
	start := now.AddDate(0, 0, -days)

	apiURL := fmt.Sprintf(
		"%s/api/monitor/usage/tool-usage?startTime=%s&endTime=%s",
		g.baseDomain,
		url.QueryEscape(FormatDateTime(start)),
		url.QueryEscape(FormatDateTime(now)),
	)

	body, err := g.doGet(apiURL)
	if err != nil {
		log.Printf("查询 %d 天工具用量: %v", days, err)
		return
	}

	var resp struct {
		Data struct {
			TotalUsage struct {
				TotalToolCallCount int64 `json:"totalToolCallCount"`
			} `json:"totalUsage"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		log.Printf("解析 %d 天工具用量: %v", days, err)
		return
	}

	*outReqs = resp.Data.TotalUsage.TotalToolCallCount
}
