# Token Bar

macOS 状态栏 API 用量监控工具，实时显示 GLM/Z.AI、NewAPI 等平台的配额使用情况。

## 功能

- **状态栏实时监控** — 直接在 macOS 状态栏显示 API 用量
- **多数据源支持** — GLM (智谱/Z.AI)、NewAPI 平台、自定义命令
- **自动刷新** — 可配置刷新间隔
- **多 Provider** — 支持同时监控多个账户/平台
- **开机自启** — 系统启动时自动运行

## 安装

### Homebrew

```bash
brew tap cheivin/token-bar
brew install token-bar
```

### 手动安装

从 [Releases](https://github.com/Cheivin/token-bar/releases) 下载 `Token-Bar-x.x.x.zip`，解压后将 `Token-Bar.app` 拖入应用程序文件夹。

### 从源码构建

```bash
# 编译当前架构
make build

# 编译通用二进制（Apple Silicon + Intel）
make build-universal

# 打包 .app
make app

# 打包发布 zip
make package
```

## 配置

配置文件位于 `~/.token-bar/config.yaml`（首次运行自动创建模板）。

### 配置示例

```yaml
refresh_interval: 5m

providers:
  # GLM 平台（智谱 AI 或 Z.AI）
  - name: GLM用量
    type: glm
    primary: true                    # 主 provider，显示在状态栏
    refresh_interval: 5m             # 可选，覆盖全局刷新间隔
    params:
      api_key: "your-api-key"        # GLM API Key
      platform: "zhipu"              # zhipu（默认）或 z.ai
      # base_url: "https://..."      # 可选，自定义 API 地址

  # NewAPI 平台（One API / New API）
  - name: OpenAI
    type: newapi
    primary: false
    params:
      base_url: "https://api.example.com"
      token: "your-token"
      user_id: "1"                   # 可选，部分平台需要

  # 自定义命令（输出 JSON）
  - name: 自定义脚本
    type: exec
    params:
      command: "./my-script.sh"      # 相对于 ~/.token-bar/ 目录
      timeout: "10s"

  # OpenCode Go 套餐（opencode.ai）
  - name: OpenCode
    type: opencode
    params:
      workspace_id: "wrk_xxxxxxxx"   # workspace ID
      auth_cookie: "Fe26.2*..."      # 浏览器登录后的 auth cookie（会过期）
```

### Provider 类型

| 类型 | 说明 | 必需参数 |
|------|------|----------|
| `glm` | GLM/Z.AI 用量 | `api_key` |
| `newapi` | NewAPI 平台 | `base_url`, `token` |
| `opencode` | OpenCode Go 套餐 | `workspace_id`, `auth_cookie` |
| `exec` | 自定义命令 | `command` |

### GLM Provider

查询智谱 AI 或 Z.AI 平台的用量：

- **5小时窗口** Token 配额及重置时间
- **每周** Token 配额及重置时间
- **MCP** 每月调用配额
- **7/30天** 请求次数和 Token 统计

`platform` 参数：
- `zhipu` — 智谱开放平台 (`open.bigmodel.cn`)
- `z.ai` — Z.AI 平台 (`api.z.ai`)

### NewAPI Provider

适配 [New API](https://github.com/Calcium-Ion/new-api) / [One API](https://github.com/songquanpeng/one-api) 平台：

- 当日消耗
- 账户余额
- 已用总量
- RPM/TPM 统计

### OpenCode Provider

监控 [opencode.ai](https://opencode.ai) Go 套餐用量，复用浏览器已登录的 auth cookie 请求用量页面：

- **滚动用量** / **每周用量** / **每月用量** 三窗口使用率及重置时间
- 状态非 `ok`（如 `rate-limited`）时对应窗口红色高亮

参数说明：
- `workspace_id` — 形如 `wrk_xxx` 的 workspace ID，可从用量页 URL 取得
- `auth_cookie` — 浏览器登录后的 `auth` cookie 值（Iron 加密格式，形如 `Fe26.2*...`）。**会过期**，失效时（HTTP 401/403）只需更新这一项，无需重启应用

> 获取方式：浏览器登录 opencode.ai 后，打开 DevTools → Application → Cookies，复制 `auth` 的值；或从 Network 请求头的 `Cookie` 中提取 `auth=...`。

### Exec Provider

执行外部命令并解析 JSON 输出，格式：

```json
{
  "title": "主信息",
  "subtitle": "tooltip 说明",
  "items": [
    {"label": "标签", "value": "值"}
  ]
}
```

## 菜单

- **Primary Provider** — 展开为一级菜单项，主信息同时显示在状态栏
- **Secondary Provider** — 作为子菜单显示
- **设置**
  - 立即刷新
  - 重新加载配置
  - 打开配置文件
  - 开机自启
- **退出**

## 开发

### 项目结构

```
.
├── main.go           # 入口，启动 darwinkit 事件循环
├── app.go            # 应用核心：菜单构建、事件循环、定时刷新
├── config.go         # 配置加载/保存
├── autolaunch.go     # 开机自启管理
└── provider/
    ├── provider.go   # Provider 接口与工具函数
    ├── glm.go        # GLM/Z.AI 实现
    ├── newapi.go     # NewAPI 平台实现
    ├── opencode.go   # OpenCode Go 套餐实现
    └── exec.go       # 自定义命令实现
```

### Provider 接口

```go
type Provider interface {
    Fetch() (*ProviderResult, error)
}

type ProviderResult struct {
    Title    string     // 状态栏主信息
    Subtitle string     // tooltip
    Items    []InfoItem // 菜单详情
}

type InfoItem struct {
    Label string
    Value string
}
```

## License

MIT
