# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Token Bar 是一个 macOS 状态栏应用，实时显示 GLM（智谱/Z.AI）、NewAPI 等平台的 API 用量配额。基于 `github.com/progrium/darwinkit`（原生 macOS API 的 Go 绑定）构建，使用 Go 开发，以 macOS .app bundle 形式分发。

## 常用命令

```bash
make build              # 编译当前架构二进制
make build-universal    # 编译通用二进制（Apple Silicon + Intel）
make app                # 编译并打包 .app（含图标）
make package            # 打包为 dist/Token-Bar-x.x.x.zip
make clean              # 清理构建产物
```

注意：`build-universal` 和 `app` 需要 CGO（darwinkit 依赖 Objective-C 绑定），且需要 clang 支持交叉编译。

> **⚠️ 构建必须用 `-gcflags="all=-N -l"`**：Go 1.25+ 的 arm64 编译器有优化器 bug，为 darwinkit 的 libffi cgo 调用生成错误代码，导致 `Application.Run()` 运行时 SIGABRT（见 [progrium/darwinkit#286](https://github.com/progrium/darwinkit/issues/286)）。Makefile 的 `GCFLAGS` 变量已默认关闭优化与内联，**不要在构建命令中省略它**（直接 `go build .` 会产生崩溃二进制）。

## 架构

### 运行流程

`main.go` 启动 → `macos.RunApp()` 锁定主线程并进入 macOS 事件循环（阻塞）→ 回调中 `LoadConfig()` 加载 `~/.token-bar/config.yaml`，创建 `NSStatusItem` 后构建菜单。配置重载改为**原地重建**：停止刷新 goroutine → 重新加载 → 重建 providers → `RemoveAllItems()` 重建菜单（不再退出进程重启）。

所有 UI 操作必须在主线程：后台 goroutine（网络请求、定时器）拿到数据后通过 `dispatch.MainQueue().DispatchAsync(func(){...})` 派发回主线程更新菜单。

### Provider 模式

所有数据源通过 `provider.Provider` 接口统一抽象：

```go
type Provider interface {
    Fetch() (*ProviderResult, error)
}
```

- `provider/glm.go` — GLM/Z.AI 平台（配额限制 + 模型用量 + 工具用量三个 API 聚合）
- `provider/newapi.go` — NewAPI/OneAPI 平台（status + user/self + log/stat 三个 API 聚合）
- `provider/exec.go` — 执行外部命令解析 JSON 输出
- `provider/opencode.go` — OpenCode Go 套餐（请求用量页面，正则解析 SolidJS SSR 注入的 hydration 数据）。params：`workspace_id`（`wrk_xxx`）、`auth_cookie`（hapi Iron 加密的 auth 值，会过期，过期时只改这一项）。仅带 auth cookie + user-agent 两个最小请求头。

`provider.NewProvider()` 工厂函数根据 `ProviderConfig.Type` 字段分发创建。新增数据源只需实现 `Provider` 接口并在工厂函数中注册。

### 配置系统

`config.go` 管理 `~/.token-bar/` 目录下的配置文件，支持 YAML/JSON 双格式自动检测，兼容旧版单 api_key 格式自动迁移。每个 provider 可覆盖全局 `refresh_interval`。

### 菜单结构

单个常驻 `NSStatusItem` + `NSMenu`。Primary provider 的数据展示在状态栏按钮标题（`button.SetTitle`）和一级菜单，Secondary provider 作为子菜单（`NewSubMenuItem`）。刷新时 `menu.RemoveAllItems()` 后按最新数据全量重建，每项最多展示 8 条 + 更新时间。设置菜单项点击通过 `NewMenuItemWithAction` 的闭包回调绑定（替代 systray 的 `ClickedCh` 通道）。

### darwinkit 对象生命周期（重要）

darwinkit 的 `New...`/工厂方法创建的对象默认是 **autorelease** 的，会在当前 autorelease pool 结束时被 Objective-C 运行时释放。若把这种对象的指针长期保存（如存进结构体字段，跨函数、跨 goroutine 使用），底层的 ObjC 对象可能已被释放，再次访问就是野指针，表现为 `objc.Object.Class()` 处 SIGTRAP。

常驻对象（`NSStatusItem`、`NSMenu`、`StatusBarButton`）必须用 `objc.Retain(&obj)` 持有：它既增加 ObjC 引用计数，又设置 Go finalizer 防止 GC 回收。`objc.Retain` 要求传入**独立堆对象**（不能是结构体中间的字段地址），所以 `app` 结构体里这些字段声明为指针类型（`*appkit.StatusItem` 等），在创建时用局部变量接收、Retain 后再取地址赋值。详见 `createStatusItem()`。

`NewMenuItemWithAction` 不接受 `nil` handler（内部 `action.Wrap(nil)` 会 panic）。纯展示菜单项用 `NewMenuItemWithSelector(title, "", objc.Selector{})`（空 selector，无 action）并 `SetEnabled(false)`。

## 关键约定

- 配置文件路径：`~/.token-bar/config.yaml`（首次运行自动创建模板）
- 图标源文件：`packaging/icon.jpg`，通过 `sips` + `iconutil` 生成 `.icns`
- 版本号在 `Makefile` 和 `packaging/Info.plist` 中维护，需同步更新
- CI（`.github/workflows/release.yml`）在 GitHub Release 发布时自动构建和上传
- `LSUIElement=true`（Info.plist）使应用不显示 Dock 图标，仅驻留状态栏
