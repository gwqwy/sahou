package interp

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"sahou/internal/encodesrc"

	"sahou/internal/errs"
	"sahou/internal/lexer"
	"sahou/internal/parser"
	"sahou/internal/stonesrc"
)

// 控制流信号（用 panic/recover 实现 返回 与 可接住错误 的非局部跳转）。
type returnSig struct{ v Value }

type breakSig struct{}    // 跳出：结束最近一层循环（v4.1）
type continueSig struct{} // 继续：跳过本轮循环剩余部分（v4.1）

type errorSig struct{ e *errs.Error }

// Interp 一个解释器实例。REPL 场景下可反复 Run 而共享 Globals。
type Interp struct {
	Globals   *Env
	ScriptDir string // 读取文件 等相对路径的基准（被运行脚本所在目录）
	Stdout    io.Writer
	Stderr    io.Writer
	Input     func(prompt string) string
	// LastValue 记录顶层表达式语句的值，REPL 用它回显。
	LastValue    Value
	HasLastValue bool

	depth    int
	maxDepth int
	chain    []errs.Frame
	warned   map[string]bool

	// v2 模块系统：按绝对路径缓存；loading 为加载栈（循环引入检测）；dirs 为当前文件目录栈
	modules map[string]*Dict
	loading []string
	dirs    []string
	modBase *Env
	// 内置标准库模块：zh/en 名 → 模块字典（网络/页面/随机/时间/数学/编码/系统）
	builtinMods map[string]*Dict
	ExitCode    int      // 系统.退出 设置的退出码；-1 表示未退出
	ProgramArgs []string // 系统.参数()：脚本名之后的命令行参数

	// v4 响应式格子：依赖图 + 变更传播
	cells    map[string]*cellDef
	watchers map[string][]func(Value)
	tracking *map[string]bool // 非空时，标识符求值会记录读取过的名字

	// mu 串行化所有求值：HTTP goroutine 处理请求时抢锁（语言层面仍是单线程语义）
	mu sync.Mutex
}

func New() *Interp {
	in := &Interp{
		Globals:   NewEnv(nil),
		ScriptDir: ".",
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Input: func(prompt string) string {
			fmt.Print(prompt)
			var line string
			_, _ = fmt.Scanln(&line) // 退化路径；CLI 会注入 bufio 版本
			return line
		},
		maxDepth:    200,
		warned:      map[string]bool{},
		modules:     map[string]*Dict{},
		builtinMods: map[string]*Dict{},
		ExitCode:    -1,
		cells:       map[string]*cellDef{},
		watchers:    map[string][]func(Value){},
	}
	for name, b := range builtins {
		in.Globals.Set(name, b)
	}
	registerNetModule(in)
	registerStdModules(in)
	return in
}

// Run 执行整个程序；返回未接住的运行时错误（语法错误在解析阶段就已产生）。
// 顶层 返回 会正常结束程序（规范 5.8）。
func (in *Interp) Run(prog *parser.Program) *errs.Error {
	in.HasLastValue = false
	if len(in.dirs) == 0 {
		in.dirs = []string{in.ScriptDir}
	}
	err := in.runBody(prog.Stmts, in.Globals)
	if err != nil {
		if err.Exit >= 0 {
			in.ExitCode = err.Exit
			return nil
		}
		return err
	}
	return nil
}

// runLoopBody 执行一轮循环体；捕获 跳出/继续 信号。
// 返回 true 表示要结束整个循环（跳出）。
func (in *Interp) runLoopBody(run func()) (done bool) {
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case breakSig:
				done = true
			case continueSig:
				done = false
			default:
				panic(r)
			}
		}
	}()
	run()
	return false
}

// runBody 执行一组语句；捕获 顶层/函数边界 的控制流信号。
func (in *Interp) runBody(stmts []parser.Stmt, env *Env) (uncaught *errs.Error) {
	defer func() {
		if r := recover(); r != nil {
			switch sig := r.(type) {
			case errorSig:
				uncaught = sig.e
			case returnSig:
				uncaught = nil // 顶层 返回：结束程序（值仅在 REPL 显示）
			case breakSig, continueSig:
				uncaught = errs.RuntimeHint(
					"跳出/继续 只能在 当 或 遍历 循环里使用。",
					"break/continue can only be used inside a while or for loop.",
					"检查这段代码是不是落在循环外面了。", 0)
			default:
				panic(r)
			}
		}
	}()
	in.execBlock(stmts, env)
	return nil
}

func (in *Interp) execBlock(stmts []parser.Stmt, env *Env) {
	for _, s := range stmts {
		in.execStmt(s, env)
	}
}

// ---------- 语句 ----------

