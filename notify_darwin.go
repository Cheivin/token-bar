package main

// macOS 原生桌面通知实现。
//
// 用 darwinkit 直接调用 NSUserNotificationCenter（已废弃但仍可用，
// darwinkit 未为其生成高级绑定，故用 objc.Call 直调）。
// 相比 beeep/osascript 回退路径，原生 API 的通知归属本应用 bundle
// （com.cheivin.token-bar），标题与图标自然正确，不会显示"脚本编辑器"。

import (
	"log"

	"github.com/progrium/darwinkit/dispatch"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
)

// 应用 bundle id。打包成 .app 时由 Info.plist 提供；
// 裸运行（go run / dlv debug）时 mainBundle 无 Info.plist，bundle id 为空，
// 需要在 initUserNotification 里手动注入，否则 NSUserNotificationCenter
// 的 defaultUserNotificationCenter 会返回 nil（见 darwinkit notification example
// 对 bundleIdentifier 的说明）。
const appBundleID = "com.cheivin.token-bar"

// notificationInited 标记 bundle id 注入是否已完成，避免重复替换方法。
var notificationInited bool

// initUserNotification 在主线程调用一次，确保进程对通知系统可见。
// 打包运行时 mainBundle 已有 bundle id，跳过注入；
// 裸运行时 bundle id 为空，注入 appBundleID 让通知系统能识别本进程。
func initUserNotification() {
	if notificationInited {
		return
	}
	notificationInited = true

	if id := foundation.Bundle_MainBundle().BundleIdentifier(); id != "" {
		return // .app 运行，bundle id 已就绪
	}
	// 仿 darwinkit notification example：替换 NSBundle 的 bundleIdentifier 方法
	nsbundle := foundation.Bundle_MainBundle().Class()
	objc.ReplaceMethod(nsbundle, objc.Sel("bundleIdentifier"), func(_ objc.IObject) string {
		return appBundleID
	})
}

// notifyUser 发送一条桌面通知。
// 可在任意 goroutine 调用：内部通过 dispatch.MainQueue 派发到主线程执行
// （NSUserNotificationCenter 必须在主线程使用）。立即返回，不等待派发完成。
func notifyUser(title, body string) error {
	dispatch.MainQueue().DispatchAsync(func() {
		initUserNotification()

		objc.WithAutoreleasePool(func() {
			center := objc.Call[objc.Object](
				objc.GetClass("NSUserNotificationCenter"),
				objc.Sel("defaultUserNotificationCenter"),
			)
			// 防御：bundle id 注入仍失败或通知被禁用时 center 可能为 nil，
			// 直接跳过避免 objc.Call 触发 "object is nil" panic。
			if center.Ptr() == nil {
				log.Printf("claude: 通知中心不可用，跳过通知（bundle id 未注入或通知被系统禁用）")
				return
			}

			notif := objc.Call[objc.Object](objc.GetClass("NSUserNotification"), objc.Sel("new"))
			notif.Autorelease()
			objc.Call[objc.Void](notif, objc.Sel("setTitle:"), title)
			objc.Call[objc.Void](notif, objc.Sel("setInformativeText:"), body)
			objc.Call[objc.Void](center, objc.Sel("deliverNotification:"), notif)
		})
	})
	return nil
}
