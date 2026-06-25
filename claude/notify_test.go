package claude

import (
	"sync"
	"testing"
)

// recorder 记录所有通知调用，用于断言。
type recorder struct {
	mu    sync.Mutex
	calls []struct{ title, body string }
}

func (r *recorder) notify(title, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, struct{ title, body string }{title, body})
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// newTestNotifier 构造一个通知器，notify 回调记录到 recorder。
func newTestNotifier(r *recorder) *Notifier {
	return NewNotifier(nil, r.notify)
}

// 首次 CheckAndNotify 应只建立 baseline，不发出任何通知，
// 即使会话已处于 blocked/done 等终态。
func TestNotifier_FirstCallIsSilentBaseline(t *testing.T) {
	r := &recorder{}
	n := newTestNotifier(r)

	current := map[int]*Session{
		100: {PID: 100, CWD: "/proj/a", State: StateBlocked, WaitingFor: "dialog open"},
		200: {PID: 200, CWD: "/proj/b", State: StateDone},
	}
	n.CheckAndNotify(current)

	if got := r.count(); got != 0 {
		t.Fatalf("首次调用不应发通知，实际发了 %d 条", got)
	}
}

// 首次 baseline 后，状态未变化时不应重复通知。
func TestNotifier_NoChangeNoNotify(t *testing.T) {
	r := &recorder{}
	n := newTestNotifier(r)

	sessions := map[int]*Session{
		100: {PID: 100, CWD: "/proj/a", State: StateDone},
	}
	n.CheckAndNotify(sessions) // baseline
	n.CheckAndNotify(sessions) // 无变化

	if got := r.count(); got != 0 {
		t.Fatalf("状态未变化不应发通知，实际发了 %d 条", got)
	}
}

// 首次 baseline 后，working → blocked 转换应触发"等待输入"通知。
func TestNotifier_WorkingToBlockedNotifies(t *testing.T) {
	r := &recorder{}
	n := newTestNotifier(r)

	working := map[int]*Session{
		100: {PID: 100, CWD: "/proj/a", State: StateWorking},
	}
	n.CheckAndNotify(working) // baseline

	blocked := map[int]*Session{
		100: {PID: 100, CWD: "/proj/a", State: StateBlocked, WaitingFor: "确认覆盖"},
	}
	n.CheckAndNotify(blocked)

	if got := r.count(); got != 1 {
		t.Fatalf("应发 1 条通知，实际 %d", got)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls[0].title != "Claude Code 等待输入 · a" {
		t.Errorf("通知标题不匹配: %q", r.calls[0].title)
	}
	if r.calls[0].body != "确认覆盖" {
		t.Errorf("通知正文应为 WaitingFor，实际: %q", r.calls[0].body)
	}
}
