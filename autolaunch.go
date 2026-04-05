package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	launchAgentLabel = "com.cheivin.token-bar"
)

// launchAgentPlist 返回 LaunchAgent plist 文件路径
func launchAgentPlist() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

// isAutoLaunchEnabled 检查是否已启用开机自启
func isAutoLaunchEnabled() bool {
	_, err := os.Stat(launchAgentPlist())
	return err == nil
}

// setAutoLaunch 设置或取消开机自启
func setAutoLaunch(enabled bool) error {
	plstPath := launchAgentPlist()

	if !enabled {
		err := os.Remove(plstPath)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// 获取当前可执行文件路径，推导 .app bundle 路径
	appPath := appBundlePath()
	if appPath == "" {
		return fmt.Errorf("仅支持从 .app 包运行时启用开机自启")
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/open</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LaunchOnlyOnce</key>
	<true/>
</dict>
</plist>`, launchAgentLabel, appPath)

	// 确保 LaunchAgents 目录存在
	os.MkdirAll(filepath.Dir(plstPath), 0755)

	return os.WriteFile(plstPath, []byte(content), 0644)
}

// appBundlePath 从当前可执行文件路径推导 .app bundle 路径
// 如果不在 .app bundle 中运行，返回空字符串
func appBundlePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// .app bundle 内路径: /path/To/App.app/Contents/MacOS/token-bar
	idx := strings.Index(exe, ".app/Contents/MacOS/")
	if idx < 0 {
		return ""
	}
	return exe[:idx+4] // 包含 ".app"
}