func (in *Interp) execStmt(s parser.Stmt, env *Env) {
	switch st := s.(type) {
	case *parser.LetStmt:
		v := in.eval(st.Value, env)
		in.assignTo(st.Target, v, env, st.Line)

	case *parser.ExprStmt:
		v := in.eval(st.X, env)
		in.LastValue = v
		in.HasLastValue = true

	case *parser.IfStmt:
		for i, cond := range st.Conds {
			if Truthy(in.eval(cond, env)) {
				in.execBlock(st.Blocks[i], env)
				return
			}
		}
		if st.Else != nil {
			in.execBlock(st.Else, env)
		}

	case *parser.WhileStmt:
		for Truthy(in.eval(st.Cond, env)) {
			if done := in.runLoopBody(func() { in.execBlock(st.Body, env) }); done {
				break
			}
		}

	case *parser.ForStmt:
		iter := in.eval(st.Iter, env)
		switch col := iter.(type) {
		case *List:
			for _, item := range col.Items {
				env.Set(st.Var, item)
				if done := in.runLoopBody(func() { in.execBlock(st.Body, env) }); done {
					break
				}
			}
		case string:
			for _, r := range col {
				env.Set(st.Var, string(r))
				if done := in.runLoopBody(func() { in.execBlock(st.Body, env) }); done {
					break
				}
			}
		case *Dict:
			for _, kv := range col.KeysAsValues() {
				env.Set(st.Var, kv)
				if done := in.runLoopBody(func() { in.execBlock(st.Body, env) }); done {
					break
				}
			}
		case nil:
			panic(errorSig{errs.RuntimeHint(
				"空值 不是可以遍历的值。", "null is not iterable.",
				"能遍历的有：列表、文本（按字符）、字典（按键）。", st.Line)})
		default:
			panic(errorSig{errs.RuntimeHint(
				TypeName(iter)+" 不是可以遍历的值。", TypeName(iter)+" is not iterable.",
				"能遍历的有：列表、文本（按字符）、字典（按键）。", st.Line)})
		}

	case *parser.FnStmt:
		if IsBuiltinName(st.Name) {
			panic(errorSig{errs.RuntimeHint(
				st.Name+" 是内置函数，不能重新定义。", st.Name+" is a builtin function and cannot be redefined.",
				"请换一个名字，比如 "+st.Name+"2。", st.Line)})
		}
		env.Set(st.Name, &SahouFn{Name: st.Name, Params: st.Params, Body: st.Body, Env: env})

	case *parser.ReturnStmt:
		var v Value
		if st.Value != nil {
			v = in.eval(st.Value, env)
		}
		panic(returnSig{v})

	case *parser.TryStmt:
		in.execTry(st, env)

	case *parser.BreakStmt:
		panic(breakSig{})

	case *parser.ContinueStmt:
		panic(continueSig{})

	case *parser.CellStmt:
		in.defineCell(st.Name, st.Expr, env, st.Line)

	case *parser.ImportStmt:
		mod, zhName, enName := in.loadModule(st.Name, st.Line)
		env.Set(zhName, mod)
		if enName != "" && enName != zhName {
			env.Set(enName, mod)
		}
	}
}

func (in *Interp) execTry(st *parser.TryStmt, env *Env) {
	sig := func() (sig *errorSig) {
		defer func() {
			if r := recover(); r != nil {
				switch e := r.(type) {
				case errorSig:
					sig = &e
				case returnSig:
					panic(r) // 返回 正常穿过 尝试（规范 5.9）
				default:
					panic(r)
				}
			}
		}()
		in.execBlock(st.Body, env)
		return nil
	}()
	if sig == nil {
		return
	}
	env.Set(st.CatchVar, sig.e.Zh) // 错误对象就是一句话（D8）
	in.execBlock(st.Catch, env)
}

// loadModule 解析并加载模块（08 文档 M2–M4）：内置 → 同目录 → stones/ → 内嵌 stones。
// 返回模块字典与应绑定的中英文名。
func (in *Interp) loadModule(name string, line int) (*Dict, string, string) {
	// 内置标准库模块（双语名都指向同一模块）
	if d, ok := in.builtinMods[name]; ok {
		zh, en := moduleNamePair(name)
		return d, zh, en
	}
	// 解析文件位置
	base := in.ScriptDir
	if n := len(in.dirs); n > 0 {
		base = in.dirs[n-1]
	}
	var path string
	for _, dir := range dirChain(base) {
		candidates := []string{
			filepath.Join(dir, name+".saho"),
			filepath.Join(dir, name, "main.saho"),
			filepath.Join(dir, "stones", name+".saho"),
			filepath.Join(dir, "stones", name, "main.saho"),
		}
		for _, c := range candidates {
			if st2, err := os.Stat(c); err == nil && !st2.IsDir() {
				path = c
				break
			}
		}
		if path != "" {
			break
		}
	}
	// 内嵌 stones 兜底（发布版 exe 单文件自带全部标准库包；磁盘 stones/ 优先，可本地覆盖）
	if path == "" {
		if src, ok := stonesrc.Source(name); ok {
			return in.loadModuleFrom(name, src, "内嵌:"+name, "", line)
		}
	}
	if path == "" {
		searched := strings.Join([]string{name + ".saho", name + "/main.saho", "stones/" + name + ".saho", "stones/" + name + "/main.saho"}, "、")
		hint := "搜索过的位置（相对 " + base + "）：" + searched + "。"
		if stonesrc.Has(name) {
			hint += "这个包是 exe 自带的，先运行 sahou 装 " + name + " 放进 stones/ 目录。"
		} else if _, err := os.Stat(filepath.Join(base, "stones.yml")); err == nil {
			hint += " stones.yml 里如果登记了这个包，先运行 sahou 装 补齐。"
		} else {
			hint += "第三方包用 sahou 装 <本地路径> 放进 stones/ 目录。运行 sahou stones 可看 exe 自带的包。"
		}
		panic(errorSig{errs.RuntimeHint(
			"找不到模块 \""+name+"\"。", "cannot find module \""+name+"\".", hint, line)})
	}
	abs, _ := filepath.Abs(path)
	src, err := encodesrc.ReadFile(abs)
	if err != nil {
		panic(errorSig{errs.RuntimeHint(
			"读不到模块文件 \""+abs+"\"。", "cannot read module file \""+abs+"\".", "", line)})
	}
	return in.loadModuleFrom(name, string(src), abs, filepath.Dir(abs), line)
}

