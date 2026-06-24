package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"token-bar/provider"

	"github.com/progrium/darwinkit/dispatch"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
)

// app 应用主结构体（基于 darwinkit / NSStatusItem 重新实现）
type app struct {
	app     appkit.Application
	config  *Config
	status  *appkit.StatusItem // 状态栏项（常驻，刷新时仅更新标题/菜单）
	menu    *appkit.Menu       // 状态栏菜单（常驻，刷新时 RemoveAllItems 后重建）
	button  appkit.StatusBarButton
	primary string // primary provider 的 tooltip

	providers   []providerInstance
	mu          sync.Mutex // 保护菜单重建
	cancelFuncs []context.CancelFunc

	settings settingsMenuItems // 当前设置菜单项引用
}

// providerInstance 单个 provider 运行时实例
type providerInstance struct {
	config   provider.ProviderConfig
	provider provider.Provider
	state    *providerMenuState
}

// providerMenuState provider 菜单构建状态
type providerMenuState struct {
	name       string
	lastResult *provider.ProviderResult
}

// settingsMenuItems 设置菜单项引用
type settingsMenuItems struct {
	refresh    appkit.MenuItem
	reload     appkit.MenuItem
	openConfig appkit.MenuItem
	autoLaunch appkit.MenuItem
	quit       appkit.MenuItem
}

// newApp 创建应用实例
func newApp(cfg *Config, application appkit.Application) *app {
	return &app{config: cfg, app: application}
}

// start 初始化状态栏并启动刷新
func (a *app) start(delegate *appkit.ApplicationDelegate) {
	a.createStatusItem()
	a.createProviders()

	// 首次菜单构建（无数据，显示加载中）
	a.rebuildAllMenus()

	// 应用退出前停止刷新 goroutine
	delegate.SetApplicationWillTerminate(func(foundation.Notification) {
		a.stopRefreshLoops()
	})

	a.startRefreshLoops()
}

// createStatusItem 创建状态栏项（整个生命周期常驻）
// darwinkit 创建的对象默认是 autorelease 的，会在 autorelease pool 结束时被释放，
// 必须用 objc.Retain 持有（同时设置 Go 侧 finalizer 防止对象被 GC 回收野指针）。
// Retain 要求传入独立堆对象，故用局部指针接收后再 Retain。
func (a *app) createStatusItem() {
	status := appkit.StatusBar_SystemStatusBar().StatusItemWithLength(appkit.VariableStatusItemLength)
	objc.Retain(&status)
	a.status = &status
	a.status.SetBehavior(appkit.StatusItemBehaviorRemovalAllowed)

	button := a.status.Button()
	objc.Retain(&button)
	a.button = button
	a.button.SetTitle("Token Bar")

	menu := appkit.NewMenuWithTitle("token-bar")
	objc.Retain(&menu)
	a.menu = &menu
	// 关闭"自动启用菜单项"，否则无 selector 的纯展示项会被禁用
	a.menu.SetAutoenablesItems(false)
	a.status.SetMenu(*a.menu)
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
			state:    &providerMenuState{name: cfg.Name},
		})
	}
}

// rebuildAllMenus 重建状态栏菜单。必须在主线程调用。
func (a *app) rebuildAllMenus() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 收集所有 provider 的最新结果
	results := make([]*provider.ProviderResult, len(a.providers))
	for i := range a.providers {
		results[i] = a.providers[i].state.lastResult
	}

	a.menu.RemoveAllItems()

	// primary provider：一级菜单展示详情
	for i := range a.providers {
		if a.providers[i].config.Primary {
			a.buildPrimaryMenu(i, results[i])
		}
	}
	// secondary provider：子菜单
	for i := range a.providers {
		if !a.providers[i].config.Primary {
			a.buildSecondaryMenu(i, results[i])
		}
	}

	// 更新状态栏标题与 tooltip（取第一个 primary）
	a.primary = ""
	for i := range a.providers {
		if a.providers[i].config.Primary && results[i] != nil {
			a.button.SetTitle(results[i].Title)
			if results[i].Subtitle != "" {
				a.primary = results[i].Subtitle
			}
			break
		}
	}
	a.button.SetToolTip(a.primary)

	a.settings = a.buildSettingsMenu()
}

