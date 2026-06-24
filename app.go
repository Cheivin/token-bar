package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"token-bar/provider"

	"fyne.io/systray"
)

// app 应用主结构体
type app struct {
	config        *Config
	providers     []providerInstance
	mu            sync.Mutex
	cancelFuncs   []context.CancelFunc
	pendingReload bool
	menuCtx       context.Context
	menuCancel    context.CancelFunc
	settings      settingsMenuItems // 当前设置菜单项引用
}

// providerInstance 单个 provider 运行时实例
type providerInstance struct {
	config   provider.ProviderConfig
	provider provider.Provider
	menu     providerMenuState
}

// providerMenuState provider 菜单状态
type providerMenuState struct {
	name       string
	titleItem  *systray.MenuItem
	items      []*systray.MenuItem
	lastResult *provider.ProviderResult
}

// settingsMenuItems 设置菜单项引用
type settingsMenuItems struct {
	refresh    *systray.MenuItem
	reload     *systray.MenuItem
	openConfig *systray.MenuItem
	autoLaunch *systray.MenuItem
	quit       *systray.MenuItem
}

// all 返回所有设置菜单项（用于 Remove 追踪）
func (s *settingsMenuItems) all() []*systray.MenuItem {
	return []*systray.MenuItem{s.refresh, s.reload, s.openConfig, s.autoLaunch, s.quit}
}

// newApp 创建应用实例
func newApp(cfg *Config) *app {
	return &app{config: cfg}
}

// onReady systray 就绪回调
func (a *app) onReady() {
	systray.SetTitle("Token Bar")

	a.createProviders()

	results := make([]*provider.ProviderResult, len(a.providers))
	a.rebuildAllMenus(results)

	a.startRefreshLoops()
}

// onExit systray 退出回调
func (a *app) onExit() {
	a.stopRefreshLoops()
	if a.menuCancel != nil {
		a.menuCancel()
	}
}

// createProviders 根据配置创建 provider 实例
func (a *app) createProviders() {
	if a.config == nil {
		return
	}
	configs := a.config.ToProviderConfigs()
	a.providers = make([]providerInstance, 0, len(configs))
	for _, cfg := range configs {
		p, err := provider.NewProvider(cfg)
		if err != nil {
			log.Printf("创建 provider %s 失败: %v", cfg.Name, err)
			continue
		}
		a.providers = append(a.providers, providerInstance{
			config:   cfg,
			provider: p,
		})
	}
}

// rebuildAllMenus 移除所有动态菜单项并重建
func (a *app) rebuildAllMenus(results []*provider.ProviderResult) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 取消旧的事件监听器
	if a.menuCancel != nil {
		a.menuCancel()
	}

	// 移除旧的 provider 菜单项
	for i := range a.providers {
		for _, item := range a.providers[i].menu.items {
			item.Remove()
		}
		a.providers[i].menu.items = nil
	}

	// 按 primary -> secondary 顺序重建
	for i, p := range a.providers {
		if p.config.Primary {
			a.buildPrimaryMenu(i, results[i])
		}
	}
	for i, p := range a.providers {
		if !p.config.Primary {
			a.buildSecondaryMenu(i, results[i])
		}
	}

	// 更新 primary provider 的 tooltip
	for i, p := range a.providers {
		if p.config.Primary && results[i] != nil && results[i].Subtitle != "" {
			systray.SetTooltip(results[i].Subtitle)
		}
	}

	// 构建设置菜单并启动事件监听
	settings := a.buildSettingsMenu()
	a.settings = settings
	a.menuCtx, a.menuCancel = context.WithCancel(context.Background())
	go a.listenSettingsEvents(a.menuCtx, settings)
}

// buildPrimaryMenu 为主 provider 构建详情菜单项
func (a *app) buildPrimaryMenu(idx int, result *provider.ProviderResult) {
	if result == nil {
		return
	}
	state := &a.providers[idx].menu
	if len(result.Items) > 8 {
		result.Items = result.Items[:8]
	}

	now := time.Now().Format("15:04:05")
	for _, item := range result.Items {
		var m *systray.MenuItem
		if item.Value == "" {
			m = systray.AddMenuItem(item.Label, "")
		} else {
			m = systray.AddMenuItem(fmt.Sprintf("  %s: %s", item.Label, item.Value), "")
		}
		state.items = append(state.items, m)
	}
	m := systray.AddMenuItem(fmt.Sprintf("  更新: %s", now), "")
	state.items = append(state.items, m)
	systray.AddSeparator()
}