// loadModuleFrom 从源码文本加载并执行一个模块（磁盘文件与内嵌包共用）。
// cacheKey 是缓存与循环检测的键；pushDir 非空时压入目录栈（内嵌包不压）。
func (in *Interp) loadModuleFrom(name, src, cacheKey, pushDir string, line int) (*Dict, string, string) {
	// 循环引入检测
	for _, p := range in.loading {
		if p == cacheKey {
			panic(errorSig{errs.RuntimeHint(
				"模块 \""+name+"\" 循环引入了自己。", "module \""+name+"\" is imported in a cycle.",
				"模块 A 引入 B、B 又引入 A 是不允许的；把共享的部分抽到第三个模块。", line)})
		}
	}
	if cached, ok := in.modules[cacheKey]; ok {
		return cached, name, ""
	}
	toks, e := lexer.Tokenize(src)
	if e != nil {
		e.Line += line - 1
		panic(errorSig{e})
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		panic(errorSig{e})
	}
	// 模块作用域：能看到全部内置（moduleBase），看不到引入者的变量
	modEnv := NewEnv(in.moduleBase())
	mod := NewDict()
	in.modules[cacheKey] = mod // 先入缓存，模块内自引用可见
	in.loading = append(in.loading, cacheKey)
	if pushDir != "" {
		in.dirs = append(in.dirs, pushDir)
	}
	in.depth++
	uncaught := func() (u *errs.Error) {
		defer func() {
			if r := recover(); r != nil {
				switch sig := r.(type) {
				case returnSig:
					u = errs.RuntimeHint(
						"模块里不能写顶层的 返回（模块不是程序）。", "a module cannot have a top-level return.",
						"要给模块一个入口，请在引入它的程序里调用模块的函数。", line)
				case errorSig:
					u = sig.e
				default:
					panic(r)
				}
			}
		}()
		in.execBlock(prog.Stmts, modEnv)
		return nil
	}()
	in.depth--
	if pushDir != "" {
		in.dirs = in.dirs[:len(in.dirs)-1]
	}
	in.loading = in.loading[:len(in.loading)-1]
	if uncaught != nil {
		delete(in.modules, cacheKey)
		panic(errorSig{uncaught})
	}
	// 导出 = 模块全部顶层名字
	for _, n := range modEnv.Names() {
		if v, ok := modEnv.Get(n); ok {
			mod.SetNew("s:"+n, v)
		}
	}
	return mod, name, ""
}

// moduleBase 只含内置函数与预置模块的基环境（模块看不到主程序的变量）。
func (in *Interp) moduleBase() *Env {
	if in.modBase != nil {
		return in.modBase
	}
	base := NewEnv(nil)
	for name, b := range builtins {
		base.Set(name, b)
	}
	for name, m := range in.builtinMods {
		base.Set(name, m)
	}
	in.modBase = base
	return base
}

// assignTo 处理赋值目标：变量、列表元素、字典字段（规范 5.7）。
func (in *Interp) assignTo(target parser.Expr, v Value, env *Env, line int) {
	switch t := target.(type) {
	case *parser.Ident:
		if IsBuiltinName(t.Name) {
			panic(errorSig{errs.RuntimeHint(
				t.Name+" 是内置函数的名字，不能改作他用。", t.Name+" is a builtin name and cannot be reassigned.",
				"请换一个变量名。", line)})
		}
		if env == in.Globals {
			if _, isCell := in.cells[t.Name]; isCell {
				panic(errorSig{errs.RuntimeHint(
					t.Name+" 是格子，由表达式自动计算，不能直接赋值。",
					t.Name+" is a cell computed automatically; you cannot assign to it.",
					"改它依赖的变量，格子的值会自动更新。", line)})
			}
		}
		env.Set(t.Name, v)
		if env == in.Globals {
			in.onChange(t.Name, line)
		}

	case *parser.Index:
		container := in.eval(t.X, env)
		idx := in.eval(t.Idx, env)
		switch col := container.(type) {
		case *List:
			i, e := IntOf(idx, line)
			if e != nil {
				panic(errorSig{e})
			}
			if i < 0 {
				panic(errorSig{errs.RuntimeHint(
					"列表下标不能是负数。", "list index cannot be negative.",
					"想取最后一个元素，写 名单[长度(名单) - 1]。", line)})
			}
			if i >= len(col.Items) {
				panic(errorSig{errs.RuntimeHint(
					fmt.Sprintf("列表只有 %d 个元素，第 %d 个位置放不进去（下标从 0 开始，合法范围是 0 到 %d）。",
						len(col.Items), i, len(col.Items)-1),
					fmt.Sprintf("the list has %d elements; index %d is out of range (valid: 0 to %d).",
						len(col.Items), i, len(col.Items)-1),
					"想在末尾加一个元素，写 名单.添加(值)。", line)})
			}
			col.Items[i] = v
		case *Dict:
			enc, ok := KeyEncode(idx)
			if !ok {
				panic(errorSig{errs.RuntimeHint(
					"字典的键只能是文本或数。", "dict keys must be text or a number.",
					"用文本或数当键，例如 动物[\"名字\"] = 值。", line)})
			}
			col.SetNew(enc, v)
		case string:
			panic(errorSig{errs.RuntimeHint(
				"文本不能按下标修改。", "strings cannot be modified by index.",
				"文本是不可变的；用 替换、分割、连接 组合出新文本。", line)})
		default:
			panic(errorSig{errs.RuntimeHint(
				TypeName(container)+" 不能按下标赋值。", TypeName(container)+" does not support index assignment.",
				"只有列表和字典可以按下标赋值。", line)})
		}

	case *parser.Member:
		container := in.eval(t.X, env)
		if d, ok := container.(*Dict); ok {
			d.SetNew("s:"+t.Name, v)
			return
		}
		panic(errorSig{errs.RuntimeHint(
			TypeName(container)+" 没有可以赋值的字段。", TypeName(container)+" has no settable fields.",
			"只有字典支持 字典.字段 = 值。", line)})
	}
}

// ---------- 表达式 ----------

