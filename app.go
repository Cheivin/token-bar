package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"token-bar/claude"
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

	// Claude Code 会话监控（独立于 provider 体系，事件驱动）
	claudeMonitor  *claude.Monitor
	claudeNotifier *claude.Notifier
	claudeCancel   context.CancelFunc
	claudeSessions map[int]*claude.Session // 最近一次会话快照（受 mu 保护）
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
		a.stopClaudeMonitor()
	})

	a.startRefreshLoops()
	a.startClaudeMonitor()
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

	// Claude Code 会话监控优先展示（独立一级公民）
	a.buildClaudeMenu()

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

	// 更新状态栏标题与 tooltip：
	// 格式 [会话圆点] | <primary provider 标题>，圆点最多展示 3 个。
	a.primary = ""
	var providerTitle string
	for i := range a.providers {
		if a.providers[i].config.Primary && results[i] != nil {
			providerTitle = results[i].Title
			if results[i].Subtitle != "" {
				a.primary = results[i].Subtitle
			}
			break
		}
	}
	a.button.SetTitle(a.statusBarTitle(providerTitle))
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

// startClaudeMonitor 启动 Claude Code 会话监控（事件驱动，独立于 provider 轮询）。
// onChange 在后台 fsnotify goroutine 触发：先存快照，再派发主线程重建菜单 + 通知检查。
// 通知的宿主前台检测需主线程，故 CheckAndNotify 在主线程闭包内执行。
func (a *app) startClaudeMonitor() {
	_, cancel := context.WithCancel(context.Background())
	a.claudeCancel = cancel

	// 宿主检测注入：DetectHostApp 调用 appkit，必须在主线程执行
	// 通知发送注入：用 darwinkit 原生 NSUserNotificationCenter，通知归属本应用
	// （com.cheivin.token-bar），图标与标题自然正确，避免 beeep/osascript 显示"脚本编辑器"。
	a.claudeNotifier = claude.NewNotifier(func(s *claude.Session) *claude.HostApp {
		return claude.DetectHostApp(s.PID)
	}, notifyUser)

	m, err := claude.NewMonitor(func(sessions map[int]*claude.Session) {
		// 快照深拷贝，避免后台 goroutine 与菜单重建竞争
		snap := make(map[int]*claude.Session, len(sessions))
		for k, v := range sessions {
			cp := *v
			snap[k] = &cp
		}

		a.mu.Lock()
		a.claudeSessions = snap
		a.mu.Unlock()

		// UI 与通知检查均需主线程
		dispatch.MainQueue().DispatchAsync(func() {
			a.rebuildAllMenus()
			a.claudeNotifier.CheckAndNotify(snap)
		})
	})
	if err != nil {
		log.Printf("启动 Claude 监控失败: %v", err)
		return
	}
	a.claudeMonitor = m
}

// stopClaudeMonitor 停止 Claude Code 会话监控。
func (a *app) stopClaudeMonitor() {
	if a.claudeCancel != nil {
		a.claudeCancel()
		a.claudeCancel = nil
	}
	if a.claudeMonitor != nil {
		a.claudeMonitor.Close()
		a.claudeMonitor = nil
	}
}

// buildClaudeMenu 构建 Claude Code 会话监控菜单。
// 无活跃会话时不显示任何项；有会话时展示一个子菜单汇总。
// 必须在主线程调用（rebuildAllMenus 内部，已持有 mu）。
func (a *app) buildClaudeMenu() {
	snap := a.claudeSessions
	if len(snap) == 0 {
		return
	}

	// 按项目名排序，保证菜单稳定
	pids := make([]int, 0, len(snap))
	for pid := range snap {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool {
		return snap[pids[i]].ProjectName() < snap[pids[j]].ProjectName()
	})

	var items []provider.InfoItem
	blocked, working, finished := 0, 0, 0
	for _, pid := range pids {
		s := snap[pid]
		st := s.EffectiveState()
		dot := stateDot(st)
		switch st {
		case claude.StateBlocked:
			blocked++
		case claude.StateWorking:
			working++
		case claude.StateDone, claude.StateIdle, claude.StateFailed, claude.StateStopped:
			finished++
		}

		label := fmt.Sprintf("%s %s", dot, s.ProjectName())
		var value string
		if st == claude.StateBlocked && s.WaitingFor != "" {
			value = s.WaitingFor
		} else {
			value = stateLabel(st)
		}
		items = append(items, provider.InfoItem{Label: label, Value: value})
	}

	// 汇总标题：显示最差状态
	headerDot := "⚪"
	switch {
	case blocked > 0:
		headerDot = "🟠"
	case working > 0:
		headerDot = "🟢"
	}
	header := fmt.Sprintf("%s Claude Code（%d 会话）", headerDot, len(snap))

	// 用现有 InfoItem 渲染：header 作为带子菜单的父项
	a.addInfoItems(*a.menu, []provider.InfoItem{
		{Label: header, Children: items},
	})
}

// stateLabel 将规范状态转为中文展示标签。
func stateLabel(st string) string {
	switch st {
	case claude.StateWorking:
		return "执行中"
	case claude.StateBlocked:
		return "等待输入"
	case claude.StateDone, claude.StateIdle:
		return "已完成"
	case claude.StateFailed:
		return "失败"
	case claude.StateStopped:
		return "已停止"
	}
	return st
}

// stateDot 将规范状态映射为状态栏/菜单用的圆点 emoji。
func stateDot(st string) string {
	switch st {
	case claude.StateWorking:
		return "🟢"
	case claude.StateBlocked:
		return "🟠"
	case claude.StateDone, claude.StateIdle:
		return "🔵"
	case claude.StateFailed:
		return "🔴"
	case claude.StateStopped:
		return "⚫"
	}
	return "⚪"
}

// statusBarTitle 组装状态栏按钮标题：[会话圆点] | <provider 标题>。
// 会话圆点最多展示 3 个，超出用 +N 提示；无活跃会话时不加前缀。
//
// 状态栏只展示"活跃"会话（working/blocked/failed/stopped）；
// 已完成（done/idle）仅在菜单内展示，不占状态栏位置，避免标题被历史会话刷屏。
func (a *app) statusBarTitle(providerTitle string) string {
	snap := a.claudeSessions

	// 过滤出活跃会话（done/idle 不上状态栏）
	active := make([]*claude.Session, 0, len(snap))
	for _, s := range snap {
		if isStatusBarActive(s.EffectiveState()) {
			active = append(active, s)
		}
	}
	if len(active) == 0 {
		return providerTitle
	}

	// 按 startedAt 倒序，最近活跃的会话优先展示
	sort.Slice(active, func(i, j int) bool {
		if active[i].StartedAt != active[j].StartedAt {
			return active[i].StartedAt > active[j].StartedAt
		}
		return active[i].ProjectName() < active[j].ProjectName()
	})

	const maxDots = 3
	var dots strings.Builder
	shown := 0
	for _, s := range active {
		if shown >= maxDots {
			break
		}
		dots.WriteString(stateDot(s.EffectiveState()))
		shown++
	}

	prefix := dots.String()
	if len(active) > maxDots {
		prefix = fmt.Sprintf("%s+%d", prefix, len(active)-maxDots)
	}

	if providerTitle == "" {
		return prefix
	}
	return prefix + " | " + providerTitle
}

// isStatusBarActive 判断会话状态是否需要在状态栏圆点中展示。
// done/idle 视为已结束，不上状态栏（仅菜单内可见）。
func isStatusBarActive(st string) bool {
	switch st {
	case claude.StateWorking, claude.StateBlocked, claude.StateFailed, claude.StateStopped:
		return true
	}
	return false
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