// buildPrimaryMenu 为主 provider 构建详情菜单项
func (a *app) buildPrimaryMenu(idx int, result *provider.ProviderResult) {
	if result == nil {
		a.menu.AddItem(a.infoMenuItem(a.providers[idx].config.Name + " 加载中..."))
		a.menu.AddItem(appkit.MenuItem_SeparatorItem())
		return
	}

	a.addInfoItems(*a.menu, result.Items)
	a.menu.AddItem(a.infoMenuItem(fmt.Sprintf("  更新: %s", time.Now().Format("15:04:05"))))
	a.menu.AddItem(appkit.MenuItem_SeparatorItem())
}

// buildSecondaryMenu 为次 provider 构建子菜单
func (a *app) buildSecondaryMenu(idx int, result *provider.ProviderResult) {
	subMenu := appkit.NewMenuWithTitle(a.providers[idx].config.Name)
	subMenu.SetAutoenablesItems(false)

	if result == nil {
		subMenu.AddItem(a.infoMenuItem(a.providers[idx].config.Name + " 加载中..."))
	} else {
		a.addInfoItems(subMenu, result.Items)
		subMenu.AddItem(a.infoMenuItem(fmt.Sprintf("  更新: %s", time.Now().Format("15:04:05"))))
	}

	// 标题行（含 result.Title 或"加载中"）
	var headerTitle string
	if result != nil {
		headerTitle = fmt.Sprintf("%s  %s", a.providers[idx].config.Name, result.Title)
	} else {
		headerTitle = a.providers[idx].config.Name
	}
	parent := appkit.NewSubMenuItem(subMenu)
	parent.SetTitle(headerTitle)
	a.menu.AddItem(parent)
}

// infoMenuItem 创建纯展示菜单项：无 action（空 selector）。
// darwinkit 的 NewMenuItemWithAction 不接受 nil handler，故用 NewMenuItemWithSelector。
// 显式 SetEnabled(true)：菜单项只有 enabled 时才显示 attributedTitle 的颜色；
// 空 selector 不会响应点击，所以 enabled 也不会产生交互副作用。
func (a *app) infoMenuItem(title string) appkit.MenuItem {
	mi := appkit.NewMenuItemWithSelector(title, "", objc.Selector{})
	mi.SetEnabled(true)
	return mi
}

// addInfoItems 将 InfoItem 列表渲染进目标菜单，支持 Children 递归子菜单。
// - 有 Value：显示 "  Label: Value" 形式
// - 无 Value：仅显示 Label（常作为分组标题）
// - 有 Children：该项作为带子菜单的父项，Value/Label 作为标题，Children 作为子菜单内容
// 注意：不做项数截断。旧的 8 项上限是扁平结构时代的产物，会导致排在后面的带子菜单项被切掉。
func (a *app) addInfoItems(menu appkit.Menu, items []provider.InfoItem) {
	for _, item := range items {
		menu.AddItem(a.infoItemToMenuItem(item))
	}
}

// infoItemToMenuItem 将单个 InfoItem 转为 MenuItem。有 Children 时构建子菜单。
func (a *app) infoItemToMenuItem(item provider.InfoItem) appkit.MenuItem {
	// 有子项：作为带子菜单的父项
	if len(item.Children) > 0 {
		subMenu := appkit.NewMenuWithTitle(item.Label)
		subMenu.SetAutoenablesItems(false)
		a.addInfoItems(subMenu, item.Children)
		parent := appkit.NewSubMenuItem(subMenu)
		parent.SetTitle(item.Label)
		return parent
	}

	// 无子项：普通展示项。
	// 有 Highlight 时前缀水位状态点（emoji 是彩色字形，原生渲染，不依赖 attributedTitle）。
	// 注：darwinkit 这个版本的 attributedTitle 颜色属性无法生效（FFI 层丢失），
	// 故不用 SetAttributedTitle 上色，改用 emoji 圆点表示水位。
	var title string
	prefix := ""
	if item.Highlight > 0 {
		prefix = highlightDot(item.Highlight) + " "
	}
	if item.Value == "" {
		title = prefix + item.Label
	} else {
		title = fmt.Sprintf("%s  %s: %s", prefix, item.Label, item.Value)
	}
	return a.infoMenuItem(title)
}

// highlightDot 按使用率水位返回彩色 emoji 圆点：<70% 绿、70-90% 橙、>=90% 红。
func highlightDot(pct float64) string {
	switch {
	case pct >= 90:
		return "🔴"
	case pct >= 70:
		return "🟠"
	default:
		return "🟢"
	}
}