func (in *Interp) eval(x parser.Expr, env *Env) Value {
	switch e := x.(type) {
	case *parser.NumLit:
		return e.Val
	case *parser.BoolLit:
		return e.Val
	case *parser.NullLit:
		return nil
	case *parser.StrLit:
		var sb strings.Builder
		for _, p := range e.Parts {
			if p.Expr != nil {
				sb.WriteString(Str(in.eval(p.Expr, env)))
			} else {
				sb.WriteString(p.Lit)
			}
		}
		return sb.String()
	case *parser.Ident:
		if in.tracking != nil && len(in.cells) > 0 {
			if _, isCell := in.cells[e.Name]; isCell {
				(*in.tracking)[e.Name] = true
			} else if _, isGlobal := in.Globals.vars[e.Name]; isGlobal {
				(*in.tracking)[e.Name] = true
			}
		}
		if _, isCell := in.cells[e.Name]; isCell {
			in.ensureCellComputed(e.Name, e.Line)
		}
		if v, ok := env.Get(e.Name); ok {
			return v
		}
		sug := errs.DidYouMean(e.Name, append(env.Names(), builtinNames...))
		if sug != "" {
			panic(errorSig{errs.RuntimeHint(
				"变量 \""+e.Name+"\" 还没有定义。你是不是想写 \""+sug+"\"？",
				"name \""+e.Name+"\" is not defined. Did you mean \""+sug+"\"?",
				"检查拼写；名字区分英文大小写。", e.Line)})
		}
		panic(errorSig{errs.RuntimeHint(
			"变量 \""+e.Name+"\" 还没有定义。", "name \""+e.Name+"\" is not defined.",
			"先给它赋一个值再使用；检查拼写，名字区分英文大小写。", e.Line)})

	case *parser.ListLit:
		items := make([]Value, len(e.Elems))
		for i, el := range e.Elems {
			items[i] = in.eval(el, env)
		}
		return &List{Items: items}

	case *parser.DictLit:
		d := NewDict()
		for _, it := range e.Items {
			k := in.eval(it.Key, env)
			enc, ok := KeyEncode(k)
			if !ok {
				panic(errorSig{errs.RuntimeHint(
					"字典的键只能是文本或数。", "dict keys must be text or a number.",
					"例如：{\"名字\": \"咪咪\"} 或 {1: \"一\"}。", e.Line)})
			}
			if d.Has(enc) && !in.warned["dupkey:"+enc] {
				in.warned["dupkey:"+enc] = true
				fmt.Fprintf(in.Stderr, "提示 第 %d 行：字典里键 %s 重复了，后面的会覆盖前面的。\n", e.Line, Str(k))
			}
			d.SetNew(enc, in.eval(it.Val, env))
		}
		return d

	case *parser.Un:
		v := in.eval(e.X, env)
		r, ok := v.(*big.Rat)
		if !ok {
			panic(errorSig{errs.RuntimeHint(
				"- 号后面要跟一个数，这里是"+TypeName(v)+"。", "unary - needs a number, got "+TypeName(v)+".",
				"只有数可以取负。", e.Line)})
		}
		return new(big.Rat).Neg(r)

	case *parser.Not:
		return !Truthy(in.eval(e.X, env))

	case *parser.Bin:
		return in.evalBin(e, env)

	case *parser.AnonFn:
		return &SahouFn{Name: "", Params: e.Params, Body: []parser.Stmt{
			&parser.ReturnStmt{Value: e.Body, Line: e.Line},
		}, Env: env}

	case *parser.Index:
		container := in.eval(e.X, env)
		idx := in.eval(e.Idx, env)
		return in.indexValue(container, idx, e.Line)

	case *parser.Member:
		return in.memberValue(in.eval(e.X, env), e.Name, e.Line)

	case *parser.Call:
		return in.evalCall(e, env)

	case *parser.Pipe:
		return in.evalPipe(e, env)
	}
	panic(errorSig{errs.Runtime("看不懂的表达式。", "unknown expression.", 0)})
}

// evalPipe 数据流水线（v3）：值沿步骤从左到右流动。
// 步骤形态：裸函数/成员（自动以流经值为实参调用）、
// 不含 它 的调用（流经值插入为第一个实参）、
// 含 它 的表达式（它 绑定为流经的值）。
func (in *Interp) evalPipe(e *parser.Pipe, env *Env) Value {
	v := in.eval(e.Base, env)
	for _, step := range e.Steps {
		switch st := step.(type) {
		case *parser.Ident:
			v = in.callStepFn(in.eval(step, env), v, e.Line, st.Name)
		case *parser.Member:
			v = in.callStepFn(in.eval(step, env), v, e.Line, st.Name)
		default:
			if pipeUsesIt(step) {
				stepEnv := NewEnv(env)
				stepEnv.Set("它", v)
				v = in.eval(step, stepEnv)
			} else if call, ok := step.(*parser.Call); ok {
				v = in.evalCallPrepend(call, v, env)
			} else {
				fv := in.eval(step, env)
				switch fv.(type) {
				case *Builtin, *SahouFn, *BoundMember:
					v = in.callStepFn(fv, v, e.Line, "管道步骤")
				default:
					panic(errorSig{errs.RuntimeHint(
						"管道的这一步不是一个加工步骤（收到的是"+TypeName(fv)+"）。",
						"this pipeline step is not a function (got "+TypeEnName(fv)+").",
						"步骤写成函数：-> 排序、-> 取(0, 2)、-> 函数(x) => x * 2，或用 它 指代流经的值。", e.Line)})
				}
			}
		}
	}
	return v
}

// callStepFn 用流经的值调用步骤函数（单实参）。
func (in *Interp) callStepFn(fn Value, v Value, line int, stepName string) Value {
	switch f := fn.(type) {
	case *Builtin:
		out, e := f.Call(in, []Value{v}, nil, line)
		if e != nil {
			panic(errorSig{e})
		}
		return out
	case *SahouFn:
		if len(f.Params) != 1 {
			panic(errorSig{errs.RuntimeHint(
				fmt.Sprintf("管道步骤的函数 %s 要恰好 1 个参数，它有 %d 个。", fnName(f), len(f.Params)),
				fmt.Sprintf("pipeline step function %s must take exactly 1 argument.", fnName(f)),
				"把多余的参数写成命名实参或用 它。", line)})
		}
		return in.callSahou(f, []Value{v}, nil, nil, line)
	case *BoundMember:
		out, e := f.Call(in, f.Recv, []Value{v}, nil, line)
		if e != nil {
			panic(errorSig{e})
		}
		return out
	}
	panic(errorSig{errs.RuntimeHint(
		TypeName(fn)+" 不是函数，管道的这一步走不下去。", TypeName(fn)+" is not a function.",
		"管道的每一步都要是函数；检查名字是不是写错了。", line)})
}

