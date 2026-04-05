package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// execProvider 通用命令行 provider，执行外部命令并解析 JSON 输出
type execProvider struct {
	command   string
	timeout   time.Duration
	configDir string // 工作目录，用于解析相对路径
}

// newExecProvider 从配置创建 exec provider 实例
func newExecProvider(cfg ProviderConfig) (Provider, error) {
	command := GetStringParam(cfg.Params, "command")
	if command == "" {
		return nil, fmt.Errorf("exec provider 缺少 command 参数")
	}

	timeoutStr := GetStringParam(cfg.Params, "timeout")
	timeout := 10 * time.Second
	if timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = d
		}
	}

	return &execProvider{
		command:   command,
		timeout:   timeout,
		configDir: cfg.ConfigDir,
	}, nil
}

// Fetch 执行命令并解析 JSON 输出为 ProviderResult
func (e *execProvider) Fetch() (*ProviderResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", e.command)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// 工作目录设为配置目录，使相对路径脚本从该目录解析
	if e.configDir != "" {
		cmd.Dir = e.configDir
	}

	if err := cmd.Run(); err != nil {
		return &ProviderResult{
			Title: fmt.Sprintf("执行失败: %s", e.truncateError(err.Error())),
			Items: []InfoItem{
				{Label: "命令", Value: e.command},
			},
		}, nil // 返回结果而非 error，避免影响其他 provider
	}

	var result ProviderResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return &ProviderResult{
			Title: fmt.Sprintf("解析失败: %s", e.truncateError(err.Error())),
			Items: []InfoItem{
				{Label: "输出", Value: e.truncateString(stdout.String(), 200)},
			},
		}, nil
	}

	return &result, nil
}

// truncateError 截断错误信息
func (e *execProvider) truncateError(s string) string {
	if len(s) > 100 {
		return s[:100] + "..."
	}
	return s
}

// truncateString 截断字符串
func (e *execProvider) truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
