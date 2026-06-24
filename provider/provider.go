package provider

import (
	"fmt"
	"net/url"
	"time"
)

// ProviderResult provider 统一返回结构
type ProviderResult struct {
	Title    string     // 主信息，如 "GLM 37.00%"
	Subtitle string     // tooltip 补充说明
	Items    []InfoItem // 附加信息列表
}

// InfoItem 附加信息项
type InfoItem struct {
	Label     string     // 如 "用量"、"使用率"
	Value     string     // 如 "296.00M / 800.00M"
	Children  []InfoItem // 子菜单项（可选）
	Highlight float64    // 高亮数值（0-100，如使用率）。>0 时按水位阈值给该行着色，0 表示不着色。
}

// Provider 数据提供者接口
type Provider interface {
	Fetch() (*ProviderResult, error)
}

// ProviderConfig provider 配置参数
type ProviderConfig struct {
	Name            string
	Type            string
	Primary         bool
	RefreshInterval time.Duration
	ConfigDir       string // 配置文件目录，exec provider 用于解析相对路径
	Params          map[string]interface{}
}

// NewProvider 根据 ProviderConfig 创建对应的 Provider 宱例
func NewProvider(cfg ProviderConfig) (Provider, error) {
	switch cfg.Type {
	case "glm":
		return newGLMProvider(cfg)
	case "exec":
		return newExecProvider(cfg)
	case "newapi":
		return newAPIProvider(cfg)
	default:
		return nil, fmt.Errorf("未知 provider 类型: %s", cfg.Type)
	}
}

// ---------- 工具函数 ----------

// FormatToken 将 token 数量格式化为易读的字符串（K/M/B）
func FormatToken(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.2fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// FormatCount 将请求数量格式化为易读的字符串
func FormatCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.2fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// FormatDateTime 格式化为 API 所需的时间格式
func FormatDateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// CalcWindowReset 计算5小时窗口的重置时间
func CalcWindowReset() string {
	now := time.Now()
	h := now.Hour()
	// 5小时窗口: 0-5, 5-10, 10-15, 15-20, 20-24
	var resetHour int
	switch {
	case h < 5:
		resetHour = 5
	case h < 10:
		resetHour = 10
	case h < 15:
		resetHour = 15
	case h < 20:
		resetHour = 20
	default:
		tomorrow := now.AddDate(0, 0, 1)
		return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 0, 0, 0, 0, now.Location()).Format("15:04")
	}
	return time.Date(now.Year(), now.Month(), now.Day(), resetHour, 0, 0, 0, now.Location()).Format("15:04")
}

// ParseBaseDomain 从完整 URL 中提取 scheme + host（如 https://open.bigmodel.cn）
func ParseBaseDomain(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

// GetStringParam 从 params map 中安全获取字符串值
func GetStringParam(params map[string]interface{}, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// mockProvider 模拟数据提供者，用于开发和测试
type mockProvider struct{}

// Fetch 返回模拟的 GLM 用量数据
func (m *mockProvider) Fetch() (*ProviderResult, error) {
	return &ProviderResult{
		Title:    "GLM 37.00%",
		Subtitle: "Mock 数据 | 5小时窗口 | 重置 " + CalcWindowReset(),
		Items: []InfoItem{
			{Label: "5小时窗口 ●", Value: ""},
			{Label: "  用量", Value: "296.00M / 800.00M"},
			{Label: "  使用率", Value: "37.00%"},
			{Label: "  重置", Value: CalcWindowReset()},
			{Label: "7天统计", Value: ""},
			{Label: "  请求", Value: "6.49K"},
			{Label: "  Token", Value: "359.71M"},
			{Label: "30天统计", Value: ""},
			{Label: "  请求", Value: "16.41K"},
			{Label: "  Token", Value: "1.02B"},
		},
	}, nil
}