// evalCallPrepend 调用步骤函数，把流经的值插入为第一个实参。
func (in *Interp) evalCallPrepend(e *parser.Call, v Value, env *Env) Value {
	fn := in.eval(e.Fn, env)
	var pos []Value
	pos = append(pos, v)
	named := map[string]Value{}
	var namedOrder []string
	for _, a := range e.Args {
		arg := in.eval(a.Value, env)
		if a.Name != "" {
			named[a.Name] = arg
			namedOrder = append(namedOrder, a.Name)
		} else {
			pos = append(pos, arg)
		}
	}
	switch f := fn.(type) {
	case *Builtin:
		out, errB := f.Call(in, pos, named, e.Line)
		if errB != nil {
			panic(errorSig{errB})
		}
		return out
	case *BoundMember:
		out, errB := f.Call(in, f.Recv, pos, named, e.Line)
		if errB != nil {
			panic(errorSig{errB})
		}
		return out
	case *SahouFn:
		return in.callSahou(f, pos, named, namedOrder, e.Line)
	}
	panic(errorSig{errs.RuntimeHint(
		TypeName(fn)+" 不是函数，不能作为管道步骤。", TypeName(fn)+" is not a function.", "", e.Line)})
}

func (in *Interp) indexValue(container, idx Value, line int) Value {
	switch col := container.(type) {
	case *List:
		i, e := IntOf(idx, line)
		if e != nil {
			panic(errorSig{e})
		}
		if i < 0 {
			panic(errorSig{errs.RuntimeHint(
				"列表下标不能是负数。", "list index cannot be negative.",
				"想取最后一个元素，写 名单[长度(名单) - 1]。", line)})
		}
		if i >= len(col.Items) {
			panic(errorSig{errs.RuntimeHint(
				fmt.Sprintf("列表只有 %d 个元素，取不到第 %d 个（下标从 0 开始，合法范围是 0 到 %d）。",
					len(col.Items), i, len(col.Items)-1),
				fmt.Sprintf("the list has %d elements, so index %d is out of range (valid: 0 to %d).",
					len(col.Items), i, len(col.Items)-1),
				"想取最后一段，用 取(名单, 起, 止)。", line)})
		}
		return col.Items[i]
	case string:
		i, e := IntOf(idx, line)
		if e != nil {
			panic(errorSig{e})
		}
		runes := []rune(col)
		if i < 0 || i >= len(runes) {
			panic(errorSig{errs.RuntimeHint(
				fmt.Sprintf("这段文本有 %d 个字符，取不到第 %d 个（下标从 0 开始，合法范围是 0 到 %d）。",
					len(runes), i, len(runes)-1),
				fmt.Sprintf("the string has %d character(s), so index %d is out of range (valid: 0 to %d).",
					len(runes), i, len(runes)-1),
				"取一段请用 取(文本, 起, 止)。", line)})
		}
		return string(runes[i])
	case *Dict:
		enc, ok := KeyEncode(idx)
		if !ok {
			panic(errorSig{errs.RuntimeHint(
				"字典的键只能是文本或数。", "dict keys must be text or a number.",
				"用文本或数当键。", line)})
		}
		if v, ok := col.Get(enc); ok {
			return v
		}
		cands := displayKeys(col)
		sug := errs.DidYouMean(Str(idx), cands)
		if sug != "" {
			panic(errorSig{errs.RuntimeHint(
				"字典里没有键 "+Str(idx)+"。你是不是想写 \""+sug+"\"？",
				"the dict has no key "+Str(idx)+". Did you mean \""+sug+"\"?",
				"现有的键有："+strings.Join(cands, "、")+"。", line)})
		}
		panic(errorSig{errs.RuntimeHint(
			"字典里没有键 "+Str(idx)+"。", "the dict has no key "+Str(idx)+".",
			"现有的键有："+strings.Join(cands, "、")+"。", line)})
	default:
		panic(errorSig{errs.RuntimeHint(
			TypeName(container)+" 不能按下标取值。", TypeName(container)+" does not support indexing.",
			"只有列表、文本、字典可以按下标取值。", line)})
	}
}

func displayKeys(d *Dict) []string {
	var out []string
	for _, kv := range d.KeysAsValues() {
		out = append(out, Str(kv))
	}
	return out
}

