// Package transpile 实现 sahou→JavaScript 转译器（05 文档 3.3 转译规则表）。
// 复用同一套 lexer/parser/AST，只新增代码生成后端——分层铁律：不 import interp。
package transpile

import (
	_ "embed"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"sahou/internal/errs"
	"sahou/internal/lexer"
	"sahou/internal/parser"
	"sahou/internal/stonesrc"
)

//go:embed rt.js
var runtimeJS string

// jsReserved 与 sahou 标识符冲突的 JS 保留字/全局名，加 _ 前缀重命名。
var jsReserved = map[string]bool{
	"class": true, "new": true, "typeof": true, "function": true, "var": true,
	"const": true, "if": true, "else": true, "return": true, "for": true,
	"while": true, "do": true, "switch": true, "case": true, "break": true,
	"continue": true, "try": true, "catch": true, "finally": true, "throw": true,
	"this": true, "super": true, "import": true, "export": true, "default": true,
	"extends": true, "instanceof": true, "delete": true, "void": true, "yield": true,
	"async": true, "await": true, "static": true, "get": true, "set": true,
	"null": true, "true": true, "false": true, "undefined": true, "NaN": true,
	"Infinity": true, "of": true, "with": true, "debugger": true, "enum": true,
}

func rename(name string) string {
	if jsReserved[name] {
		return "_" + name
	}
	return name
}

// builtinJS 29 个内置函数 → 运行时辅助函数（3.3 第 2 表：一一映射到 __saho_*）。
var builtinJS = map[string]string{
	"打印": "__saho_print", "print": "__saho_print",
	"输入": "__saho_input", "input": "__saho_input",
	"数": "__saho_tonum", "num": "__saho_tonum",
	"文本转数": "__saho_tonum", "str_to_num": "__saho_tonum",
	"文本": "__saho_str", "str": "__saho_str",
	"列表": "__saho_tolist", "list": "__saho_tolist",
	"字典": "__saho_todict", "dict": "__saho_todict",
	"从a到b": "__saho_range_to", "range_to": "__saho_range_to",
	"长度": "__saho_len", "len": "__saho_len",
	"取": "__saho_slice", "slice": "__saho_slice",
	"抛出": "__saho_throw", "throw": "__saho_throw",
	"类型": "__saho_type_of", "type_of": "__saho_type_of",
	"包含": "__saho_contains", "contains": "__saho_contains",
	"求和": "__saho_sum", "sum": "__saho_sum",
	"最大值": "__saho_maxmin_p", "max": "__saho_maxmin_p",
	"最小值": "__saho_min_p", "min": "__saho_min_p",
	"排序": "__saho_sorted", "sorted": "__saho_sorted",
	"反转": "__saho_reversed", "reversed": "__saho_reversed",
	"连接": "__saho_join", "join": "__saho_join",
	"分割": "__saho_split", "split": "__saho_split",
	"替换": "__saho_replace", "replace": "__saho_replace",
	"修剪": "__saho_trim", "trim": "__saho_trim",
	"位置": "__saho_find", "find": "__saho_find",
	"转大写": "__saho_upper", "upper": "__saho_upper",
	"转小写": "__saho_lower", "lower": "__saho_lower",
	"绝对值": "__saho_abs", "abs": "__saho_abs",
	"平方根": "__saho_sqrt", "sqrt": "__saho_sqrt",
	"四舍五入": "__saho_round", "round": "__saho_round",
	"读取文件": "__saho_unavailable_read_file", "read_file": "__saho_unavailable_read_file",
	"写入文件": "__saho_unavailable_write_file", "write_file": "__saho_unavailable_write_file",
	"文件存在": "__saho_unavailable_file_exists", "file_exists": "__saho_unavailable_file_exists",
}

var binopJS = map[string]string{
	lexer.PLUS: "__saho_add", lexer.MINUS: "__saho_sub", lexer.STAR: "__saho_mul",
	lexer.SLASH: "__saho_div", lexer.PERCENT: "__saho_mod",
	lexer.EQ: "__saho_eq", lexer.NEQ: "__saho_ne",
	lexer.LT: "__saho_lt", lexer.LE: "__saho_le", lexer.GT: "__saho_gt", lexer.GE: "__saho_ge",
}

