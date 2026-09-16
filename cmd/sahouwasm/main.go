//go:build js && wasm

// sahou.wasm —— 在浏览器里直接解释执行 sahou 代码（不转译、不生成 JavaScript 源码）。
// 页面通过加载器提供 __SAHO_SOURCE（源码文本）或 __SAHO_PAGE（.saho 地址）。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"syscall/js"

	"sahou/internal/errs"
	"sahou/internal/interp"
)

func main() {
	wait := make(chan struct{}, 0)
	registerPlayground()

	if src := readSource(); src != "" {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("sahou.wasm 内部错误：", r)
				}
			}()
			in := interp.New()
			if e := in.RunSource(src); e != nil {
				fmt.Println(errs.Format(e))
			}
		}()
	}

	<-wait // 保持解释器常驻：事件与响应式更新持续工作
}

// registerPlayground 注册 __sahou_run(源码)（别名 __卅_run）：新建独立解释器运行，
// 捕获全部打印，返回 JSON {"output": ..., "error": ...}（在线体验用）。
func registerPlayground() {
	run := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		src := ""
		if len(args) > 0 {
			src = args[0].String()
		}
		in := interp.New()
		var buf bytes.Buffer
		in.Stdout = &buf
		result := map[string]interface{}{}
		if e := in.RunSource(src); e != nil {
			result["error"] = errs.Format(e)
		}
		result["output"] = buf.String()
		data, _ := json.Marshal(result)
		return js.ValueOf(string(data))
	})
	js.Global().Set("__sahou_run", run)
	js.Global().Set("__卅_run", run) // 语言名别名，页面两种写法都能用
}

// readSource 优先读全局注入的源码文本，否则 fetch __SAHO_PAGE 指向的 .saho 文件。
func readSource() string {
	g := js.Global()
	if v := g.Get("__SAHO_SOURCE"); !v.IsUndefined() && v.String() != "" {
		return v.String()
	}
	if v := g.Get("__SAHO_PAGE"); !v.IsUndefined() && v.String() != "" {
		text, err := fetchText(v.String())
		if err != nil {
			fmt.Println("sahou.wasm：读不到页面源码：", err)
			return ""
		}
		return text
	}
	return ""
}

func fetchText(url string) (string, error) {
	done := make(chan struct{})
	var text string
	var ferr error
	p := js.Global().Call("fetch", url)
	p.Call("then", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		args[0].Call("text").Call("then", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			text = args[0].String()
			close(done)
			return nil
		}))
		return nil
	}))
	p.Call("catch", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		ferr = fmt.Errorf("fetch 失败")
		close(done)
		return nil
	}))
	<-done
	return text, ferr
}
