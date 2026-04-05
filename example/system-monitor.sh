#!/bin/bash
# 系统资源监控示例脚本
# 输出 JSON 格式，供 exec provider 使用

# 获取 CPU 使用率 (macOS)
get_cpu_usage() {
    # 使用 top 命令获取 CPU 空闲率，然后计算使用率
    cpu_idle=$(top -l 1 -n 0 | grep "CPU usage" | awk '{print $7}' | sed 's/%//')
    if [ -n "$cpu_idle" ]; then
        echo "$(echo "100 - $cpu_idle" | bc)"
    else
        echo "0"
    fi
}

# 获取内存使用情况 (macOS)
get_memory_info() {
    # 使用 vm_stat 获取内存信息
    page_size=4096
    mem_total=$(sysctl -n hw.memsize)
    mem_total_mb=$((mem_total / 1024 / 1024))
    
    # 获取已使用内存
    pages_free=$(vm_stat | grep "Pages free" | awk '{print $3}' | sed 's/\.//')
    pages_active=$(vm_stat | grep "Pages active" | awk '{print $3}' | sed 's/\.//')
    pages_inactive=$(vm_stat | grep "Pages inactive" | awk '{print $3}' | sed 's/\.//')
    pages_speculative=$(vm_stat | grep "Pages speculative" | awk '{print $3}' | sed 's/\.//')
    pages_wired=$(vm_stat | grep "Pages wired down" | awk '{print $3}' | sed 's/\.//')
    pages_compressed=$(vm_stat | grep "Pages occupied by compressor" | awk '{print $5}' | sed 's/\.//')
    
    # 计算已使用内存 (active + wired + compressed)
    [ -z "$pages_active" ] && pages_active=0
    [ -z "$pages_wired" ] && pages_wired=0
    [ -z "$pages_compressed" ] && pages_compressed=0
    
    mem_used=$(( (pages_active + pages_wired + pages_compressed) * page_size / 1024 / 1024 ))
    mem_used_gb=$(echo "scale=2; $mem_used / 1024" | bc)
    mem_total_gb=$(echo "scale=2; $mem_total_mb / 1024" | bc)
    mem_percent=$(echo "scale=1; $mem_used * 100 / $mem_total_mb" | bc)
    
    echo "$mem_used_gb|$mem_total_gb|$mem_percent"
}

# 获取进程数
get_process_count() {
    ps aux | wc -l | tr -d ' '
}

# 获取负载
get_load_avg() {
    sysctl -n vm.loadavg | awk '{print $2}'
}

# 主逻辑
cpu_usage=$(get_cpu_usage)
mem_info=$(get_memory_info)
mem_used=$(echo "$mem_info" | cut -d'|' -f1)
mem_total=$(echo "$mem_info" | cut -d'|' -f2)
mem_percent=$(echo "$mem_info" | cut -d'|' -f3)
process_count=$(get_process_count)
load_avg=$(get_load_avg)

# 输出 JSON
cat <<EOF
{
  "title": "CPU ${cpu_usage}% | MEM ${mem_percent}%",
  "subtitle": "系统资源监控",
  "items": [
    {"label": "CPU", "value": "${cpu_usage}%"},
    {"label": "内存", "value": "${mem_used}GB / ${mem_total}GB"},
    {"label": "内存使用率", "value": "${mem_percent}%"},
    {"label": "负载", "value": "${load_avg}"},
    {"label": "进程数", "value": "${process_count}"}
  ]
}
EOF