type Builder struct {
	out   strings.Builder
	ind   int
	mods  map[string]*modUnit
	itVar string // 管道步骤生成时，它 绑定到的 JS 参数名
}

// modIDByName 找到某默认名对应的模块 id（同一模块可能被多个名字引入，取首个）。
func modIDByName(name string, mods map[string]*modUnit) string {
	for id, u := range mods {
		if u.name == name {
			return id
		}
	}
	return name
}

// modUnit 一个用户模块的转译中间产物。
type modUnit struct {
	id    string // 以 / 分隔的路径 id
	name  string // 引入时的默认名
	prog  *parser.Program
	base  string
	names []string // 顶层名字（导出表）
}

// Build 把 AST 转成完整 JS 文件（运行时前导 + 模块工厂 + 用户程序 IIFE）。
func Build(prog *parser.Program, baseDir string) (string, *errs.Error) {
	mods := map[string]*modUnit{}
	if e := collectModules(prog, baseDir, mods, nil); e != nil {
		return "", e
	}
	names, e := collectAssigned(prog.Stmts)
	if e != nil {
		return "", e
	}
	b := &Builder{mods: mods}
	b.out.WriteString("// 由 sahou build 生成 —— 请勿手改\n")
	b.out.WriteString(runtimeJS)
	b.ind = 0
	// 用户模块工厂（转译期已做循环检测）
	for _, u := range mods {
		b.linef("__saho_require(%s, function () {", goQuote(u.id))
		b.ind++
		for _, n := range u.names {
			b.linef("let %s;", rename(n))
		}
		for _, st := range u.prog.Stmts {
			if e := b.stmt(st); e != nil {
				return "", e
			}
		}
		parts := make([]string, len(u.names))
		for i, n := range u.names {
			parts[i] = "[" + goQuote(n) + ", " + rename(n) + "]"
		}
		b.linef("return new Map([%s]);", strings.Join(parts, ", "))
		b.ind--
		b.linef("});")
	}
	b.out.WriteString("\n(function () {\n  \"use strict\";\n")
	b.ind = 1
	for _, n := range names {
		b.linef("let %s;", rename(n))
	}
	for _, s := range prog.Stmts {
		if e := b.stmt(s); e != nil {
			return "", e
		}
	}
	b.out.WriteString("})();\n")
	return b.out.String(), nil
}

// collectModules 沿引入语句递归收集本地用户模块（转译期循环检测，08 文档 M6）。
func collectModules(prog *parser.Program, base string, mods map[string]*modUnit, stack []string) *errs.Error {
	for _, s := range prog.Stmts {
		imp, ok := s.(*parser.ImportStmt)
		if !ok || isBuiltinModule(imp.Name) {
			continue
		}
		// 磁盘优先；找不到再兜 exe 内嵌 stones（与解释器 loadModule 同口径）
		path := resolveModule(base, imp.Name)
		var id string
		var src []byte
		var unitBase string
		if path != "" {
			id = strings.ReplaceAll(path, "\\", "/")
			data, err := os.ReadFile(path)
			if err != nil {
				return errs.SyntaxHint("读不到模块文件 \""+path+"\"。", "cannot read module file.", "", imp.Line)
			}
			src = data
			unitBase = filepath.Dir(path)
		} else if embeddedSrc, ok := stonesrc.Source(imp.Name); ok {
			id = "内嵌:" + imp.Name
			src = []byte(embeddedSrc)
			unitBase = base // 嵌套引入沿用同一查找基（磁盘优先，再兜内嵌）
		} else {
			return errs.SyntaxHint(
				"找不到模块 \""+imp.Name+"\"。", "cannot find module \""+imp.Name+"\".",
				"转译器在页面文件同级目录和 stones/ 目录里找 名字.saho 或 名字/main.saho；exe 自带的包可以直接引入（sahou stones 查看）。", imp.Line)
		}
		for _, p := range stack {
			if p == id {
				return errs.SyntaxHint(
					"模块 \""+imp.Name+"\" 循环引入了自己。", "module \""+imp.Name+"\" is imported in a cycle.",
					"把共享的部分抽到第三个模块。", imp.Line)
			}
		}
		if _, seen := mods[id]; seen {
			continue
		}
		toks, e := lexer.Tokenize(string(src))
		if e != nil {
			return e
		}
		mp, e := parser.Parse(toks)
		if e != nil {
			return e
		}
		unit := &modUnit{id: id, name: imp.Name, prog: mp, base: unitBase}
		mods[id] = unit
		unit.names, e = collectAssigned(mp.Stmts)
		if e != nil {
			return e
		}
		if e := collectModules(mp, unit.base, mods, append(stack, id)); e != nil {
			return e
		}
	}
	return nil
}

