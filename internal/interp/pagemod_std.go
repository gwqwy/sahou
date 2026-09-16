//go:build !js || !wasm

// 页面/page 模块 —— 服务端（非浏览器）宿主版本：调用即给出双语提示。
package interp

import "sahou/internal/errs"

func registerPageModule(in *Interp) {
	members := []struct{ zh, en string }{
		{"取元素", "el"}, {"取全部", "el_all"}, {"置文本", "set_text"}, {"取文本", "get_text"},
		{"值", "value"}, {"置值", "set_value"}, {"置样式", "set_style"}, {"置属性", "set_attr"},
		{"点击", "on_click"}, {"输入", "on_input"}, {"提交", "on_submit"},
		{"创建", "create"}, {"加入", "append"}, {"移除", "remove"},
		{"绑输入", "bind_input"}, {"绑定", "bind"},
	}
	page := NewDict()
	for _, mem := range members {
		fn := &Builtin{Zh: "页面." + mem.zh, En: "page." + mem.en}
		zh := "页面." + mem.zh
		fn.Call = func(in *Interp, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
			return nil, errs.RuntimeHint(
				zh+" 只能在浏览器里用（sahou.wasm 直接解释执行）；服务端程序请用 网络 模块。",
				zh+" only works in the browser via sahou.wasm; use the net module on the server.",
				"把 .saho 交给 sahou.wasm 加载即可，无需转译。", line)
		}
		page.SetNew("s:"+mem.zh, fn)
		page.SetNew("s:"+mem.en, fn)
	}
	in.builtinMods["页面"] = page
	in.builtinMods["page"] = page
	in.Globals.Set("页面", page)
	in.Globals.Set("page", page)
}