func (in *Interp) memberValue(recv Value, name string, line int) Value {
	switch col := recv.(type) {
	case *List:
		if m := listMember(name); m != nil {
			fn := m
			return &BoundMember{Recv: recv, Zh: m.zh, En: m.en,
				Call: func(in *Interp, r Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
					return fn.fn(in, r, args, named, line)
				}}
		}
		panic(errorSig{errs.RuntimeHint(
			"列表没有成员 "+name+"。", "lists have no member "+name+".",
			"列表的成员只有 添加/append 和 去重/dedupe；其余操作用全局函数，比如 排序(...)。", line)})
	case *Server:
		if m := serverMembers[name]; m != nil {
			fn := m
			return &BoundMember{Recv: recv, Zh: m.zh, En: m.en,
				Call: func(in *Interp, r Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
					return fn.fn(in, r, args, named, line)
				}}
		}
		panic(errorSig{errs.RuntimeHint(
			"服务没有成员 "+name+"。", "the server has no member "+name+".",
			"服务的成员有 路由/route、监听/listen、静态/static。", line)})
	case *Database:
		if m := dbMembers[name]; m != nil {
			fn := m
			return &BoundMember{Recv: recv, Zh: m.zh, En: m.en,
				Call: func(in *Interp, r Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
					return fn.fn(in, r, args, named, line)
				}}
		}
		panic(errorSig{errs.RuntimeHint(
			"库没有成员 "+name+"。", "the database has no member "+name+".",
			"库的成员有 执行/execute、查询/query、表/tables、关闭/close。", line)})
	case *UIPage:
		if m := uiPageMembers[name]; m != nil {
			fn := m
			return &BoundMember{Recv: recv, Zh: m.zh, En: m.en,
				Call: func(in *Interp, r Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
					return fn.fn(in, r, args, named, line)
				}}
		}
		panic(errorSig{errs.RuntimeHint(
			"页面没有成员 "+name+"。", "the page has no member "+name+".",
			"页面的成员有 标题/title、标签/label、按钮/button、输入框/input、复选/checkbox、下拉/select、行/row、弹窗/alert、显示/show、到HTML/to_html。", line)})
	case *UIWidget:
		if m := uiWidgetMembers[name]; m != nil {
			fn := m
			return &BoundMember{Recv: recv, Zh: m.zh, En: m.en,
				Call: func(in *Interp, r Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
					return fn.fn(in, r, args, named, line)
				}}
		}
		panic(errorSig{errs.RuntimeHint(
			"控件没有成员 "+name+"。", "the widget has no member "+name+".",
			"控件的成员有 改文本/set_text、取文本/text、改占位/set_placeholder、选中/checked、勾/set_checked、选中项/selected、改选项/set_options。", line)})
	case *Dict:
		if m := dictMember(name); m != nil {
			fn := m
			return &BoundMember{Recv: recv, Zh: m.zh, En: m.en,
				Call: func(in *Interp, r Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
					return fn.fn(in, r, args, named, line)
				}}
		}
		if v, ok := col.Get("s:" + name); ok {
			return v
		}
		cands := displayKeys(col)
		sug := errs.DidYouMean(name, cands)
		if sug != "" {
			panic(errorSig{errs.RuntimeHint(
				"字典里没有字段 "+name+"。你是不是想写 \""+sug+"\"？",
				"the dict has no field "+name+". Did you mean \""+sug+"\"?",
				"现有的字段有："+strings.Join(cands, "、")+"。", line)})
		}
		panic(errorSig{errs.RuntimeHint(
			"字典里没有字段 "+name+"。", "the dict has no field "+name+".",
			"现有的字段有："+strings.Join(cands, "、")+"；也可以直接赋一个新字段。", line)})
	default:
		hint := "只有字典和列表有成员。"
		if _, ok := recv.(string); ok {
			hint = "文本没有成员；用全局函数，比如 转大写(...)、替换(...)。"
		}
		panic(errorSig{errs.RuntimeHint(
			TypeName(recv)+" 没有成员。", TypeName(recv)+" has no members.", hint, line)})
	}
}

// evalBin 算术 / 比较 / 逻辑（规范 3.3）。
func (in *Interp) evalBin(e *parser.Bin, env *Env) Value {
	switch e.Op {
	case "and":
		return Truthy(in.eval(e.L, env)) && Truthy(in.eval(e.R, env))
	case "or":
		return Truthy(in.eval(e.L, env)) || Truthy(in.eval(e.R, env))
	}
	l := in.eval(e.L, env)
	r := in.eval(e.R, env)
	switch e.Op {
	case lexer.PLUS, lexer.MINUS, lexer.STAR, lexer.SLASH, lexer.PERCENT:
		return in.arith(e.Op, l, r, e.Line)
	case lexer.EQ, lexer.NEQ:
		eq, errEq := DeepEqual(l, r, map[[2]interface{}]bool{})
		if errEq != nil {
			errEq.Line = e.Line
			panic(errorSig{errEq})
		}
		if e.Op == lexer.EQ {
			return eq
		}
		return !eq
	default: // < <= > >=
		return in.compare(e.Op, l, r, e.Line)
	}
}

func (in *Interp) arith(op string, l, r Value, line int) Value {
	lr, lok := l.(*big.Rat)
	rr, rok := r.(*big.Rat)
	if lok && rok {
		switch op {
		case lexer.PLUS:
			return new(big.Rat).Add(lr, rr)
		case lexer.MINUS:
			return new(big.Rat).Sub(lr, rr)
		case lexer.STAR:
			return new(big.Rat).Mul(lr, rr)
		case lexer.SLASH:
			if rr.Sign() == 0 {
				panic(errorSig{errs.Runtime("不能除以 0。", "cannot divide by zero.", line)})
			}
			return new(big.Rat).Quo(lr, rr) // 真除法（D3）：整除则分母为 1，否则精确分数
		case lexer.PERCENT:
			if rr.Sign() == 0 {
				panic(errorSig{errs.Runtime("不能对 0 取余。", "cannot take remainder by zero.", line)})
			}
			// 规范 3.3 的基准：-7 % 3 得 2（floor 除法语义，同 Python）
			q := new(big.Rat).Quo(lr, rr)
			floorQ := new(big.Int).Div(q.Num(), q.Denom()) // Div 为 floor（分母恒正）
			prod := new(big.Rat).Mul(new(big.Rat).SetInt(floorQ), rr)
			return new(big.Rat).Sub(lr, prod)
		}
	}
	// 文本与列表的 + * 重载（规范 3.3）
	if op == lexer.PLUS {
		if ls, ok := l.(string); ok {
			if rs, ok2 := r.(string); ok2 {
				return ls + rs
			}
		}
		if ll, ok := l.(*List); ok {
			if rl, ok2 := r.(*List); ok2 {
				items := make([]Value, len(ll.Items)+len(rl.Items))
				copy(items, ll.Items)
				copy(items[len(ll.Items):], rl.Items)
				return &List{Items: items}
			}
		}
	}
	if op == lexer.STAR {
		if ls, ok := l.(string); ok && IsIntVal(r) {
			return repeatString(ls, AsInt(r), line)
		}
		if IsIntVal(l) {
			if rs, ok := r.(string); ok {
				return repeatString(rs, AsInt(l), line)
			}
		}
		if ll, ok := l.(*List); ok && IsIntVal(r) {
			return repeatList(ll.Items, AsInt(r), line)
		}
		if IsIntVal(l) {
			if rl, ok := r.(*List); ok {
				return repeatList(rl.Items, AsInt(l), line)
			}
		}
	}
	zh, en := badOpMsg(op, l, r)
	panic(errorSig{errs.RuntimeHint(zh, en, fixHint(op, l, r), line)})
}

