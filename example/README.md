# Example Scripts

此目录包含 exec provider 的示例脚本，用于演示如何创建自定义数据源。

## 使用方法

1. 将脚本复制到 `~/.token-bar/` 目录
2. 添加执行权限：`chmod +x ~/.token-bar/system-monitor.sh`
3. 在配置文件中添加 provider：

```yaml
providers:
  - name: 系统监控
    type: exec
    primary: false
    params:
      command: "./system-monitor.sh"
      timeout: "5s"
```

## 脚本说明

### system-monitor.sh

监控 macOS 系统 CPU 和内存使用情况。

**输出示例：**

```json
{
  "title": "CPU 25.5% | MEM 68.2%",
  "subtitle": "系统资源监控",
  "items": [
    {"label": "CPU", "value": "25.5%"},
    {"label": "内存", "value": "10.5GB / 16.0GB"},
    {"label": "内存使用率", "value": "68.2%"},
    {"label": "负载", "value": "2.15"},
    {"label": "进程数", "value": "352"}
  ]
}
```

## 自定义脚本开发

exec provider 执行脚本并解析标准输出的 JSON 内容。

### 输出格式

```json
{
  "title": "状态栏显示的主信息",
  "subtitle": "鼠标悬停时的提示信息（可选）",
  "items": [
    {"label": "标签1", "value": "值1"},
    {"label": "标签2", "value": "值2"}
  ]
}
```

### 要求

1. 脚本必须输出有效的 JSON 到标准输出
2. 工作目录为 `~/.token-bar/`
3. 超时时间可通过 `timeout` 参数配置，默认 10 秒

### 示例：简单的天气脚本

```bash
#!/bin/bash
# weather.sh - 获取天气信息

city="${1:-Beijing}"
weather=$(curl -s "wttr.in/${city}?format=%t+%C")

cat <<EOF
{
  "title": "${weather}",
  "subtitle": "天气 - ${city}",
  "items": [
    {"label": "城市", "value": "${city}"},
    {"label": "天气", "value": "${weather}"}
  ]
}
EOF
```

配置：

```yaml
providers:
  - name: 天气
    type: exec
    params:
      command: "./weather.sh Beijing"
```
