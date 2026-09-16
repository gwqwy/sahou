//go:build windows

// 原生窗口（Windows）：WebView2 真窗口 —— 系统自带运行时（Win10/11 预装），无 CGO。
package interp

import (
	"fmt"

	webview "github.com/jchv/go-webview2"
)

// runNativeWindow 打开 WebView2 原生窗口（阻塞到关窗）。
func runNativeWindow(url, title string, w, h int) {
	fmt.Printf("原生窗口已打开：%s（%d×%d，关闭窗口即退出）\n", title, w, h)
	wv := webview.New(false)
	wv.SetTitle(title)
	wv.SetSize(w, h, webview.HintNone)
	wv.Navigate(url)
	wv.Run()
	wv.Destroy()
}

// runNativePage 打开"纯代码写页面"的原生窗口（阻塞到关窗）：
// 绑定 __saho_event（用户事件 → 卅 事件函数）与 __saho_sync（输入镜像同步）；
// 绑定完成后回调 onReady，此后页面方法可以往窗口里 Eval。
func runNativePage(spec uiWindowSpec) {
	fmt.Printf("页面已打开：%s（%d×%d，关闭窗口即继续）\n", spec.title, spec.w, spec.h)
	wv := webview.New(false)
	wv.SetTitle(spec.title)
	wv.SetSize(spec.w, spec.h, webview.HintNone)
	_ = wv.Bind("__saho_event", func(payload string) string { return spec.onEvent(payload) })
	_ = wv.Bind("__saho_sync", func(payload string) string { spec.onSync(payload); return "ok" })
	if spec.onReady != nil {
		spec.onReady(wv)
	}
	wv.SetHtml(spec.html)
	wv.Run()
	wv.Destroy()
}
