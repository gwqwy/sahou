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
