//go:build !windows

// 非 Windows 平台：没有 WebView2，原生窗口退回系统默认浏览器。
package interp

import "fmt"

func runNativeWindow(url, title string, w, h int) {
	fmt.Printf("此平台没有 WebView2，改用浏览器窗口：%s（%s）\n", title, url)
	openBrowser(url)
	// 浏览器模式没有"关窗"事件，阻塞由调用方的 ListenAndServe 承担
	select {} //nolint: 由进程退出结束
}