// buildSettingsMenu 构建设置和退出菜单（点击项绑定回调）
func (a *app) buildSettingsMenu() settingsMenuItems {
	a.menu.AddItem(appkit.MenuItem_SeparatorItem())

	settingsMenu := appkit.NewMenuWithTitle("设置")
	settingsMenu.SetAutoenablesItems(true)
	mRefresh := appkit.NewMenuItemWithAction("立即刷新", "", func(objc.Object) { a.refreshAll() })
	mReload := appkit.NewMenuItemWithAction("重新加载配置", "", func(objc.Object) { a.reloadConfig() })
	mOpenConfig := appkit.NewMenuItemWithAction("打开配置文件", "", func(objc.Object) { a.openConfigFile() })
	mAutoLaunch := appkit.NewMenuItemWithAction("开机自启", "", func(objc.Object) { a.toggleAutoLaunch() })
	settingsMenu.AddItem(mRefresh)
	settingsMenu.AddItem(mReload)
	settingsMenu.AddItem(mOpenConfig)
	settingsMenu.AddItem(mAutoLaunch)

	if isAutoLaunchEnabled() {
		mAutoLaunch.SetState(appkit.ControlStateValueOn)
	} else {
		mAutoLaunch.SetState(appkit.ControlStateValueOff)
	}

	settingsItem := appkit.NewSubMenuItem(settingsMenu)
	settingsItem.SetTitle("设置")
	a.menu.AddItem(settingsItem)

	mQuit := appkit.NewMenuItemWithAction("退出", "", func(objc.Object) { a.app.Terminate(nil) })
	a.menu.AddItem(mQuit)

	return settingsMenuItems{
		refresh:    mRefresh,
		reload:     mReload,
		openConfig: mOpenConfig,
		autoLaunch: mAutoLaunch,
		quit:       mQuit,
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

// refreshProvider 刷新单个 provider：后台拉取 → 主线程重建菜单
func (a *app) refreshProvider(idx int) {
	result, err := a.providers[idx].provider.Fetch()
	if err != nil {
		log.Printf("刷新 %s: %v", a.providers[idx].config.Name, err)
		result = &provider.ProviderResult{
			Title: fmt.Sprintf("错误: %s", truncateError(err.Error())),
		}
	}

	a.mu.Lock()
	if a.providers[idx].state == nil {
		a.providers[idx].state = &providerMenuState{}
	}
	a.providers[idx].state.lastResult = result
	a.mu.Unlock()

	// UI 操作必须在主线程
	dispatch.MainQueue().DispatchAsync(func() {
		a.rebuildAllMenus()
	})
}

// refreshAll 立即刷新所有 provider
func (a *app) refreshAll() {
	for idx := range a.providers {
		go a.refreshProvider(idx)
	}
}

// openConfigFile 用系统默认编辑器打开配置文件
func (a *app) openConfigFile() {
	go func() {
		_ = exec.Command("open", ConfigPath()).Start()
	}()
}

// toggleAutoLaunch 切换开机自启，并在主线程更新菜单勾选状态
func (a *app) toggleAutoLaunch() {
	enabled := !isAutoLaunchEnabled()
	if err := setAutoLaunch(enabled); err != nil {
		log.Printf("设置开机自启: %v", err)
		return
	}
	dispatch.MainQueue().DispatchAsync(func() {
		if enabled {
			a.settings.autoLaunch.SetState(appkit.ControlStateValueOn)
		} else {
			a.settings.autoLaunch.SetState(appkit.ControlStateValueOff)
		}
	})
}

// reloadConfig 原地重载配置：停止旧 goroutine → 重载 → 重建 providers → 重建菜单
func (a *app) reloadConfig() {
	a.stopRefreshLoops()

	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("重新加载配置: %v", err)
		dispatch.MainQueue().DispatchAsync(func() {
			a.startRefreshLoops()
		})
		return
	}
	a.config = cfg

	dispatch.MainQueue().DispatchAsync(func() {
		a.providers = nil
		a.createProviders()
		a.rebuildAllMenus()
		a.startRefreshLoops()
	})
}

// truncateError 截断错误信息
func truncateError(s string) string {
	if len(s) > 100 {
		return s[:100] + "..."
	}
	return s
}
