package claude

import (
	"log"
	"sync"

	"github.com/gen2brain/beeep"
)

// NotifyTrigger 描述一次需要发通知的状态转换类型。
type NotifyTrigger int

const (
	TriggerNone    NotifyTrigger = iota
	TriggerBlocked               // 进入等待用户输入
	TriggerDone                  // 任务完成
	TriggerFailed                // 执行失败
)

// Notifier 跟踪每个会话的上一次状态，检测转换并触发桌面通知。
//
// 通知触发条件（与 CC-Status 一致）：
//   - working → blocked：宿主终端不在前台时通知"等待输入"
//   - → done：宿主不在前台时通知"任务完成"
//   - → failed：宿主不在前台时通知"执行失败"
type Notifier struct {
	mu       sync.Mutex
	prev     map[int]string // pid → 上一次 EffectiveState
	hostCheck func(*Session) *HostApp // 可注入的宿主检测（主线程回调），默认 nil=始终认为不在前台
}

// NewNotifier 创建通知器。
// hostCheck 必须在主线程执行 NSWorkspace 调用，由调用方注入；
// 若为 nil 则视为"宿主永不在前台"，即总是发通知。
func NewNotifier(hostCheck func(*Session) *HostApp) *Notifier {
	return &Notifier{
		prev:      make(map[int]string),
		hostCheck: hostCheck,
	}
}

// CheckAndNotify 对照当前会话集合，对发生关注转换的会话发通知。
// 应在主线程调用（因 hostCheck 内部调用 NSWorkspace）。
func (n *Notifier) CheckAndNotify(current map[int]*Session) {
	n.mu.Lock()
	prev := n.prev
	// 先算出要发的通知，再异步发（beeep 内部 fork 子进程，不阻塞主线程）
	type pending struct {
		title string
		body  string
	}
	var pendings []pending

	for pid, s := range current {
		prevState := prev[pid]
		currState := s.EffectiveState()
		trigger, title, body := n.classify(prevState, s)
		if trigger == TriggerNone {
			continue
		}
		// 宿主前台检测：宿主在前台时抑制通知
		if n.hostCheck != nil {
			host := n.hostCheck(s)
			if host != nil && IsHostAppForeground(host) {
				continue
			}
		}
		pendings = append(pendings, pending{title: title, body: body})
		_ = currState
	}

	// 更新上一次状态：以当前集合为准（消失的会话会被清理）
	n.prev = make(map[int]string, len(current))
	for pid, s := range current {
		n.prev[pid] = s.EffectiveState()
	}
	n.mu.Unlock()

	// 异步发通知，避免阻塞主线程
	for _, p := range pendings {
		go func(title, body string) {
			if err := beeep.Notify(title, body, ""); err != nil {
				log.Printf("claude: 发送通知失败: %v", err)
			}
		}(p.title, p.body)
	}
}

// classify 根据状态转换判定是否需要通知及通知内容。
// 返回 (trigger, title, body)；trigger==TriggerNone 表示无需通知。
func (n *Notifier) classify(prevState string, s *Session) (NotifyTrigger, string, string) {
	curr := s.EffectiveState()
	project := s.ProjectName()

	// working → blocked：进入等待用户输入
	if curr == StateBlocked && prevState != StateBlocked {
		body := "等待用户输入"
		if s.WaitingFor != "" {
			body = s.WaitingFor
		}
		return TriggerBlocked, "Claude Code 等待输入 · " + project, body
	}

	// → done：任务完成（仅从非终态进入才通知，避免重复）
	if s.IsDone() && !isFinishedState(prevState) {
		return TriggerDone, "Claude Code 任务完成 · " + project, ""
	}

	// → failed：执行失败
	if s.IsFailed() && prevState != StateFailed {
		return TriggerFailed, "Claude Code 执行失败 · " + project, ""
	}

	return TriggerNone, "", ""
}

// isFinishedState 判断旧状态是否属于"已完成"类（用于避免 done 重复通知）。
func isFinishedState(state string) bool {
	switch state {
	case StateDone, StateFailed, StateStopped, StateIdle:
		return true
	}
	return false
}