func AsInt(v Value) int {
	r := v.(*big.Rat)
	i := new(big.Int).Quo(r.Num(), r.Denom())
	return int(i.Int64())
}

func repeatString(s string, n int, line int) Value {
	if n < 0 {
		panic(errorSig{errs.RuntimeHint(
			"重复次数不能是负数。", "repeat count cannot be negative.",
			"负数相当于重复 0 次，直接写 0 或 \"\" 即可。", line)})
	}
	return strings.Repeat(s, n)
}

func repeatList(items []Value, n int, line int) Value {
	if n < 0 {
		panic(errorSig{errs.RuntimeHint(
			"重复次数不能是负数。", "repeat count cannot be negative.",
			"负数相当于重复 0 次，直接写 [] 即可。", line)})
	}
	out := make([]Value, 0, len(items)*n)
	for i := 0; i < n; i++ {
		out = append(out, items...)
	}
	return &List{Items: out}
}

func badOpMsg(op string, l, r Value) (string, string) {
	switch op {
	case lexer.PLUS:
		zh := fmt.Sprintf("不能把 %s 和 %s 相加。", TypeName(l), TypeName(r))
		en := fmt.Sprintf("cannot add %s and %s.", TypeName(l), TypeName(r))
		return zh, en
	}
	zh := fmt.Sprintf("%s 和 %s 之间不能做 %s 运算。", TypeName(l), TypeName(r), op)
	en := fmt.Sprintf("cannot apply %s to %s and %s.", op, TypeName(l), TypeName(r))
	return zh, en
}

func fixHint(op string, l, r Value) string {
	if op == lexer.PLUS {
		if IsNum(l) {
			return fmt.Sprintf("你可能是想把数字转换成文本，试试 文本(%s)？", exprName(r))
		}
		if IsNum(r) {
			return fmt.Sprintf("你可能是想把数字转换成文本，试试 文本(%s)？", exprName(l))
		}
		return "相加的两边要么都是数，要么都是文本，要么都是列表。"
	}
	return "检查两边的类型；需要转换时用 数(...) 或 文本(...)。"
}

func exprName(v Value) string {
	switch x := v.(type) {
	case *big.Rat:
		return FmtNum(x)
	case string:
		return x
	}
	return "值"
}

func (in *Interp) compare(op string, l, r Value, line int) Value {
	lr, lok := l.(*big.Rat)
	rr, rok := r.(*big.Rat)
	if lok && rok {
		return ratCompare(op, lr.Cmp(rr))
	}
	ls, lok := l.(string)
	rs, rok := r.(string)
	if lok && rok {
		return ratCompare(op, strings.Compare(ls, rs))
	}
	lb, lok := l.(bool)
	rb, rok := r.(bool)
	if lok && rok {
		cmp := 0
		switch {
		case lb == rb:
			cmp = 0
		case !lb && rb:
			cmp = -1
		default:
			cmp = 1
		}
		return ratCompare(op, cmp)
	}
	panic(errorSig{errs.RuntimeHint(
		fmt.Sprintf("%s 和 %s 没有大小之分，不能比较。", TypeName(l), TypeName(r)),
		fmt.Sprintf("%s and %s are not order-comparable.", TypeName(l), TypeName(r)),
		"< <= > >= 只能比较两个数或两个文本；判断是否相等用 ==。", line)})
}

func ratCompare(op string, cmp int) bool {
	switch op {
	case lexer.LT:
		return cmp < 0
	case lexer.LE:
		return cmp <= 0
	case lexer.GT:
		return cmp > 0
	default: // GE
		return cmp >= 0
	}
}

// ---------- 调用 ----------

func (in *Interp) evalCall(e *parser.Call, env *Env) Value {
	fn := in.eval(e.Fn, env)
	// 先按位置/名字求值实参
	var pos []Value
	named := map[string]Value{}
	var namedOrder []string
	for _, a := range e.Args {
		v := in.eval(a.Value, env)
		if a.Name != "" {
			if _, dup := named[a.Name]; dup {
				panic(errorSig{errs.RuntimeHint(
					"参数 "+a.Name+" 给了两次。", "argument "+a.Name+" is given twice.",
					"同一个参数只能给一次。", e.Line)})
			}
			named[a.Name] = v
			namedOrder = append(namedOrder, a.Name)
		} else {
			if len(named) > 0 {
				panic(errorSig{errs.RuntimeHint(
					"命名实参要写在普通实参后面。", "named arguments must come after positional ones.",
					"把 名字: 值 形式的参数挪到最后，例如 print(x, 结尾: \"\")。", e.Line)})
			}
			pos = append(pos, v)
		}
	}
	switch f := fn.(type) {
	case *Builtin:
		v, errB := in.callBuiltin(f, pos, named, e.Line)
		if errB != nil {
			panic(errorSig{errB})
		}
		return v
	case *BoundMember:
		v, errB := f.Call(in, f.Recv, pos, named, e.Line)
		if errB != nil {
			panic(errorSig{errB})
		}
		return v
	case *SahouFn:
		return in.callSahou(f, pos, named, namedOrder, e.Line)
	}
	panic(errorSig{errs.RuntimeHint(
		TypeName(fn)+" 不是函数，不能调用。", TypeName(fn)+" is not callable.",
		"函数要先定义或赋值再调用；检查变量名是不是写错了。", e.Line)})
}

