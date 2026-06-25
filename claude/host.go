package claude

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/progrium/darwinkit/kernel"
	"github.com/progrium/darwinkit/macos/appkit"
)

// HostApp 表示启动某个 Claude 会话的宿主 GUI 应用（终端/IDE）。
type HostApp struct {
	BundleID string // 如 com.apple.Terminal
	Name     string // 如 Terminal
	GUIPID   int    // 宿主应用进程 ID
}

// DetectHostApp 从 Claude 会话的 pid 出发，沿进程树向上遍历，
// 返回最近的 GUI 应用（终端/IDE）。未找到则返回 nil。
//
// 实现思路：NSRunningApplication 只能查询 GUI 应用；对进程树中每个 pid
// 查询 RunningApplication，第一个命中的即为宿主。无需维护 bundleID 白名单。
//
// 注意：此函数调用 appkit API，必须在主线程执行。
func DetectHostApp(pid int) *HostApp {
	const maxDepth = 16 // 防御性上限，避免异常进程树死循环
	cur := pid
	for i := 0; cur > 1 && i < maxDepth; i++ {
		app := appkit.RunningApplication_RunningApplicationWithProcessIdentifier(kernel.Pid(cur))
		// 非 GUI 进程（shell、CLI、已退出进程）会返回 nil 对象，必须先判空，
		// 否则对其调用 BundleIdentifier() 会 panic（objc.Call 收到 nil receiver）。
		if app.IsNil() {
			ppid, err := getParentPID(cur)
			if err != nil {
				return nil
			}
			cur = ppid
			continue
		}
		// 命中 GUI 应用：bundleID 非空即表示该 pid 是一个已注册的 .app
		if bid := app.BundleIdentifier(); bid != "" {
			return &HostApp{
				BundleID: bid,
				Name:     app.LocalizedName(),
				GUIPID:   cur,
			}
		}
		var err error
		cur, err = getParentPID(cur)
		if err != nil {
			return nil
		}
	}
	return nil
}

// getParentPID 返回指定进程的父 PID。
// 通过 fork `ps -o ppid= -p <pid>` 实现，避免引入 cgo。
func getParentPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, err
	}
	return ppid, nil
}

// IsHostAppForeground 判断给定宿主 GUI 应用是否当前在前台。
// 必须在主线程执行（NSWorkspace 约束）。
func IsHostAppForeground(host *HostApp) bool {
	if host == nil {
		return false
	}
	front := appkit.Workspace_SharedWorkspace().FrontmostApplication()
	// FrontmostApplication 在极端情况下（如无 GUI 会话）可能返回 nil
	if front.IsNil() {
		return false
	}
	if front.ProcessIdentifier() == 0 {
		return false
	}
	return int(front.ProcessIdentifier()) == host.GUIPID
}
