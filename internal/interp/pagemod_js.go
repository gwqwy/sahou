//go:build js && wasm

// 页面/page 模块 —— 浏览器宿主版本（sahou.wasm 直接解释执行 .saho）。
// 通过 syscall/js 操作 DOM；绑定类成员把元素接进 v4 响应式格子。
package interp

import (
	"fmt"
	"strconv"
	"strings"
	"syscall/js"

	"sahou/internal/errs"
)

// pageEl 浏览器元素句柄（不透明，语言里不可直接读写）。
type pageEl struct{ v js.Value }

func (p *pageEl) isPageEl() {}

func jsDoc() js.Value { return js.Global().Get("document") }

func registerPageModule(in *Interp) {
	page := NewDict()
	def := func(zh, en string, npos int, body func(in *Interp, pos []Value, line int) (Value, *errs.Error)) {
		fn := &Builtin{Zh: "页面." + zh, En: "page." + en}
		fn.Call = func(in *Interp, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
			if len(args) < npos {
				return nil, errs.RuntimeHint(
					"页面."+zh+" 缺少参数。", "page."+en+" is missing arguments.", "", line)
			}
			return body(in, args, line)
		}
		page.SetNew("s:"+zh, fn)
		page.SetNew("s:"+en, fn)
	}

	elArg := func(args []Value, i int) *pageEl {
		if h, ok := args[i].(*pageEl); ok {
			return h
		}
		return nil
	}

	def("取元素", "el", 1, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		sel, ok := pos[0].(string)
		if !ok {
			return nil, errs.RuntimeHint("选择器要是一段文本。", "selector must be a string.", "", line)
		}
		el := jsDoc().Call("querySelector", sel)
		if el.IsNull() || el.IsUndefined() {
			return nil, nil
		}
		return &pageEl{v: el}, nil
	})
	def("取全部", "el_all", 1, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		sel, _ := pos[0].(string)
		nodes := jsDoc().Call("querySelectorAll", sel)
		out := &List{}
		n := nodes.Get("length").Int()
		for i := 0; i < n; i++ {
			out.Items = append(out.Items, &pageEl{v: nodes.Index(i)})
		}
		return out, nil
	})
	def("置文本", "set_text", 2, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		if el == nil {
			return nil, elementErr(line)
		}
		el.v.Set("textContent", Str(pos[1]))
		return nil, nil
	})
	def("取文本", "get_text", 1, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		if el == nil {
			return nil, elementErr(line)
		}
		return el.v.Get("textContent").String(), nil
	})
	def("值", "value", 1, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		if el == nil {
			return nil, elementErr(line)
		}
		return el.v.Get("value").String(), nil
	})
	def("置值", "set_value", 2, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		if el == nil {
			return nil, elementErr(line)
		}
		el.v.Set("value", Str(pos[1]))
		return nil, nil
	})
	def("置样式", "set_style", 3, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		if el == nil {
			return nil, elementErr(line)
		}
		name, _ := pos[1].(string)
		el.v.Get("style").Set(name, Str(pos[2]))
		return nil, nil
	})
	def("置属性", "set_attr", 3, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		if el == nil {
			return nil, elementErr(line)
		}
		el.v.Call("setAttribute", Str(pos[1]), Str(pos[2]))
		return nil, nil
	})
	def("创建", "create", 1, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		tag, _ := pos[0].(string)
		return &pageEl{v: jsDoc().Call("createElement", tag)}, nil
	})
	def("加入", "append", 2, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		parent, child := elArg(pos, 0), elArg(pos, 1)
		if parent == nil || child == nil {
			return nil, elementErr(line)
		}
		parent.v.Call("appendChild", child.v)
		return nil, nil
	})
	def("移除", "remove", 1, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		if el == nil {
			return nil, elementErr(line)
		}
		el.v.Get("parentNode").Call("removeChild", el.v)
		return nil, nil
	})

	// 事件成员：点击/输入/提交 —— 事件处理就是传函数值（D5）
	eventDef := func(zh, en, ev string) {
		def(zh, en, 2, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
			el := elArg(pos, 0)
			if el == nil {
				return nil, elementErr(line)
			}
			handler := pos[1]
			cb := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				ev := NewDict()
				ev.SetNew("s:类型", zh)
				ev.SetNew("s:目标", &pageEl{v: this.Get("target")})
				if len(args) > 0 {
					ev.SetNew("s:目标", &pageEl{v: args[0].Get("target")})
				}
				_, _ = in.CallHandler(handler, ev)
				return nil
			})
			el.v.Call("addEventListener", ev, cb)
			return nil, nil
		})
	}
	eventDef("点击", "on_click", "click")
	eventDef("输入", "on_input", "input")
	eventDef("提交", "on_submit", "submit")

	// v4 响应式绑定：输入框直接驱动变量，格子变化自动刷新元素
	def("绑输入", "bind_input", 2, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		name, ok := pos[1].(string)
		if el == nil || !ok {
			return nil, errs.RuntimeHint(
				"绑输入 要一个输入框元素和一个变量名。", "bind_input needs an input element and a variable name.",
				"例如：页面.绑输入(数量输入, \"数量\")。", line)
		}
		apply := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			defer func() {
				if r := recover(); r != nil {
					if sig, ok := r.(errorSig); ok {
						fmt.Println("sahou：", sig.e.Zh)
						return
					}
					panic(r)
				}
			}()
			text := el.v.Get("value").String()
			in.SetVarFromText(name, text)
			return nil
		})
		el.v.Call("addEventListener", "input", apply)
		apply.Invoke(js.ValueOf(map[string]interface{}{}))
		return nil, nil
	})
	def("绑定", "bind", 2, func(in *Interp, pos []Value, line int) (Value, *errs.Error) {
		el := elArg(pos, 0)
		name, ok := pos[1].(string)
		if el == nil || !ok {
			return nil, errs.RuntimeHint(
				"绑定 要一个元素和一个变量/格子名。", "bind needs an element and a variable/cell name.",
				"例如：页面.绑定(总价显示, \"总价\")。", line)
		}
		in.Watch(name, func(v Value) {
			el.v.Set("textContent", Str(v))
		})
		if v, ok := in.TopValue(name); ok {
			el.v.Set("textContent", Str(v))
		}
		return nil, nil
	})

	in.builtinMods["页面"] = page
	in.builtinMods["page"] = page
	in.Globals.Set("页面", page)
	in.Globals.Set("page", page)
}

func elementErr(line int) *errs.Error {
	return errs.RuntimeHint(
		"元素不能这样用。它只能交给 页面 模块的函数。",
		"an element cannot be used like this; pass it to page functions.",
		"取元素 找不到时会返回 空值，检查选择器。", line)
}

// SetVarFromText 供 绑输入 使用：变量当前是数时，仅接受合法数字文本
// （半截输入如 "" / "3." 不写入，电子表格行为）；否则写入文本。
func (in *Interp) SetVarFromText(name, text string) {
	if cur, ok := in.Globals.Get(name); ok {
		if _, isNum := cur.(*numRat); isNum {
			if parsed, perr := strconv.ParseFloat(strings.TrimSpace(text), 64); perr == nil {
				in.SetTopVar(name, floatToRat(parsed))
			}
			return
		}
	}
	in.SetTopVar(name, text)
}