func (in *Interp) callSahou(f *SahouFn, pos []Value, named map[string]Value, namedOrder []string, line int) Value {
	if len(pos) > len(f.Params) {
		got := TypeName(pos[len(f.Params)])
		panic(errorSig{errs.RuntimeHint(
			fmt.Sprintf("函数 %s 只要 %d 个参数，但给了至少 %d 个（多出来的是%s）。",
				fnName(f), len(f.Params), len(pos), got),
			fmt.Sprintf("function %s takes %d arguments but got %d.", fnName(f), len(f.Params), len(pos)),
			"参数个数要与定义一致。", line)})
	}
	callEnv := NewEnv(f.Env) // 词法作用域：父环境是定义处（规范 5.3）
	for i, p := range f.Params {
		if i < len(pos) {
			callEnv.Set(p, pos[i])
			continue
		}
		// 位置不够：在命名实参里找（用户参数不做双语别名，名字必须完全一致）
		if v, ok := named[p]; ok {
			callEnv.Set(p, v)
			delete(named, p)
			continue
		}
		sug := errs.DidYouMean(p, namedOrder)
		missing := fmt.Sprintf("函数 %s 缺少参数 %s。", fnName(f), p)
		missingEn := fmt.Sprintf("function %s is missing argument %s.", fnName(f), p)
		hint := fmt.Sprintf("定义是 函数 %s(%s)，要给 %d 个参数。", fnName(f), strings.Join(f.Params, ", "), len(f.Params))
		if sug != "" {
			missing += fmt.Sprintf("你是不是想写 %s？", sug)
			missingEn += fmt.Sprintf(" Did you mean %s?", sug)
		}
		panic(errorSig{errs.RuntimeHint(missing, missingEn, hint, line)})
	}
	if len(named) > 0 {
		for _, unknown := range namedOrder {
			if _, still := named[unknown]; !still {
				continue
			}
			sug := errs.DidYouMean(unknown, f.Params)
			zh := fmt.Sprintf("函数 %s 没有名叫 %s 的参数。", fnName(f), unknown)
			en := fmt.Sprintf("function %s has no parameter named %s.", fnName(f), unknown)
			if sug != "" {
				zh += fmt.Sprintf("你是不是想写 %s？", sug)
				en += fmt.Sprintf(" Did you mean %s?", sug)
			}
			panic(errorSig{errs.RuntimeHint(zh, en,
				fmt.Sprintf("定义是 函数 %s(%s)。", fnName(f), strings.Join(f.Params, ", ")), line)})
		}
	}
	// 调用链与递归深度
	if in.depth >= in.maxDepth {
		panic(errorSig{errs.RuntimeHint(
			"函数嵌套调用太深了（超过 "+fmt.Sprint(in.maxDepth)+" 层）。", "function call nesting is too deep.",
			"通常是递归没有写结束条件；检查递归什么时候停下来。", line)})
	}
	in.depth++
	in.chain = append(in.chain, errs.Frame{Line: line, Func: fnName(f)})
	var result Value = nil
	func() {
		defer func() {
			if r := recover(); r != nil {
				if sig, ok := r.(returnSig); ok {
					result = sig.v
					return
				}
				panic(r)
			}
		}()
		in.execBlock(f.Body, callEnv)
	}()
	in.chain = in.chain[:len(in.chain)-1]
	in.depth--
	return result
}

// dirChain 从 base 逐级向上到文件系统根（stones 包随项目根走，node_modules 惯例）。
func dirChain(base string) []string {
	var out []string
	d, err := filepath.Abs(base)
	if err != nil {
		return []string{base}
	}
	for {
		out = append(out, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return out
}

func fnName(f *SahouFn) string {
	if f.Name == "" {
		return "匿名函数"
	}
	return f.Name
}

// ScriptPath 返回相对路径相对脚本目录的绝对化结果。
func (in *Interp) ScriptPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(in.ScriptDir, p)
}

// Chain 返回当前调用链的副本（CLI 打印调用位置用）。
func (in *Interp) Chain() []errs.Frame {
	out := make([]errs.Frame, len(in.chain))
	copy(out, in.chain)
	return out
}

// callBuiltin 绑定内置函数的实参并调用（位置 + 命名，别名见规范 6.1）。
func (in *Interp) callBuiltin(b *Builtin, pos []Value, named map[string]Value, line int) (Value, *errs.Error) {
	return b.Call(in, pos, named, line)
}

// pipeUsesIt 检查管道步骤表达式里是否用了 它（递归遍历子表达式）。
func pipeUsesIt(x parser.Expr) bool {
	switch e := x.(type) {
	case *parser.Ident:
		return e.Name == "它"
	case *parser.NumLit, *parser.StrLit, *parser.BoolLit, *parser.NullLit:
		if st, ok := x.(*parser.StrLit); ok {
			for _, p := range st.Parts {
				if p.Expr != nil && pipeUsesIt(p.Expr) {
					return true
				}
			}
		}
		return false
	case *parser.ListLit:
		for _, el := range e.Elems {
			if pipeUsesIt(el) {
				return true
			}
		}
		return false
	case *parser.DictLit:
		for _, it := range e.Items {
			if pipeUsesIt(it.Key) || pipeUsesIt(it.Val) {
				return true
			}
		}
		return false
	case *parser.Bin:
		return pipeUsesIt(e.L) || pipeUsesIt(e.R)
	case *parser.Un:
		return pipeUsesIt(e.X)
	case *parser.Not:
		return pipeUsesIt(e.X)
	case *parser.Index:
		return pipeUsesIt(e.X) || pipeUsesIt(e.Idx)
	case *parser.Member:
		return pipeUsesIt(e.X)
	case *parser.Call:
		if pipeUsesIt(e.Fn) {
			return true
		}
		for _, a := range e.Args {
			if pipeUsesIt(a.Value) {
				return true
			}
		}
		return false
	case *parser.AnonFn:
		return false // 匿名函数引入新作用域，内部 它 与管道无关
	}
	return false
}
