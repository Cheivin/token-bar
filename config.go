package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"token-bar/provider"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	RefreshInterval string          `yaml:"refresh_interval" json:"refresh_interval"`
	Providers       []ProviderEntry `yaml:"providers" json:"providers"`
}

// ProviderEntry 单个 provider 配置
type ProviderEntry struct {
	Name            string                 `yaml:"name" json:"name"`
	Type            string                 `yaml:"type" json:"type"`
	Primary         bool                   `yaml:"primary" json:"primary"`
	RefreshInterval string                 `yaml:"refresh_interval" json:"refresh_interval"`
	Params          map[string]interface{} `yaml:"params" json:"params"`
}

// configDir 配置文件目录
func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(home, ".token-bar")
}

// configPaths 返回所有可能的配置文件路径（按优先级排列）
func configPaths() []string {
	dir := configDir()
	return []string{
		filepath.Join(dir, "config.yaml"),
		filepath.Join(dir, "config.yml"),
		filepath.Join(dir, "config.json"),
	}
}

// FindConfigFile 查找第一个存在的配置文件
func FindConfigFile() string {
	for _, p := range configPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 都不存在，返回默认路径（YAML）
	return filepath.Join(configDir(), "config.yaml")
}

// ConfigPath 返回当前使用的配置文件路径（导出）
func ConfigPath() string {
	return FindConfigFile()
}

// LoadConfig 加载配置文件，支持 YAML 和 JSON 双格式
func LoadConfig() (*Config, error) {
	path := FindConfigFile()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 配置文件不存在，创建默认模板
			cfg := defaultConfig()
			if saveErr := cfg.Save(); saveErr != nil {
				log.Printf("创建默认配置: %v", saveErr)
			}
			return cfg, nil
		}
		return &Config{}, err
	}

	// 根据扩展名选择解析器
	ext := filepath.Ext(path)
	switch ext {
	case ".yaml", ".yml":
		return parseYAML(data)
	case ".json":
		return parseJSON(data)
	default:
		return parseYAML(data)
	}
}

// parseYAML 解析 YAML 格式配置
func parseYAML(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// 检测旧格式兼容
	if len(cfg.Providers) == 0 {
		tryMigrateOldFormat(data, cfg)
	}
	return cfg, nil
}

// parseJSON 解析 JSON 格式配置
func parseJSON(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	// 检测旧格式兼容（顶层 api_key 字段）
	if len(cfg.Providers) == 0 {
		tryMigrateOldFormat(data, cfg)
	}
	return cfg, nil
}

// oldJSONConfig 旧版 JSON 配置结构
type oldJSONConfig struct {
	APIKey string `json:"api_key"`
}

// tryMigrateOldFormat 检测并迁移旧版 JSON 配置格式
func tryMigrateOldFormat(data []byte, cfg *Config) {
	var old oldJSONConfig
	if err := json.Unmarshal(data, &old); err != nil || old.APIKey == "" {
		return
	}
	cfg.RefreshInterval = "5m"
	cfg.Providers = []ProviderEntry{
		{
			Name:            "GLM用量",
			Type:            "glm",
			Primary:         true,
			RefreshInterval: "5m",
			Params:          map[string]interface{}{"api_key": old.APIKey},
		},
	}
}

// defaultConfig 返回默认配置
func defaultConfig() *Config {
	return &Config{
		RefreshInterval: "5m",
		Providers: []ProviderEntry{
			{
				Name:    "GLM用量",
				Type:    "glm",
				Primary: true,
				Params:  map[string]interface{}{},
			},
		},
	}
}

// Save 将配置保存为 YAML 格式
func (c *Config) Save() error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// ToProviderConfigs 将配置转换为 provider.ProviderConfig 列表
func (c *Config) ToProviderConfigs() []provider.ProviderConfig {
	defaultRefresh := parseRefreshInterval(c.RefreshInterval)
	cfgDir := configDir()

	var result []provider.ProviderConfig
	for _, entry := range c.Providers {
		refresh := defaultRefresh
		if entry.RefreshInterval != "" {
			if d, err := time.ParseDuration(entry.RefreshInterval); err == nil {
				refresh = d
			}
		}

		result = append(result, provider.ProviderConfig{
			Name:            entry.Name,
			Type:            entry.Type,
			Primary:         entry.Primary,
			RefreshInterval: refresh,
			ConfigDir:       cfgDir,
			Params:          entry.Params,
		})
	}
	return result
}

// parseRefreshInterval 解析刷新间隔字符串
func parseRefreshInterval(s string) time.Duration {
	if s == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}
