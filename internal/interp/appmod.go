// v1.7 应用模块：内置模块 应用/app —— 桌面应用与手机端。
//
// 桌面应用：应用.窗口(目录) —— 把目录变成一个本地网站并在系统默认浏览器里打开，
// 对用户就像打开了一个应用窗口（浏览器壳模型，Electron 的零依赖版）。
//
// 手机端：应用.手机(目录) —— 监听 0.0.0.0，打印局域网网址，手机连同一 Wi-Fi
// 用浏览器打开即可使用；配合 PWA 清单可"添加到主屏幕"像原生 App 一样启动。
package interp

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"sahou/internal/errs"
)

func appModule() *Dict {
	m := newMod("应用", "app")
	// 应用.打开(网址)：用系统默认浏览器打开
	m.fn("打开", "open", [][]string{{"网址!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		url, e := needTextArg(args, 0, "应用", "打开", line)
		if e != nil {
			return nil, e
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = "http://" + url
		}
		openBrowser(url)
		return nil, nil
	})
	// 应用.窗口(目录, 端口: 0=自动, 静默: 假)：桌面应用——本地服务 + 打开浏览器窗口
	m.fn("窗口", "window", [][]string{{"目录!"}, {"端口", "port"}, {"静默", "silent"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		dir, e := needTextArg(args, 0, "应用", "窗口", line)
		if e != nil {
			return nil, e
		}
		port := 0
		if a, ok := opt(args, 1); ok {
			n, e := needIntMod(args, 1, "应用", "窗口", line)
			if e != nil {
				return nil, e
			}
			_ = a
			port = n
		}
		silent := false
		if a, ok := opt(args, 2); ok {
			silent = Truthy(a)
		}
		return serveApp(in.ScriptPath(dir), "127.0.0.1", port, silent, line)
	})
	// 应用.原生窗口(目录, 标题, 宽: 1000, 高: 700)：WebView2 真窗口（无浏览器地址栏）
	m.fn("原生窗口", "native_window", [][]string{{"目录!"}, {"标题", "title"}, {"宽", "width"}, {"高", "height"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		dir, e := needTextArg(args, 0, "应用", "原生窗口", line)
		if e != nil {
			return nil, e
		}
		title := dir
		if a, ok := opt(args, 1); ok {
			if t, ok2 := a.(string); ok2 {
				title = t
			}
		}
		w, h := 1000, 700
		if _, ok := opt(args, 2); ok {
			if n, e := needIntMod(args, 2, "应用", "原生窗口", line); e == nil {
				w = n
			} else {
				return nil, e
			}
		}
		if _, ok := opt(args, 3); ok {
			if n, e := needIntMod(args, 3, "应用", "原生窗口", line); e == nil {
				h = n
			} else {
				return nil, e
			}
		}
		port, err := freePort("127.0.0.1")
		if err != nil {
			return nil, errs.RuntimeHint("找不到空闲端口。", "no free port found.", "", line)
		}
		url := fmt.Sprintf("http://127.0.0.1:%d/", port)
		handler := http.FileServer(http.Dir(in.ScriptPath(dir)))
		go func() { _ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), handler) }()
		runNativeWindow(url, title, w, h)
		return nil, nil
	})
	// 应用.写页面(标题, 宽: 420, 高: 320)：纯代码写图形页面（标签/按钮/输入框+事件）
	m.fn("写页面", "page", [][]string{{"标题!", "title!"}, {"宽", "width"}, {"高", "height"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		return uiNewPage(in, args, line)
	})
	// 应用.手机(目录, 端口: 8000)：手机端——监听局域网并打印手机可访问的网址
	m.fn("手机", "mobile", [][]string{{"目录!"}, {"端口", "port"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		dir, e := needTextArg(args, 0, "应用", "手机", line)
		if e != nil {
			return nil, e
		}
		port := 8000
		if a, ok := opt(args, 1); ok {
			_ = a
			n, e := needIntMod(args, 1, "应用", "手机", line)
			if e != nil {
				return nil, e
			}
			port = n
		}
		return serveApp(in.ScriptPath(dir), "0.0.0.0", port, false, line)
	})
	return m.d
}

// serveApp 起静态服务并（可选）开浏览器；host 为 0.0.0.0 时打印局域网网址。
func serveApp(dir, host string, port int, silent bool, line int) (Value, *errs.Error) {
	if port == 0 {
		p, err := freePort(host)
		if err != nil {
			return nil, errs.RuntimeHint(
				"找不到空闲端口。", "no free port found.", "", line)
		}
		port = p
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if host == "0.0.0.0" {
		if ips := lanIPs(); len(ips) > 0 {
			fmt.Printf("手机端已启动：手机连同一 Wi-Fi，用浏览器打开 http://%s:%d/\n", ips[0], port)
			for _, ip := range ips[1:] {
				fmt.Printf("  也可用：http://%s:%d/\n", ip, port)
			}
			fmt.Println("（在手机浏览器里选\"添加到主屏幕\"，就能像 App 一样启动。）")
		} else {
			fmt.Printf("手机端已启动：http://127.0.0.1:%d/（没找到局域网地址，手机可能连不上）\n", port)
		}
	} else {
		fmt.Printf("应用已启动：%s （关闭这个窗口或按 Ctrl+C 退出）\n", url)
	}
	if !silent {
		openBrowser(url)
	}
	handler := http.FileServer(http.Dir(dir))
	if err := http.ListenAndServe(addr, handler); err != nil {
		return nil, errs.RuntimeHint(
			fmt.Sprintf("应用在 %s 上启动失败：%s。", addr, err.Error()),
			fmt.Sprintf("the app failed to start on %s.", addr),
			"通常是端口被占用：换一个端口，或让端口=0 自动分配。", line)
	}
	return nil, nil
}

func freePort(host string) (int, error) {
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	l, err := net.Listen("tcp", host+":0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// lanIPs 列出局域网 IPv4 地址（IPv4 且非环回）。
func lanIPs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
				out = append(out, ipnet.IP.String())
			}
		}
	}
	return out
}

// openBrowser 用系统默认浏览器打开网址（跨平台）。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func registerAppModule(in *Interp) {
	m := appModule()
	in.Globals.Set("应用", m)
	in.Globals.Set("app", m)
	in.builtinMods["应用"] = m
	in.builtinMods["app"] = m
}