// buildSecondaryMenu 为次 provider 构建子菜单
func (a *app) buildSecondaryMenu(idx int, result *provider.ProviderResult) {
	state := &a.providers[idx].menu

	if result == nil {
		titleItem := systray.AddMenuItem(a.providers[idx].config.Name+" 加载中...", a.providers[idx].config.Name)
		state.titleItem = titleItem
		state.items = append(state.items, titleItem)
		return
	}

	if len(result.Items) > 8 {
		result.Items = result.Items[:8]
	}

	titleItem := systray.AddMenuItem(a.providers[idx].config.Name+" "+result.Title, a.providers[idx].config.Name)
	state.titleItem = titleItem
	state.items = append(state.items, titleItem)

	now := time.Now().Format("15:04:05")
	for _, item := range result.Items {
		var m *systray.MenuItem
		if item.Value == "" {
			m = titleItem.AddSubMenuItem(item.Label, "")
		} else {
			m = titleItem.AddSubMenuItem(fmt.Sprintf("  %s: %s", item.Label, item.Value), "")
		}
		state.items = append(state.items, m)
	}
	m := titleItem.AddSubMenuItem(fmt.Sprintf("  更新: %s", now), "")
	state.items = append(state.items, m)
}

// buildSettingsMenu 构建设置和退出菜单
func (a *app) buildSettingsMenu() settingsMenuItems {
	systray.AddSeparator()

	settingsMenu := systray.AddMenuItem("设置", "设置")
	mRefresh := settingsMenu.AddSubMenuItem("立即刷新", "立即刷新所有数据")
	mReload := settingsMenu.AddSubMenuItem("重新加载配置", "重新加载配置文件")
	mOpenConfig := settingsMenu.AddSubMenuItem("打开配置文件", "用编辑器打开配置文件")
	mAutoLaunch := settingsMenu.AddSubMenuItem("开机自启", "开机自动启动")
	mQuit := systray.AddMenuItem("退出", "退出应用")

	if isAutoLaunchEnabled() {
		mAutoLaunch.Check()
	}

	return settingsMenuItems{
		refresh:    mRefresh,
		reload:     mReload,
		openConfig: mOpenConfig,
		autoLaunch: mAutoLaunch,
		quit:       mQuit,
	}
}

// listenSettingsEvents 监听设置菜单项事件（context 取消后退出）
func (a *app) listenSettingsEvents(ctx context.Context, s settingsMenuItems) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.refresh.ClickedCh:
			for idx := range a.providers {
				go a.refreshProvider(idx)
			}
		case <-s.reload.ClickedCh:
			a.reloadConfig()
		case <-s.openConfig.ClickedCh:
			a.openConfigFile()
		case <-s.autoLaunch.ClickedCh:
			a.toggleAutoLaunch()
		case <-s.quit.ClickedCh:
			a.pendingReload = false
			systray.Quit()
		}
	}
}

// startRefreshLoops 为每个 provider 启动独立 goroutine 定时刷新
func (a *app) startRefreshLoops() {
	a.cancelFuncs = make([]context.CancelFunc, 0, len(a.providers))
	for idx := range a.providers {
		ctx, cancel := context.WithCancel(context.Background())
		a.cancelFuncs = append(a.cancelFuncs, cancel)

		interval := a.providers[idx].config.RefreshInterval
		if interval <= 0 {
			interval = 5 * time.Minute
		}

		go func(ctx context.Context, idx int, interval time.Duration) {
			a.refreshProvider(idx)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					a.refreshProvider(idx)
				}
			}
		}(ctx, idx, interval)
	}
}

// stopRefreshLoops 停止所有刷新 goroutine
func (a *app) stopRefreshLoops() {
	for _, cancel := range a.cancelFuncs {
		cancel()
	}
	a.cancelFuncs = nil
}

// refreshProvider 刷新单个 provider 的数据并重建菜单
func (a *app) refreshProvider(idx int) {
	result, err := a.providers[idx].provider.Fetch()
	if err != nil {
		log.Printf("刷新 %s: %v", a.providers[idx].config.Name, err)
		result = &provider.ProviderResult{
			Title: fmt.Sprintf("错误: %s", truncateError(err.Error())),
		}
	}

	a.providers[idx].menu.lastResult = result

	// 收集所有 provider 的最新结果
	results := make([]*provider.ProviderResult, len(a.providers))
	for i := range a.providers {
		results[i] = a.providers[i].menu.lastResult
	}

	// 全量重建菜单
	a.rebuildAllMenus(results)
}

// openConfigFile 用系统默认编辑器打开配置文件
func (a *app) openConfigFile() {
	_ = exec.Command("open", ConfigPath()).Start()
}

// toggleAutoLaunch 切换开机自启
func (a *app) toggleAutoLaunch() {
	enabled := !isAutoLaunchEnabled()
	if err := setAutoLaunch(enabled); err != nil {
		log.Printf("设置开机自启: %v", err)
		return
	}
	if enabled {
		a.settings.autoLaunch.Check()
	} else {
		a.settings.autoLaunch.Uncheck()
	}
}

// reloadConfig 重载配置并重建菜单
func (a *app) reloadConfig() {
	a.stopRefreshLoops()
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("重新加载配置: %v", err)
		return
	}
	a.config = cfg
	a.pendingReload = true
	systray.Quit()
}

// truncateError 截断错误信息
func truncateError(s string) string {
	if len(s) > 100 {
		return s[:100] + "..."
	}
	return s
}
