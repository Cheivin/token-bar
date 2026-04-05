package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"

	"token-bar/provider"

	"github.com/getlantern/systray"
)

// app 应用主结构体
type app struct {
	config        *Config
	providers     []providerInstance
	menus         []providerMenuState
	cancelFuncs   []context.CancelFunc
	pendingReload bool
	settingsItems settingsMenuItems
}

// providerInstance 单个 provider 运行时实例
type providerInstance struct {
	config   provider.ProviderConfig
	provider provider.Provider
	menu     providerMenuState
}

// providerMenuState provider 菜单状态
type providerMenuState struct {
	name        string
	titleItem   *systray.MenuItem   // 主菜单项（仅次 provider 使用）
	detailItems []*systray.MenuItem // 详情子菜单项
}

// newApp 创建应用实例
func newApp(cfg *Config) *app {
	return &app{config: cfg}
}

// onReady systray 就绪回调
func (a *app) onReady() {

	systray.SetTitle("Token Bar")
	// 创建 provider 实例
	a.createProviders()

	// 构建菜单
	a.buildMenu()

	// 启动定时刷新
	a.startRefreshLoops()

	// 启动事件循环
	go a.eventLoop()
}

// onExit systray 退出回调
func (a *app) onExit() {
	a.stopRefreshLoops()
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

// buildMenu 构建完整菜单
func (a *app) buildMenu() {
	a.menus = make([]providerMenuState, 0, len(a.providers))

	// 先构建 primary provider，再构建 secondary，确保主信息在菜单顶部
	for i, p := range a.providers {
		if p.config.Primary {
			a.buildPrimaryMenu(i)
		}
	}
	for i, p := range a.providers {
		if !p.config.Primary {
			a.buildSecondaryMenu(i)
		}
	}

	// 分隔线
	systray.AddSeparator()

	// 设置子菜单
	settingsMenu := systray.AddMenuItem("设置", "设置")
	mRefresh := settingsMenu.AddSubMenuItem("立即刷新", "立即刷新所有数据")
	mReload := settingsMenu.AddSubMenuItem("重新加载配置", "重新加载配置文件")
	mOpenConfig := settingsMenu.AddSubMenuItem("打开配置文件", "用编辑器打开配置文件")
	mAutoLaunch := settingsMenu.AddSubMenuItem("开机自启", "开机自动启动")

	// 退出
	mQuit := systray.AddMenuItem("退出", "退出应用")

	// 初始化开机自启菜单状态
	if isAutoLaunchEnabled() {
		mAutoLaunch.Check()
	}

	// 保存设置菜单项引用
	a.settingsItems = settingsMenuItems{
		refresh:    mRefresh,
		reload:     mReload,
		openConfig: mOpenConfig,
		autoLaunch: mAutoLaunch,
		quit:       mQuit,
	}
}

// settingsMenuItems 设置菜单项引用
type settingsMenuItems struct {
	refresh    *systray.MenuItem
	reload     *systray.MenuItem
	openConfig *systray.MenuItem
	autoLaunch *systray.MenuItem
	quit       *systray.MenuItem
}

// buildPrimaryMenu 为主 provider 构建一级菜单（展开所有详情项）
func (a *app) buildPrimaryMenu(idx int) {
	p := a.providers[idx]
	state := providerMenuState{name: p.config.Name}
	state.detailItems = make([]*systray.MenuItem, 0)

	// primary 的主信息已在状态栏显示，菜单中不再重复创建标题项

	// 占位详情项（稍后刷新填充）
	for i := 0; i < 8; i++ {
		item := systray.AddMenuItem("  加载中...", "")
		state.detailItems = append(state.detailItems, item)
	}

	// 分隔线
	systray.AddSeparator()

	a.providers[idx].menu = state
	a.menus = append(a.menus, state)
}

// buildSecondaryMenu 为次 provider 构建子菜单
func (a *app) buildSecondaryMenu(idx int) {
	p := a.providers[idx]
	state := providerMenuState{name: p.config.Name}
	state.detailItems = make([]*systray.MenuItem, 0)

	// 一级菜单项（主信息）
	titleItem := systray.AddMenuItem(p.config.Name+" 加载中...", p.config.Name)
	state.titleItem = titleItem

	// 子菜单占位
	for i := 0; i < 8; i++ {
		sub := titleItem.AddSubMenuItem("  加载中...", "")
		state.detailItems = append(state.detailItems, sub)
	}

	a.providers[idx].menu = state
	a.menus = append(a.menus, state)
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
			// 首次立即刷新
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

// eventLoop 处理菜单事件
func (a *app) eventLoop() {
	for {
		select {
		case <-a.settingsItems.refresh.ClickedCh:
			// 立即刷新所有 provider
			for idx := range a.providers {
				go a.refreshProvider(idx)
			}
		case <-a.settingsItems.reload.ClickedCh:
			a.reloadConfig()
		case <-a.settingsItems.openConfig.ClickedCh:
			a.openConfigFile()
		case <-a.settingsItems.autoLaunch.ClickedCh:
			a.toggleAutoLaunch()
		case <-a.settingsItems.quit.ClickedCh:
			a.pendingReload = false
			systray.Quit()
		}
	}
}

// refreshProvider 刷新单个 provider 的数据并更新菜单
func (a *app) refreshProvider(idx int) {
	result, err := a.providers[idx].provider.Fetch()
	if err != nil {
		log.Printf("刷新 %s: %v", a.providers[idx].config.Name, err)
		result = &provider.ProviderResult{
			Title: fmt.Sprintf("错误: %s", truncateError(err.Error())),
		}
	}

	// 主 provider 同时更新状态栏标题（名字 + 主信息）
	if a.providers[idx].config.Primary {
		systray.SetTitle(a.providers[idx].config.Name + " " + result.Title)
		if result.Subtitle != "" {
			systray.SetTooltip(result.Subtitle)
		}
	}

	// 更新菜单项
	a.updateMenuForProvider(idx, result)
}

// updateMenuForProvider 更新 provider 的菜单项显示
func (a *app) updateMenuForProvider(idx int, result *provider.ProviderResult) {
	state := a.providers[idx].menu

	if state.titleItem != nil {
		// 次 provider：更新一级菜单标题
		state.titleItem.SetTitle(a.providers[idx].config.Name + " " + result.Title)
	}

	// 更新详情项
	now := time.Now().Format("15:04:05")
	allItems := result.Items
	if len(allItems) > len(state.detailItems) {
		allItems = allItems[:len(state.detailItems)]
	}

	for i, item := range allItems {
		if item.Value == "" {
			state.detailItems[i].SetTitle(item.Label)
		} else {
			state.detailItems[i].SetTitle(fmt.Sprintf("  %s: %s", item.Label, item.Value))
		}
		// 保持 enabled 状态以正常显示
	}

	// 最后一项显示更新时间
	lastIdx := len(allItems)
	if lastIdx < len(state.detailItems) {
		state.detailItems[lastIdx].SetTitle(fmt.Sprintf("  更新: %s", now))
		// 隐藏多余的占位项
		for j := lastIdx + 1; j < len(state.detailItems); j++ {
			state.detailItems[j].Hide()
		}
	}
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
		a.settingsItems.autoLaunch.Check()
	} else {
		a.settingsItems.autoLaunch.Uncheck()
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