// stdModulePair 模块名的规范中英对（引入时两个拼写都绑定）。
func stdModulePair(name string) (string, string) {
	switch name {
	case "net":
		return "网络", "net"
	case "page":
		return "页面", "page"
	case "random":
		return "随机", "random"
	case "time":
		return "时间", "time"
	case "math":
		return "数学", "math"
	case "encoding":
		return "编码", "encoding"
	case "sys":
		return "系统", "sys"
	}
	return name, name
}

func isBuiltinModule(name string) bool {
	return stdModuleObject(name) != ""
}

// stdModuleObject 内置模块名 → 生成的运行时对象表达式；"" 表示不是内置模块。
// 页面 在浏览器宿主里直接就是 page 对象（宿主保证存在）。
func stdModuleObject(name string) string {
	switch name {
	case "网络", "net":
		return "__saho_mod_net"
	case "页面", "page":
		return "__saho_mod_page"
	case "随机", "random":
		return "__saho_std_random"
	case "时间", "time":
		return "__saho_std_time"
	case "数学", "math":
		return "__saho_std_math"
	case "编码", "encoding":
		return "__saho_std_encoding"
	case "系统", "sys":
		return "__saho_std_sys"
	}
	return ""
}

func resolveModule(base, name string) string {
	var dirs []string
	d, err := filepath.Abs(base)
	if err == nil {
		for {
			dirs = append(dirs, d)
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	} else {
		dirs = []string{base}
	}
	for _, dir := range dirs {
		candidates := []string{
			filepath.Join(dir, name+".saho"),
			filepath.Join(dir, name, "main.saho"),
			filepath.Join(dir, "stones", name+".saho"),
			filepath.Join(dir, "stones", name, "main.saho"),
		}
		for _, c := range candidates {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
		}
	}
	return ""
}

func (b *Builder) linef(format string, args ...interface{}) {
	fmt.Fprintf(&b.out, strings.Repeat("  ", b.ind)+format+"\n", args...)
}

// collectAssigned 收集一个函数作用域内（不含嵌套函数体）被赋值/定义的名字。
func collectAssigned(stmts []parser.Stmt) ([]string, *errs.Error) {
	var names []string
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	var walk func(stmts []parser.Stmt) *errs.Error
	walk = func(stmts []parser.Stmt) *errs.Error {
		for _, s := range stmts {
			switch st := s.(type) {
			case *parser.LetStmt:
				if id, ok := st.Target.(*parser.Ident); ok {
					add(id.Name)
				}
			case *parser.ForStmt:
				add(st.Var)
				if e := walk(st.Body); e != nil {
					return e
				}
			case *parser.ImportStmt:
				switch st.Name {
				case "网络", "net":
					add("网络")
					add("net")
				case "页面", "page":
					add("页面")
					add("page")
				default:
					add(st.Name)
				}
			case *parser.TryStmt:
				add(st.CatchVar)
				if e := walk(st.Body); e != nil {
					return e
				}
				if e := walk(st.Catch); e != nil {
					return e
				}
			case *parser.FnStmt:
				add(st.Name)
			case *parser.IfStmt:
				for _, blk := range st.Blocks {
					if e := walk(blk); e != nil {
						return e
					}
				}
				if e := walk(st.Else); e != nil {
					return e
				}
			case *parser.WhileStmt:
				if e := walk(st.Body); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := walk(stmts); e != nil {
		return nil, e
	}
	return names, nil
}

func (b *Builder) stmt(s parser.Stmt) *errs.Error {
	switch st := s.(type) {
	case *parser.LetStmt:
		val, e := b.expr(st.Value)
		if e != nil {
			return e
		}
		switch t := st.Target.(type) {
		case *parser.Ident:
			b.linef("%s = %s;", rename(t.Name), val)
		case *parser.Index:
			xx, e2 := b.expr(t.X)
			if e2 != nil {
				return e2
			}
			idx, e2 := b.expr(t.Idx)
			if e2 != nil {
				return e2
			}
			b.linef("__saho_set(%s, %s, %s);", xx, idx, val)
		case *parser.Member:
			xx, e2 := b.expr(t.X)
			if e2 != nil {
				return e2
			}
			b.linef("__saho_set(%s, %s, %s);", xx, goQuote(t.Name), val)
		default:
			return errs.Syntax("= 的左边只能是要赋值的目标。", "invalid assignment target.", st.Line)
		}
	case *parser.ExprStmt:
		x, e := b.expr(st.X)
		if e != nil {
			return e
		}
		b.linef("%s;", x)
	case *parser.IfStmt:
		for i, cond := range st.Conds {
			c, e := b.expr(cond)
			if e != nil {
				return e
			}
			if i == 0 {
				b.linef("if (__saho_truthy(%s)) {", c)
			} else {
				b.linef("} else if (__saho_truthy(%s)) {", c)
			}
			b.ind++
			for _, sub := range st.Blocks[i] {
				if e := b.stmt(sub); e != nil {
					return e
				}
			}
			b.ind--
		}
		if st.Else != nil {
			b.linef("} else {")
			b.ind++
			for _, sub := range st.Else {
				if e := b.stmt(sub); e != nil {
					return e
				}
			}
			b.ind--
		}
		b.linef("}")
	case *parser.WhileStmt:
		c, e := b.expr(st.Cond)
		if e != nil {
			return e
		}
		b.linef("while (__saho_truthy(%s)) {", c)
		b.ind++
		for _, sub := range st.Body {
			if e := b.stmt(sub); e != nil {
				return e
			}
		}
		b.ind--
		b.linef("}")
	case *parser.ForStmt:
		iter, e := b.expr(st.Iter)
		if e != nil {
			return e
		}
		b.linef("for (%s of __saho_iter(%s)) {", rename(st.Var), iter)
		b.ind++
		for _, sub := range st.Body {
			if e := b.stmt(sub); e != nil {
				return e
			}
		}
		b.ind--
		b.linef("}")
	case *parser.FnStmt:
		params := make([]string, len(st.Params))
		for i, p := range st.Params {
			params[i] = rename(p)
		}
		names, e := collectAssigned(st.Body)
		if e != nil {
			return e
		}
		paramSet := map[string]bool{}
		for _, pn := range st.Params {
			paramSet[pn] = true
		}
		b.linef("%s = __saho_defn(function %s(%s) {", rename(st.Name), rename(st.Name), strings.Join(params, ", "))
		b.ind++
		for _, n := range names {
			if n == st.Name || paramSet[n] {
				continue // 自身名字由外层 let 承载；参数名是函数级绑定，不能再 let
			}
			b.linef("let %s;", rename(n))
		}
		for _, sub := range st.Body {
			if e := b.stmt(sub); e != nil {
				return e
			}
		}
		b.linef("return null;")
		b.ind--
		b.linef("}, [%s], %q);", joinRenamed(st.Params), st.Name)
	case *parser.ReturnStmt:
		if st.Value == nil {
			b.linef("return;")
		} else {
			v, e := b.expr(st.Value)
			if e != nil {
				return e
			}
			b.linef("return %s;", v)
		}
	case *parser.ImportStmt:
		if obj := stdModuleObject(st.Name); obj != "" {
			zh, en := stdModulePair(st.Name)
			b.linef("%s = %s;", rename(zh), obj)
			b.linef("%s = %s;", rename(en), obj)
		} else {
			b.linef("%s = __saho_require(%s);", rename(st.Name), goQuote(modIDByName(st.Name, b.mods)))
		}
	case *parser.TryStmt:
		b.linef("try {")
		b.ind++
		for _, sub := range st.Body {
			if e := b.stmt(sub); e != nil {
				return e
			}
		}
		b.ind--
		b.linef("} catch (__saho_e) {")
		b.ind++
		b.linef("%s = __saho_error_text(__saho_e);", rename(st.CatchVar))
		for _, sub := range st.Catch {
			if e := b.stmt(sub); e != nil {
				return e
			}
		}
		b.ind--
		b.linef("}")
	}
	return nil
}

// __saho_numLit 把精确有理数字面量转成最短 JS 数字字面量。
func __saho_numLit(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	f := new(big.Float).SetPrec(200).SetRat(r)
	return f.Text('g', 15)
}

func joinRenamed(ps []string) string {
	quoted := make([]string, len(ps))
	for i, p := range ps {
		quoted[i] = fmt.Sprintf("%q", p)
	}
	return strings.Join(quoted, ", ")
}

func (b *Builder) expr(x parser.Expr) (string, *errs.Error) {
	switch e := x.(type) {
	case *parser.NumLit:
		return __saho_numLit(e.Val), nil
	case *parser.BoolLit:
		if e.Val {
			return "true", nil
		}
		return "false", nil
	case *parser.NullLit:
		return "null", nil
	case *parser.StrLit:
		return b.strLit(e)
	case *parser.Ident:
		if why, bad := serverOnlyModule(e.Name); bad {
			return "", errs.RuntimeHint(
				why+"只能在解释器（本机/服务端）里用，转译成浏览器 JS 后没有对应实现。",
				why+" is server-side only and has no browser build.",
				"浏览器侧请用 页面 模块；要跑服务端程序请用 sahou run。", e.Line)
		}
		return b.ident(e), nil
	case *parser.ListLit:
		parts := make([]string, len(e.Elems))
		for i, el := range e.Elems {
			s, err := b.expr(el)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case *parser.DictLit:
		items := make([]string, len(e.Items))
		for i, it := range e.Items {
			k, err := b.expr(it.Key)
			if err != nil {
				return "", err
			}
			v, err := b.expr(it.Val)
			if err != nil {
				return "", err
			}
			items[i] = "[__saho_key(" + k + "), " + v + "]"
		}
		return "new Map([" + strings.Join(items, ", ") + "])", nil
	case *parser.Bin:
		return b.bin(e)
	case *parser.Un:
		inner, err := b.expr(e.X)
		if err != nil {
			return "", err
		}
		return "__saho_neg(" + inner + ")", nil
	case *parser.Not:
		inner, err := b.expr(e.X)
		if err != nil {
			return "", err
		}
		return "!__saho_truthy(" + inner + ")", nil
	case *parser.Index:
		xx, err := b.expr(e.X)
		if err != nil {
			return "", err
		}
		idx, err := b.expr(e.Idx)
		if err != nil {
			return "", err
		}
		return "__saho_index(" + xx + ", " + idx + ")", nil
	case *parser.Member:
		xx, err := b.expr(e.X)
		if err != nil {
			return "", err
		}
		return "__saho_get(" + xx + ", " + goQuote(e.Name) + ")", nil
	case *parser.AnonFn:
		params := make([]string, len(e.Params))
		for i, p := range e.Params {
			params[i] = rename(p)
		}
		body, err := b.expr(e.Body)
		if err != nil {
			return "", err
		}
		return "__saho_defn(function(" + strings.Join(params, ", ") + ") { return " + body + "; }, [" + joinRenamed(e.Params) + "], \"\")", nil
	case *parser.Call:
		return b.call(e)
	case *parser.Pipe:
		return b.pipe(e)
	}
	return "", errs.Syntax("转译器遇到未知表达式。", "unknown expression in codegen.", 0)
}

// optsBody 命名实参对象字面量。
func optsBody(named map[string]string, order []string) string {
	parts := make([]string, 0, len(order))
	for _, n := range order {
		parts = append(parts, goQuote(n)+": "+named[n])
	}
	return strings.Join(parts, ", ")
}

// jsMemberName 模块成员的 JS 访问形式：ASCII 标识符用点号，中文用下标。
func jsMemberName(module, name string) string {
	if isASCIIIdent(name) {
		return "." + name
	}
	return "[" + goQuote(name) + "]"
}

func isASCIIIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// strLit 文本字面量：模板字符串 + 插值表达式经 __saho_str 归一。
func (b *Builder) strLit(e *parser.StrLit) (string, *errs.Error) {
	var sb strings.Builder
	sb.WriteString("`")
	for _, p := range e.Parts {
		if p.Expr != nil {
			inner, err := b.expr(p.Expr)
			if err != nil {
				return "", err
			}
			sb.WriteString("${__saho_str(" + inner + ")}")
		} else {
			lit := p.Lit
			lit = strings.ReplaceAll(lit, "\\", "\\\\")
			lit = strings.ReplaceAll(lit, "`", "\\`")
			lit = strings.ReplaceAll(lit, "${", "\\${")
			sb.WriteString(lit)
		}
	}
	sb.WriteString("`")
	return sb.String(), nil
}

// ident 变量读取：内置函数名→辅助函数；模块名→模块对象/报错；其余原名。
func (b *Builder) ident(e *parser.Ident) string {
	if h, ok := builtinJS[e.Name]; ok {
		return h
	}
	if obj := stdModuleObject(e.Name); obj != "" {
		return obj
	}
	if e.Name == "它" && b.itVar != "" {
		return b.itVar
	}
	return rename(e.Name)
}

func (b *Builder) bin(e *parser.Bin) (string, *errs.Error) {
	l, err := b.expr(e.L)
	if err != nil {
		return "", err
	}
	r, err := b.expr(e.R)
	if err != nil {
		return "", err
	}
	switch e.Op {
	case "and":
		return "(__saho_truthy(" + l + ") && __saho_truthy(" + r + "))", nil
	case "or":
		return "(__saho_truthy(" + l + ") || __saho_truthy(" + r + "))", nil
	}
	h, ok := binopJS[e.Op]
	if !ok {
		return "", errs.Syntax("转译器遇到未知运算符 "+e.Op+"。", "unknown operator in codegen.", e.Line)
	}
	if e.Op == lexer.NEQ {
		return "!__saho_eq(" + l + ", " + r + ")", nil
	}
	return h + "(" + l + ", " + r + ")", nil
}

func (b *Builder) call(e *parser.Call) (string, *errs.Error) {
	var pos []string
	named := map[string]string{}
	var namedOrder []string
	for _, a := range e.Args {
		v, err := b.expr(a.Value)
		if err != nil {
			return "", err
		}
		if a.Name != "" {
			if _, dup := named[a.Name]; !dup {
				namedOrder = append(namedOrder, a.Name)
			}
			named[a.Name] = v
		} else {
			pos = append(pos, v)
		}
	}
	emitOpts := func() string {
		if len(named) == 0 {
			return ""
		}
		parts := make([]string, 0, len(named))
		for _, n := range namedOrder {
			parts = append(parts, goQuote(n)+": "+named[n])
		}
		return ", {" + strings.Join(parts, ", ") + "}"
	}

	switch fn := e.Fn.(type) {
	case *parser.Ident:
		if h, ok := builtinJS[fn.Name]; ok {
			return h + "(" + strings.Join(pos, ", ") + emitOpts() + ")", nil
		}
		target := b.ident(fn)
		if len(named) > 0 {
			return "__saho_call(" + target + ", [" + strings.Join(pos, ", ") + "], {" + optsBody(named, namedOrder) + "})", nil
		}
		return target + "(" + strings.Join(pos, ", ") + ")", nil
	case *parser.Member:
		if id, ok := fn.X.(*parser.Ident); ok {
			if obj := stdModuleObject(id.Name); obj != "" {
				return obj + jsMemberName(id.Name, fn.Name) + "(" + strings.Join(pos, ", ") + emitOpts() + ")", nil
			}
		}
		xx, err := b.expr(fn.X)
		if err != nil {
			return "", err
		}
		get := "__saho_get(" + xx + ", " + goQuote(fn.Name) + ")"
		if len(named) > 0 {
			return "__saho_call(" + get + ", [" + strings.Join(pos, ", ") + "], {" + optsBody(named, namedOrder) + "})", nil
		}
		return "(" + get + ")(" + strings.Join(pos, ", ") + ")", nil
	default:
		target, err := b.expr(e.Fn)
		if err != nil {
			return "", err
		}
		if len(named) > 0 {
			return "__saho_call(" + target + ", [" + strings.Join(pos, ", ") + "], {" + optsBody(named, namedOrder) + "})", nil
		}
		return "(" + target + ")(" + strings.Join(pos, ", ") + ")", nil
	}
}

// pipe 把管道步骤逐个转成"以流经值为单参的函数"。
func (b *Builder) pipe(e *parser.Pipe) (string, *errs.Error) {
	var fns []string
	for _, step := range e.Steps {
		fnExpr, e2 := b.stepToFn(step)
		if e2 != nil {
			return "", e2
		}
		fns = append(fns, fnExpr)
	}
	base, e2 := b.expr(e.Base)
	if e2 != nil {
		return "", e2
	}
	return "__saho_pipe(" + base + ", [" + strings.Join(fns, ", ") + "])", nil
}

// stepToFn 把一个管道步骤转成 function(__saho_it){ ... } 或函数引用。
func (b *Builder) stepToFn(step parser.Expr) (string, *errs.Error) {
	switch st := step.(type) {
	case *parser.Ident:
		if st.Name != "它" {
			return b.ident(st), nil // 裸函数名：流经值自动作为实参
		}
	case *parser.Member:
		if id, ok := st.X.(*parser.Ident); ok {
			if _, isMod := stdModuleObject2(id.Name); isMod {
				obj := stdModuleObject(id.Name)
				return "function(__saho_it){ return " + obj + jsMemberName(id.Name, st.Name) + "(__saho_it); }", nil
			}
		}
	}

	if containsIt(step) {
		saved := b.itVar
		b.itVar = "__saho_it"
		body, e2 := b.expr(step)
		b.itVar = saved
		if e2 != nil {
			return "", e2
		}
		return "function(__saho_it){ return " + body + "; }", nil
	}

	if call, ok := step.(*parser.Call); ok {
		fnT, e2 := b.expr(call.Fn)
		if e2 != nil {
			return "", e2
		}
		var args []string
		args = append(args, "__saho_it")
		var opts []string
		for _, a := range call.Args {
			av, e2 := b.expr(a.Value)
			if e2 != nil {
				return "", e2
			}
			if a.Name != "" {
				opts = append(opts, goQuote(a.Name)+": "+av)
			} else {
				args = append(args, av)
			}
		}
		if len(opts) > 0 {
			args = append(args, "{"+strings.Join(opts, ", ")+"}")
		}
		return "function(__saho_it){ return " + fnT + "(" + strings.Join(args, ", ") + "); }", nil
	}

	switch step.(type) {
	case *parser.AnonFn:
		t, e2 := b.expr(step)
		return t, e2
	}
	return "", errs.SyntaxHint(
		"管道的这一步要是一个加工步骤（函数）。", "this pipeline step must be a function.",
		"写成 裸函数名、函数调用，或含 它 的表达式。", step.Pos())
}

// stdModuleObject2 判断名字是否内置模块；返回对象名与是否命中。
func stdModuleObject2(name string) (string, bool) {
	obj := stdModuleObject(name)
	return obj, obj != ""
}

func containsIt(x parser.Expr) bool {
	switch e := x.(type) {
	case *parser.Ident:
		return e.Name == "它"
	case *parser.StrLit:
		for _, p := range e.Parts {
			if p.Expr != nil && containsIt(p.Expr) {
				return true
			}
		}
		return false
	case *parser.ListLit:
		for _, el := range e.Elems {
			if containsIt(el) {
				return true
			}
		}
		return false
	case *parser.DictLit:
		for _, it := range e.Items {
			if containsIt(it.Key) || containsIt(it.Val) {
				return true
			}
		}
		return false
	case *parser.Bin:
		return containsIt(e.L) || containsIt(e.R)
	case *parser.Un:
		return containsIt(e.X)
	case *parser.Not:
		return containsIt(e.X)
	case *parser.Index:
		return containsIt(e.X) || containsIt(e.Idx)
	case *parser.Member:
		return containsIt(e.X)
	case *parser.Call:
		if containsIt(e.Fn) {
			return true
		}
		for _, a := range e.Args {
			if containsIt(a.Value) {
				return true
			}
		}
		return false
	}
	return false
}

func goQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// serverOnlyModule 服务端专属模块名（浏览器无对应实现）→ 返回 (中文名, 是否服务端专属)。
func serverOnlyModule(name string) (string, bool) {
	switch name {
	case "网络", "net":
		return "网络/net 模块", true
	case "数据库", "db":
		return "数据库/db 模块", true
	case "应用", "app":
		return "应用/app 模块", true
	case "测试", "assert":
		return "测试/assert 模块", true
	}
	return "", false
}
