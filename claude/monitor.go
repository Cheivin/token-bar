// Package claude 监听 Claude Code CLI 的会话状态。
//
// Claude Code CLI 会为每个活跃会话在 ~/.claude/sessions/<pid>.json 写入
// 单行 JSON 快照。本包通过 fsnotify 监听该目录，事件驱动地解析所有快照，
// 聚合出当前所有会话的状态，供状态栏菜单展示与 blocked 状态通知使用。
package claude

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// 会话快照目录（与 Claude Code CLI 约定一致）。
func sessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".claude", "sessions")
}

// Session 对应一个 ~/.claude/sessions/<pid>.json 快照。
// 字段同时覆盖新版（state）与旧版（status）两种结构。
type Session struct {
	PID        int    `json:"pid"`
	CWD        string `json:"cwd"`
	Kind       string `json:"kind"`
	StartedAt  int64  `json:"startedAt"`
	SessionID  string `json:"sessionId"`
	State      string `json:"state"`      // 新版：working/blocked/done/failed/stopped
	Status     string `json:"status"`     // 旧版：busy/waiting/idle
	WaitingFor string `json:"waitingFor"` // 等待原因，如 "dialog open"
	ID         string `json:"id"`
	Name       string `json:"name"`
}

// 规范化的会话状态。跨新旧版本统一。
const (
	StateWorking = "working" // 正在执行任务
	StateBlocked = "blocked" // 等待用户输入/确认
	StateDone    = "done"    // 任务完成
	StateFailed  = "failed"  // 执行失败
	StateStopped = "stopped" // 已停止
	StateIdle    = "idle"    // 旧版空闲（视为 done）
)

// EffectiveState 统一新旧版本字段，返回规范状态。
// state 字段优先；缺失时用旧版 status 推断。
func (s *Session) EffectiveState() string {
	switch s.State {
	case StateWorking, StateBlocked, StateDone, StateFailed, StateStopped:
		return s.State
	}
	// 旧版兼容
	switch s.Status {
	case "busy":
		return StateWorking
	case "idle":
		return StateIdle
	case "waiting":
		// 旧版 waiting + waitingFor != "dialog open" 视为 blocked
		if s.WaitingFor != "" && s.WaitingFor != "dialog open" {
			return StateBlocked
		}
		// waitingFor == "dialog open" 也属于需要用户交互的 blocked
		return StateBlocked
	}
	return ""
}

// IsBlocked 当前是否需要用户输入。
func (s *Session) IsBlocked() bool { return s.EffectiveState() == StateBlocked }

// IsWorking 当前是否正在执行任务。
func (s *Session) IsWorking() bool { return s.EffectiveState() == StateWorking }

// IsFinished 当前是否处于终止状态（done/failed/stopped/idle）。
func (s *Session) IsFinished() bool {
	switch s.EffectiveState() {
	case StateDone, StateFailed, StateStopped, StateIdle:
		return true
	}
	return false
}

// IsFailed 当前是否执行失败。
func (s *Session) IsFailed() bool { return s.EffectiveState() == StateFailed }

// IsDone 当前是否完成。
func (s *Session) IsDone() bool {
	st := s.EffectiveState()
	return st == StateDone || st == StateIdle
}

// ProjectName 从 cwd 提取项目名（末尾目录段）。
func (s *Session) ProjectName() string {
	if s.CWD == "" {
		if s.Name != "" {
			return s.Name
		}
		return "unknown"
	}
	// 去掉末尾斜杠后取末段
	cwd := strings.TrimRight(s.CWD, "/")
	if i := strings.LastIndex(cwd, "/"); i >= 0 {
		return cwd[i+1:]
	}
	return cwd
}

// Monitor 事件驱动地监听 ~/.claude/sessions/ 目录。
type Monitor struct {
	mu       sync.RWMutex
	sessions map[int]*Session
	onChange func(map[int]*Session)
	watcher  *fsnotify.Watcher
	stop     chan struct{}
}

// NewMonitor 创建并启动监听器。onChange 在每次快照变化时回调（后台 goroutine 触发）。
// 若 sessions 目录不存在会尝试创建；创建失败仅记录日志，不中断。
func NewMonitor(onChange func(map[int]*Session)) (*Monitor, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	dir := sessionsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("claude: 创建 sessions 目录失败 %s: %v", dir, err)
	}
	if err := w.Add(dir); err != nil {
		log.Printf("claude: 监听 sessions 目录失败 %s: %v", dir, err)
	}

	m := &Monitor{
		sessions: make(map[int]*Session),
		onChange: onChange,
		watcher:  w,
		stop:     make(chan struct{}),
	}

	go m.loop()
	m.poll() // 首次立即读取已有快照
	return m, nil
}

// loop 事件循环：fsnotify 事件经 100ms debounce 后触发一次 poll，
// 避免 Claude 短时间内连续写多个文件导致重复扫描。
func (m *Monitor) loop() {
	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}

	for {
		select {
		case <-m.stop:
			return
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			// 文件创建/修改/删除/重命名都触发
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
				debounce.Reset(100 * time.Millisecond)
			}
		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				log.Printf("claude: fsnotify 错误: %v", err)
			}
		case <-debounce.C:
			m.poll()
		}
	}
}

// poll 全量读取 sessions 目录，解析所有 *.json，刷新快照并回调 onChange。
// 读取失败的文件直接跳过（可能是原子写未完成的中间态），下次事件重试。
func (m *Monitor) poll() {
	dir := sessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	current := make(map[int]*Session)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		// 必须有 state 或 status 字段才有效；pid 缺失则用文件名兜底
		if s.State == "" && s.Status == "" {
			continue
		}
		if s.PID == 0 {
			continue // 无 pid 无法标识会话
		}
		current[s.PID] = &s
	}

	m.mu.Lock()
	m.sessions = current
	m.mu.Unlock()

	if m.onChange != nil {
		m.onChange(current)
	}
}

// Sessions 返回当前所有会话的快照副本（线程安全）。
func (m *Monitor) Sessions() map[int]*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int]*Session, len(m.sessions))
	for k, v := range m.sessions {
		out[k] = v
	}
	return out
}

// Close 停止监听并释放 watcher。
func (m *Monitor) Close() {
	close(m.stop)
	_ = m.watcher.Close()
}
