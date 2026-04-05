package main

import (
	_ "embed"
	"log"

	"github.com/getlantern/systray"
)

func main() {
	for {
		cfg, err := LoadConfig()
		if err != nil {
			log.Printf("加载配置: %v", err)
			cfg = nil
		}

		app := newApp(cfg)

		systray.Run(app.onReady, app.onExit)

		// 如果需要热重载，循环重新启动
		if !app.pendingReload {
			break
		}
	}
}
