package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// newapiProvider NewAPI 平台数据提供者，查询配额余额、用量统计、RPM/TPM
type newapiProvider struct {
	baseURL string
	userID  string
	token   string
	client  *http.Client
}

// newapiStatusConfig /api/status 返回的配额单位换算参数
type newapiStatusConfig struct {
	QuotaDisplayType string  `json:"quota_display_type"` // 显示单位，如 "USD"、"CNY"
	QuotaPerUnit     float64 `json:"quota_per_unit"`     // 每单位配额数
	USDExchangeRate  float64 `json:"usd_exchange_rate"`  // 美元汇率
}

// newAPIProvider 从配置创建 NewAPI provider 实例
func newAPIProvider(cfg ProviderConfig) (Provider, error) {
	baseURL := GetStringParam(cfg.Params, "base_url")
	token := GetStringParam(cfg.Params, "token")
	userID := GetStringParam(cfg.Params, "user_id")

	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("newapi provider 缺少 base_url 或 token 参数")
	}

	parsed, err := ParseBaseDomain(baseURL)
	if err != nil {
		return nil, fmt.Errorf("解析 base_url: %w", err)
	}

	return &newapiProvider{
		baseURL: parsed,
		userID:  userID,
		token:   token,
		client:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Fetch 从 NewAPI 平台获取用量数据并转换为 ProviderResult
func (n *newapiProvider) Fetch() (*ProviderResult, error) {
	// 1. 获取配额单位换算参数
	statusCfg, err := n.fetchStatus()
	if err != nil {
		// 降级：无换算参数时使用原始数字
		statusCfg = nil
	}

	// 2. 获取个人资料（余额、已用量、请求次数）
	userData, err := n.fetchUserSelf()
	if err != nil {
		return nil, fmt.Errorf("查询个人资料: %w", err)
	}

	// 3. 获取当日日志统计（当日消耗、RPM、TPM）
	statData, err := n.fetchLogStat()
	if err != nil {
		return nil, fmt.Errorf("查询日志统计: %w", err)
	}

	// 构建结果
	balance := n.formatQuota(userData.Quota, statusCfg)
	todayQuota := n.formatQuota(statData.Quota, statusCfg)
	rpm := statData.RPM
	tpm := statData.TPM

	title := fmt.Sprintf("%s / %s", todayQuota, balance)
	subtitle := fmt.Sprintf("消耗(当日)/余额 %s/%s | RPM %d | TPM %d", todayQuota, balance, rpm, tpm)

	usedTotal := n.formatQuota(userData.UsedQuota, statusCfg)

	items := []InfoItem{
		{Label: "消耗(当日)", Value: todayQuota},
		{Label: "余额", Value: balance},
		{Label: "已用(总计)", Value: usedTotal},
		{Label: "RPM", Value: fmt.Sprintf("%d", rpm)},
		{Label: "TPM", Value: fmt.Sprintf("%d", tpm)},
		{Label: "请求次数", Value: FormatCount(userData.RequestCount)},
	}

	return &ProviderResult{
		Title:    title,
		Subtitle: subtitle,
		Items:    items,
	}, nil
}

// doGet 发送带认证的 GET 请求
func (n *newapiProvider) doGet(apiURL string, auth bool) ([]byte, error) {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+n.token)
		req.Header.Set("New-Api-User", n.userID)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("API Token 无效或已过期")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// fetchStatus 获取配额单位换算参数（无需认证）
func (n *newapiProvider) fetchStatus() (*newapiStatusConfig, error) {
	body, err := n.doGet(n.baseURL+"/api/status", false)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Success bool               `json:"success"`
		Data    newapiStatusConfig `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析响应: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("获取配置摘要失败")
	}
	return &resp.Data, nil
}

// newapiUserData /api/user/self 返回的个人资料
type newapiUserData struct {
	Quota        int64 `json:"quota"`         // 剩余配额
	UsedQuota    int64 `json:"used_quota"`    // 已使用配额
	RequestCount int64 `json:"request_count"` // 请求次数
}

// fetchUserSelf 获取个人资料（余额、已用量、请求次数）
func (n *newapiProvider) fetchUserSelf() (*newapiUserData, error) {
	body, err := n.doGet(n.baseURL+"/api/user/self", true)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Success bool           `json:"success"`
		Data    newapiUserData `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析响应: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("获取个人资料失败")
	}
	return &resp.Data, nil
}

// newapiStatData /api/log/self/stat 返回的日志统计数据
type newapiStatData struct {
	Quota int64 `json:"quota"` // 指定时间范围内的配额消耗
	RPM   int64 `json:"rpm"`   // 每分钟请求数
	TPM   int64 `json:"tpm"`   // 每分钟Token数
}

// fetchLogStat 获取当日日志统计（当日消耗、RPM、TPM）
func (n *newapiProvider) fetchLogStat() (*newapiStatData, error) {
	now := time.Now()
	// 今日0点的时间戳
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	apiURL := fmt.Sprintf(
		"%s/api/log/self/stat?type=2&start_timestamp=%d&end_timestamp=%d",
		n.baseURL,
		startOfDay.Unix(),
		now.Unix(),
	)

	body, err := n.doGet(apiURL, true)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Success bool           `json:"success"`
		Data    newapiStatData `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析响应: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("获取日志统计失败")
	}
	return &resp.Data, nil
}

// formatQuota 将原始配额转换为可读的显示格式
// 如果 statusCfg 为 nil（降级模式），显示原始数字
func (n *newapiProvider) formatQuota(rawQuota int64, cfg *newapiStatusConfig) string {
	if cfg == nil || cfg.QuotaPerUnit == 0 {
		return FormatToken(rawQuota)
	}
	value := float64(rawQuota) / cfg.QuotaPerUnit
	symbol := currencySymbol(cfg.QuotaDisplayType)
	return fmt.Sprintf("%s%.2f", symbol, value)
}

// currencySymbol 将货币代码转换为对应的符号
func currencySymbol(code string) string {
	switch code {
	case "USD":
		return "$"
	case "CNY", "RMB":
		return "¥"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "JPY":
		return "¥"
	default:
		return code
	}
}
