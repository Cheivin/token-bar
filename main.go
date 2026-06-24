package main

import (
	"log"

	"github.com/progrium/darwinkit/macos"
	"github.com/progrium/darwinkit/macos/appkit"
)

func main() {
	// darwinkit 的 RunApp 内部会 LockOSThread 并启动 macOS 事件循环（阻塞）。
	// 与之前 systray 的"退出再重启"热重载不同，配置重载改为在事件循环内原地重建菜单。
	macos.RunApp(func(app appkit.Application, delegate *appkit.ApplicationDelegate) {
		cfg, err := LoadConfig()
		if err != nil {
			log.Printf("加载配置: %v", err)
			cfg = nil
		}

		// 应用不显示 Dock 图标，仅驻留状态栏（配合 Info.plist 的 LSUIElement=true）
		app.SetActivationPolicy(appkit.ApplicationActivationPolicyAccessory)

		a := newApp(cfg, app)
		a.start(delegate)
	})
}
