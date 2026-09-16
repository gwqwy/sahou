//go:build !windows

// 非 Windows 平台：没有 WebView2，原生窗口退回系统默认浏览器。
package interp

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

func runNativeWindow(url, title string, w, h int) {
	fmt.Printf("此平台没有 WebView2，改用浏览器窗口：%s（%s）\n", title, url)
	openBrowser(url)
	// 浏览器模式没有"关窗"事件，阻塞由调用方的 ListenAndServe 承担
	select {} //nolint: 由进程退出结束
}

// uiEvent 事件桥收到的浏览器事件。
type uiEvent struct {
	sync    bool
	payload string
}

// runNativePage 非 Windows 实现：起本地 HTTP 服务，把页面交给浏览器，
// 事件与状态同步经 POST 上行（/事件、/同步），控件更新经 SSE 下行（/流），
// 页面隐藏/关闭时 sendBeacon("关闭") 通知退出（与"关窗即继续"同义）。
func runNativePage(spec uiWindowSpec) {
	fmt.Printf("此平台没有 WebView2，页面在浏览器里打开（事件经本地事件桥可用）：%s\n", spec.title)

	var (
		regMu  sync.Mutex
		sse    chan string // 当前 SSE 订阅者
		closed sync.Once
	)
	events := make(chan uiEvent, 64)
	evals := make(chan string, 64)
	done := make(chan struct{})

	// browserEval 实现 uiWebView：把 JS 命令排队，由主循环推给 SSE。
	// 所有页面方法（改文本/弹窗…）因此无需改动即可跨平台工作。
	browserEval := &funcWebView{fn: func(js string) {
		select {
		case evals <- js:
		default: // 队列满则丢弃（浏览器模式下极端情况）
		}
	}}

	mux := http.NewServeMux()
	post := func(path string, isSync bool) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, 4096)
			n, _ := r.Body.Read(buf)
			if n > 0 {
				select {
				case events <- uiEvent{sync: isSync, payload: string(buf[:n])}:
				default:
				}
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	post("/事件", false)
	post("/同步", true)
	mux.HandleFunc("/关闭", func(w http.ResponseWriter, r *http.Request) {
		closed.Do(func() { close(done) })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/流", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)
		if fl != nil {
			fl.Flush()
		}
		c := make(chan string, 32)
		regMu.Lock()
		sse = c
		regMu.Unlock()
		defer func() {
			regMu.Lock()
			if sse == c {
				sse = nil
			}
			regMu.Unlock()
		}()
		for js := range c {
			fmt.Fprintf(w, "data: %s\n\n", js)
			if fl != nil {
				fl.Flush()
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// 浏览器事件桥：替换 Bind 垫片（POST 上行 + SSE 下行 + 关窗信标）
		glue := `<script>
window.__saho_event = function(args){ fetch("/事件", {method: "POST", body: args}); };
window.__saho_sync = function(args){ fetch("/同步", {method: "POST", body: args}); };
const 桥 = new EventSource("/流");
桥.onmessage = function(e){ try { (0, eval)(e.data); } catch (err) {} };
addEventListener("pagehide", function(){ navigator.sendBeacon("/关闭"); });
</script>`
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Replace(spec.html, "</body>", glue+"</body>", 1)))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("事件桥起不了本地服务：", err)
		select {} //nolint: 由进程退出结束
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	url := "http://" + ln.Addr().String() + "/"
	if spec.onReady != nil {
		spec.onReady(browserEval)
	}
	openBrowser(url)

	for {
		select {
		case ev := <-events:
			if ev.sync {
				spec.onSync(ev.payload)
			} else {
				spec.onEvent(ev.payload)
			}
		case js := <-evals:
			regMu.Lock()
			c := sse
			regMu.Unlock()
			if c != nil {
				select {
				case c <- js:
				default:
				}
			}
		case <-done:
			regMu.Lock()
			if sse != nil {
				close(sse)
				sse = nil
			}
			regMu.Unlock()
			_ = srv.Close()
			return
		}
	}
}

// funcWebView 用函数充当 uiWebView（浏览器事件桥的回推通道）。
type funcWebView struct {
	fn func(js string)
}

func (f *funcWebView) Eval(js string) { f.fn(js) }
