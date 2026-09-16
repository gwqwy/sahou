// v4 响应式计算模型：格子（数据流）。
// 程序 = 一张依赖图：格子由表达式自动维护，依赖变化时自动重算——
// 这与"顺序执行 + 手动更新"的命令式模型是两种计算范式。
package interp

import (
	"fmt"

	"sahou/internal/errs"
	"sahou/internal/lexer"
	"sahou/internal/parser"
)

// cellValue 读格子的当前值（不存在则返回 ok=false）。
func (in *Interp) cellValue(name string) (Value, bool) {
	if _, exists := in.cells[name]; !exists {
		return nil, false
	}
	return in.Globals.Get(name)
}

// cellDef 一个响应式格子：公式 + 缓存值 + 依赖集。
type cellDef struct {
	name      string
	expr      parser.Expr
	deps      map[string]bool
	computed  bool
	computing bool // 正在计算（环检测）
}

// defineCell 处理 `格子 名字 = 表达式`（仅顶层）。
// 惰性求值：这里只记录公式，第一次被读取时才计算（电子表格语义——
// 允许"先写格子、后给依赖赋值"的自然顺序）。
func (in *Interp) defineCell(name string, expr parser.Expr, env *Env, line int) {
	if env != in.Globals {
		panic(errorSig{errs.RuntimeHint(
			"格子要写在程序顶层，函数里面不能定义格子。",
			"cells can only be defined at the top level, not inside functions.",
			"把格子移到程序最外层；函数里可以直接读它的值。", line)})
	}
	if IsBuiltinName(name) {
		panic(errorSig{errs.RuntimeHint(
			name+" 是内置函数的名字，不能当格子。", name+" is a builtin name.",
			"请换一个格子名。", line)})
	}
	in.cells[name] = &cellDef{name: name, expr: expr}
}

// ensureCellComputed 保证格子的缓存值是最新的（惰性求值 + 环检测）。
func (in *Interp) ensureCellComputed(name string, line int) {
	c := in.cells[name]
	if c == nil || c.computed {
		return
	}
	if c.computing {
		panic(errorSig{errs.RuntimeHint(
			fmt.Sprintf("格子 %s 的计算形成了环。", name),
			fmt.Sprintf("cell %s is part of a dependency cycle.", name),
			"格子 A 依赖 B、B 又依赖 A 是不允许的；检查格子的依赖方向。", line)})
	}
	c.computing = true
	deps := map[string]bool{}
	saved := in.tracking
	in.tracking = &deps
	var v Value
	var cellErr *errs.Error
	func() {
		defer func() {
			in.tracking = saved
			if r := recover(); r != nil {
				if sig, ok := r.(errorSig); ok {
					cellErr = errs.RuntimeHint(
						fmt.Sprintf("格子 %s 的表达式出错了：%s", name, sig.e.Zh),
						fmt.Sprintf("cell %s failed: %s", name, sig.e.En),
						"检查格子表达式依赖的变量。", line)
					return
				}
				panic(r)
			}
		}()
		v = in.eval(c.expr, in.Globals)
	}()
	c.computing = false
	if cellErr != nil {
		panic(errorSig{cellErr})
	}
	c.deps = deps
	c.computed = true
	in.Globals.Set(name, v)
}

// onChange 顶层某个名字变化后：让依赖它的格子失效，全部重算，并通知观察者。
func (in *Interp) onChange(name string, line int) {
	invalidated := map[string]bool{}
	var invalidate func(string)
	invalidate = func(n string) {
		for _, c := range in.cells {
			if c.deps[n] && !invalidated[c.name] {
				invalidated[c.name] = true
				c.computed = false
				invalidate(c.name)
			}
		}
	}
	invalidate(name)

	// 尚未计算成功的格子（deps 未建立，如"先写格子后赋值"）也一并重算
	for _, c := range in.cells {
		if !c.computed {
			in.ensureCellComputed(c.name, line)
		}
	}

	// 触发观察者：名字本身 + 受影响格子
	fired := map[string]bool{}
	var fire func(string)
	fire = func(n string) {
		if fired[n] {
			return
		}
		fired[n] = true
		for _, c := range in.cells {
			if c.deps[n] {
				fire(c.name)
			}
		}
		if _, isCell := in.cells[n]; isCell {
			in.ensureCellComputed(n, line)
		}
		for _, cb := range in.watchers[n] {
			if v, ok := in.Globals.Get(n); ok {
				cb(v)
			}
		}
	}
	fire(name)
}

// fireWatchers 触发某个名字上的所有观察者。
func (in *Interp) fireWatchers(name string) {
	for _, cb := range in.watchers[name] {
		if v, ok := in.Globals.Get(name); ok {
			cb(v)
		}
	}
}

// Watch 订阅顶层变量/格子的变化（宿主绑定用）。
func (in *Interp) Watch(name string, cb func(Value)) {
	in.watchers[name] = append(in.watchers[name], cb)
}

// SetTopVar 从宿主设置顶层变量并触发响应式重算。
func (in *Interp) SetTopVar(name string, v Value) {
	if _, isCell := in.cells[name]; isCell {
		return // 格子由表达式维护
	}
	in.Globals.Set(name, v)
	in.onChange(name, 0)
}

// TopValue 读顶层变量/格子的当前值。
func (in *Interp) TopValue(name string) (Value, bool) {
	return in.Globals.Get(name)
}

// RunSource 从源码文本直接运行（sahou.wasm 浏览器宿主用）。
func (in *Interp) RunSource(src string) *errs.Error {
	toks, e := lexer.Tokenize(src)
	if e != nil {
		return e
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		return e
	}
	return in.Run(prog)
}
