// Package parser 包含 sahou 的 AST 定义与递归下降语法分析器（规范 4.2 文法）。
package parser

import (
	"math/big"
	"strings"

	"sahou/internal/errs"
	"sahou/internal/lexer"
)

// ---------- AST ----------

type Stmt interface{ Pos() int }

type Expr interface{ Pos() int }

type Program struct{ Stmts []Stmt }

type Line struct{ N int }

func (l Line) Pos() int { return l.N }

// --- 语句 ---

type LetStmt struct { // [let/设] 目标 = 值（let 前缀可选）
	Target Expr
	Value  Expr
	Line   int
}

type ExprStmt struct {
	X    Expr
	Line int
}

type IfStmt struct {
	Conds  []Expr   // 如果 / 又如 条件（至少 1 个）
	Blocks [][]Stmt // 与 Conds 一一对应
	Else   []Stmt   // 可为 nil
	Line   int
}

type WhileStmt struct {
	Cond Expr
	Body []Stmt
	Line int
}

type ForStmt struct {
	Var  string
	Iter Expr
	Body []Stmt
	Line int
}

type FnStmt struct {
	Name   string
	Params []string
	Body   []Stmt
	Line   int
}

type ReturnStmt struct {
	Value Expr // 可为 nil（返回 空值）
	Line  int
}

// BreakStmt 跳出 —— 结束最近一层循环（v4.1）。
type BreakStmt struct{ Line int }

// ContinueStmt 继续 —— 跳过本轮循环剩余部分（v4.1）。
type ContinueStmt struct{ Line int }

// CellStmt 格子 名字 = 表达式 —— v4 响应式格子（数据流计算模型）。
type CellStmt struct {
	Name string
	Expr Expr
	Line int
}

// ImportStmt 用 "模块名" [引入] —— v2 模块引入（08 文档 M1）。
type ImportStmt struct {
	Name string // 模块名字面量
	Line int
}

type TryStmt struct {
	Body     []Stmt
	CatchVar string
	Catch    []Stmt
	Line     int
}

func (s *LetStmt) Pos() int      { return s.Line }
func (s *ExprStmt) Pos() int     { return s.Line }
func (s *IfStmt) Pos() int       { return s.Line }
func (s *WhileStmt) Pos() int    { return s.Line }
func (s *ForStmt) Pos() int      { return s.Line }
func (s *FnStmt) Pos() int       { return s.Line }
func (s *ReturnStmt) Pos() int   { return s.Line }
func (s *BreakStmt) Pos() int    { return s.Line }
func (s *ContinueStmt) Pos() int { return s.Line }
func (s *CellStmt) Pos() int     { return s.Line }
func (s *ImportStmt) Pos() int   { return s.Line }
func (s *TryStmt) Pos() int      { return s.Line }

// --- 表达式 ---

type NumLit struct {
	Val  *big.Rat
	Line int
}

type StrLit struct {
	Parts []StrPart // Lit 非空为文字；Expr 非空为已解析的表达式
	Line  int
}

type StrPart struct {
	Lit  string
	Expr Expr // 可为 nil
}

type Ident struct {
	Name string
	Line int
}

type ListLit struct {
	Elems []Expr
	Line  int
}

type DictItem struct {
	Key Expr
	Val Expr
}

type DictLit struct {
	Items []DictItem
	Line  int
}

type Bin struct { // + - * / % == != < <= > >= and or
	Op   string
	L, R Expr
	Line int
}

type Un struct { // 一元 -
	X    Expr
	Line int
}

type Not struct { // 非 / not
	X    Expr
	Line int
}

type Index struct {
	X    Expr
	Idx  Expr
	Line int
}

type Member struct {
	X    Expr
	Name string
	Line int
}

type Arg struct {
	Name  string // 命名实参时非空（如 结尾: "  "）
	Value Expr
}

type Call struct {
	Fn   Expr
	Args []Arg
	Line int
}

// Pipe 数据流水线（v3）：Base → 步骤 → 步骤 → ...
// 步骤里的 Ident 它 在求值时绑定"正在流经的值"。
type Pipe struct {
	Base  Expr
	Steps []Expr
	Line  int
}

type AnonFn struct { // 函数(参数) => 表达式
	Params []string
	Body   Expr
	Line   int
}

func (e *NumLit) Pos() int  { return e.Line }
func (e *StrLit) Pos() int  { return e.Line }
func (e *Ident) Pos() int   { return e.Line }
func (e *ListLit) Pos() int { return e.Line }
func (e *DictLit) Pos() int { return e.Line }
func (e *Bin) Pos() int     { return e.Line }
func (e *Un) Pos() int      { return e.Line }
func (e *Not) Pos() int     { return e.Line }
func (e *Index) Pos() int   { return e.Line }
func (e *Member) Pos() int  { return e.Line }
func (e *Call) Pos() int    { return e.Line }
func (e *Pipe) Pos() int    { return e.Line }
func (e *AnonFn) Pos() int  { return e.Line }

// ---------- 分析器 ----------

type Parser struct {
	toks []lexer.Token
	pos  int
}

// Parse 分析整个记号流；语法错误以 *errs.Error（Kind=Syntax）返回。
func Parse(toks []lexer.Token) (*Program, *errs.Error) {
	p := &Parser{toks: toks}
	stmts, e := p.stmts(nil)
	if e != nil {
		return nil, e
	}
	return &Program{Stmts: stmts}, nil
}

// ParseExprSource 解析一段插值表达式源码（词法器从字符串里捕获的），line 用于报错定位。
func ParseExprSource(src string, line int) (Expr, *errs.Error) {
	toks, e := lexer.Tokenize(src)
	if e != nil {
		e.Line = line
		return nil, e
	}
	p := &Parser{toks: toks}
	x, e2 := p.expr()
	if e2 != nil {
		e2.Line = line
		return nil, e2
	}
	if p.cur().Kind != lexer.EOF {
		return nil, errs.SyntaxHint(
			"插值 "+src+" 里有多余的内容。", "unexpected content inside interpolation "+src+".",
			"花括号里只能放一个表达式。", line)
	}
	return x, nil
}

func (p *Parser) cur() lexer.Token { return p.toks[p.pos] }
func (p *Parser) peek() lexer.Token {
	if p.pos+1 < len(p.toks) {
		return p.toks[p.pos+1]
	}
	return p.toks[len(p.toks)-1]
}

func (p *Parser) next() lexer.Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *Parser) at(kinds ...string) bool {
	k := p.cur().Kind
	for _, x := range kinds {
		if k == x {
			return true
		}
	}
	return false
}

// skipNL 跳过任意数量的换行（空白行合法）。
func (p *Parser) skipNL() {
	for p.at(lexer.NL) {
		p.next()
	}
}

// ---- 语句 ----

var blockStoppers = []string{lexer.END, lexer.ELSE, lexer.ELIF, lexer.CATCH, lexer.EOF}

// stmts 解析语句序列，直到遇到 stops 里的记号或文件结束。
func (p *Parser) stmts(stops []string) ([]Stmt, *errs.Error) {
	var out []Stmt
	for {
		p.skipNL()
		if p.at(lexer.EOF) || p.at(stops...) {
			return out, nil
		}
		s, e := p.stmt()
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
}

func inStops(kind string, stops []string) bool {
	for _, s := range stops {
		if kind == s {
			return true
		}
	}
	return false
}

// expectNL 要求换行（规范文法里的 换行）；条件/块头之后必须换行，
// 但如果紧跟着块收尾记号，就留给“块体不可为空”的检查去报更友好的错。
func (p *Parser) expectNL(ctx string) *errs.Error {
	if p.at(lexer.NL) {
		p.next()
		return nil
	}
	if p.at(blockStoppers...) {
		return nil
	}
	return errs.SyntaxHint(
		ctx+"后面要换一行，再写块里的内容。", "expected a newline after "+ctx+" before the block body.",
		"块里的语句要另起一行；sahou 靠换行分隔语句，靠 完毕 收尾。", p.cur().Line)
}

func (p *Parser) stmt() (Stmt, *errs.Error) {
	t := p.cur()
	switch t.Kind {
	case lexer.LET:
		return p.letStmt()
	case lexer.IF:
		return p.ifStmt()
	case lexer.ELIF:
		return nil, errs.SyntaxHint(
			"这里出现了 又如，但它前面没有 如果 块。", "found 又如 (elif) without a matching 如果 (if).",
			"又如 必须紧跟在一个 如果 块的 完毕 之前。", t.Line)
	case lexer.ELSE:
		return nil, errs.SyntaxHint(
			"这里出现了 否则，但它前面没有 如果 块。", "found 否则 (else) without a matching 如果 (if).",
			"否则 必须写在 如果 块的最后、完毕 之前。", t.Line)
	case lexer.END:
		return nil, errs.SyntaxHint(
			"这里多了一个 完毕，它没有可以收尾的块。", "unexpected 完毕 (end); there is no open block to close.",
			"检查一下上面的 如果/当/遍历/函数/尝试 是不是少写了或多写了。", t.Line)
	case lexer.CATCH:
		return nil, errs.SyntaxHint(
			"这里出现了 接住，但它前面没有 尝试 块。", "found 接住 (catch) without a matching 尝试 (try).",
			"接住 必须跟在一个 尝试 块后面。", t.Line)
	case lexer.WHILE:
		return p.whileStmt()
	case lexer.FOR:
		return p.forStmt()
	case lexer.FN:
		if p.peek().Kind == lexer.IDENT {
			return p.fnStmt()
		}
		return p.exprStmt() // 匿名函数表达式语句
	case lexer.RETURN:
		return p.returnStmt()
	case lexer.TRY:
		return p.tryStmt()
	case lexer.USE:
		return p.importStmt()
	case lexer.CELL:
		return p.cellStmt()
	case lexer.BREAK:
		line := p.cur().Line
		p.next()
		return &BreakStmt{Line: line}, p.consumeEndOfStmt()
	case lexer.CONTINUE:
		line := p.cur().Line
		p.next()
		return &ContinueStmt{Line: line}, p.consumeEndOfStmt()
	default:
		return p.exprStmt()
	}
}

func (p *Parser) letStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next() // let / 设
	if !p.at(lexer.IDENT) {
		return nil, errs.SyntaxHint(
			"设 后面要跟一个变量名。", "expected a variable name after let/设.",
			"例如：设 总价 = 0。", line)
	}
	target, e := p.lvalue()
	if e != nil {
		return nil, e
	}
	if !p.at(lexer.ASSIGN) {
		return nil, errs.SyntaxHint(
			"这里应该是赋值的 = 号。", "expected = for assignment.",
			"赋值写法：名字 = 值。比较相等才是两个等号 ==。", p.cur().Line)
	}
	p.next()
	val, e := p.expr()
	if e != nil {
		return nil, e
	}
	return &LetStmt{Target: target, Value: val, Line: line}, nil
}

// lvalue 解析赋值目标：名字、名字[...]、名字.字段 的链。
func (p *Parser) lvalue() (Expr, *errs.Error) {
	tok := p.next() // IDENT（调用方已检查）
	x := Expr(&Ident{Name: tok.Text, Line: tok.Line})
	for {
		switch p.cur().Kind {
		case lexer.LBRACK:
			line := p.cur().Line
			p.next()
			idx, e := p.expr()
			if e != nil {
				return nil, e
			}
			if !p.at(lexer.RBRACK) {
				return nil, p.unexpected("]")
			}
			p.next()
			x = &Index{X: x, Idx: idx, Line: line}
		case lexer.DOT:
			line := p.cur().Line
			p.next()
			if !p.at(lexer.IDENT) {
				return nil, errs.SyntaxHint(
					"点号 . 后面要跟字段名。", "expected a field name after '.'.",
					"取字典字段写 动物.名字；要按文本键取，写 动物[\"名字\"]。", line)
			}
			name := p.next()
			x = &Member{X: x, Name: name.Text, Line: line}
		default:
			return x, nil
		}
	}
}

func (p *Parser) unexpected(want string) *errs.Error {
	t := p.cur()
	got := describe(t)
	return errs.SyntaxHint(
		"这里应该是 "+want+"，但看到的是 "+got+"。", "expected "+want+" but found "+got+".",
		"检查这一行是不是写错了；中文输入法的全角符号也会导致这个错误。", t.Line)
}

func describe(t lexer.Token) string {
	switch t.Kind {
	case lexer.EOF:
		return "文件结尾"
	case lexer.NL:
		return "换行"
	case lexer.NUMBER:
		return "数字 " + t.Text
	case lexer.STRING:
		return "文本"
	case lexer.IDENT:
		return "名字 " + t.Text
	default:
		return "'" + t.Text + "'"
	}
}

func (p *Parser) ifStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	s := &IfStmt{Line: line}
	braceMode := false
	for {
		p.next() // if / 如果 或 elif / 又如
		cond, e := p.expr()
		if e != nil {
			return nil, e
		}
		body, braced, e := p.blockBody("如果")
		if e != nil {
			return nil, e
		}
		if braced {
			braceMode = true
		}
		s.Conds = append(s.Conds, cond)
		s.Blocks = append(s.Blocks, body)
		p.skipNL()
		if p.at(lexer.ELIF) {
			continue
		}
		break
	}
	if p.at(lexer.ELSE) {
		p.next()
		elseBody, braced, e := p.blockBody("否则")
		if e != nil {
			return nil, e
		}
		if braceMode && !braced {
			return nil, errs.SyntaxHint(
				"如果 用了花括号，否则 也要用花括号。",
				"the else branch must use braces when if does.",
				"两种收尾风格不要混用：要么都用 { }，要么都用 完毕。", p.cur().Line)
		}
		s.Else = elseBody
	}
	if !braceMode {
		if e := p.expectEnd("如果", line); e != nil {
			return nil, e
		}
	}
	return s, nil
}

// blockBody 解析块体，两种风格任选其一（同一块内需一致）：
//
//	花括号风格（v2 新增，Java 式）：{ 语句... }，允许空块
//	完毕风格（D1 原生）：换行 + 语句... + end/完毕，块体不可为空
//
// 返回 (语句列表, 是否用了花括号)。
func (p *Parser) blockBody(blockZh string) ([]Stmt, bool, *errs.Error) {
	if p.at(lexer.LBRACE) {
		return p.bracedBody()
	}
	// 允许 { 换行到下一行再出现（Java 风格的花括号换行写法）
	if p.at(lexer.NL) && p.peek().Kind == lexer.LBRACE {
		p.next()
		return p.bracedBody()
	}
	if e := p.expectNL(blockZh + " 的块头"); e != nil {
		return nil, false, e
	}
	body, e := p.stmts(blockStoppers)
	if e != nil {
		return nil, false, e
	}
	if len(body) == 0 {
		return nil, false, emptyBlock(p.cur())
	}
	return body, false, nil
}

func (p *Parser) bracedBody() ([]Stmt, bool, *errs.Error) {
	p.next() // {
	body, e := p.stmts([]string{lexer.RBRACE, lexer.EOF})
	if e != nil {
		return nil, true, e
	}
	if !p.at(lexer.RBRACE) {
		return nil, true, p.unexpected("}")
	}
	p.next()
	return body, true, nil
}

func emptyBlock(t lexer.Token) *errs.Error {
	return errs.SyntaxHint(
		"这个块里什么也没有。", "this block is empty.",
		"至少写一条语句，或者把整个块删掉。", t.Line)
}

// expectEnd 要求块收尾词；缺收尾时指出块从哪一行开始（规范 7.2 示例 3）。
func (p *Parser) expectEnd(blockZh string, startLine int) *errs.Error {
	if p.at(lexer.END) {
		p.next()
		return nil
	}
	t := p.cur()
	return errs.SyntaxHint(
		"第 "+itoa(startLine)+" 行的 "+blockZh+" 块没有收尾，缺少 完毕。",
		"the "+blockZh+" block starting on line "+itoa(startLine)+" is not closed; missing 完毕 (end).",
		"这个块从第 "+itoa(startLine)+" 行开始，一直开到这里还没关。补一个 完毕，或检查嵌套层数。",
		t.Line)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (p *Parser) whileStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next()
	cond, e := p.expr()
	if e != nil {
		return nil, e
	}
	body, braced, e := p.blockBody("当")
	if e != nil {
		return nil, e
	}
	if !braced {
		if e := p.expectEnd("当", line); e != nil {
			return nil, e
		}
	}
	return &WhileStmt{Cond: cond, Body: body, Line: line}, nil
}

func (p *Parser) forStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next()
	if !p.at(lexer.IDENT) {
		return nil, errs.SyntaxHint(
			"遍历 后面要跟一个循环变量名。", "expected a loop variable after for/遍历.",
			"例如：遍历 名字 于 名单。", line)
	}
	varTok := p.next()
	if !p.at(lexer.IN) {
		return nil, errs.SyntaxHint(
			"循环变量后面要写 于（in）。", "expected 于 (in) after the loop variable.",
			"例如：遍历 名字 于 名单。", p.cur().Line)
	}
	p.next()
	iter, e := p.expr()
	if e != nil {
		return nil, e
	}
	body, braced, e := p.blockBody("遍历")
	if e != nil {
		return nil, e
	}
	if !braced {
		if e := p.expectEnd("遍历", line); e != nil {
			return nil, e
		}
	}
	return &ForStmt{Var: varTok.Text, Iter: iter, Body: body, Line: line}, nil
}

func (p *Parser) fnStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next() // fn / 函数
	name := p.next()
	params, e := p.paramList()
	if e != nil {
		return nil, e
	}
	body, braced, e := p.blockBody("函数 " + name.Text)
	if e != nil {
		return nil, e
	}
	if !braced {
		if e := p.expectEnd("函数 "+name.Text, line); e != nil {
			return nil, e
		}
	}
	return &FnStmt{Name: name.Text, Params: params, Body: body, Line: line}, nil
}

func (p *Parser) paramList() ([]string, *errs.Error) {
	if !p.at(lexer.LPAREN) {
		return nil, p.unexpected("(")
	}
	p.next()
	var params []string
	seen := map[string]bool{}
	for !p.at(lexer.RPAREN) {
		if !p.at(lexer.IDENT) {
			return nil, errs.SyntaxHint(
				"参数表里要写参数名。", "expected a parameter name in the parameter list.",
				"参数只能是名字，例如：函数 加(a, b)。没有默认值（D5）。", p.cur().Line)
		}
		tok := p.next()
		if seen[tok.Text] {
			return nil, errs.SyntaxHint(
				"参数 "+tok.Text+" 重复了。", "duplicate parameter "+tok.Text+".",
				"每个参数名字要不一样。", tok.Line)
		}
		seen[tok.Text] = true
		params = append(params, tok.Text)
		if p.at(lexer.COMMA) {
			p.next()
			if p.at(lexer.RPAREN) {
				return nil, errs.SyntaxHint(
					"逗号后面没有参数。", "stray comma in the parameter list.",
					"删掉最后一个逗号。", p.cur().Line)
			}
			continue
		}
		break
	}
	if !p.at(lexer.RPAREN) {
		return nil, p.unexpected(")")
	}
	p.next()
	return params, nil
}

func (p *Parser) returnStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next()
	if p.at(lexer.NL) || p.at(blockStoppers...) {
		return &ReturnStmt{Line: line}, nil // 返回 空值
	}
	val, e := p.expr()
	if e != nil {
		return nil, e
	}
	return &ReturnStmt{Value: val, Line: line}, nil
}

// consumeEndOfStmt 跳过语句后的换行（若在括号内则无换行记号）。
func (p *Parser) consumeEndOfStmt() *errs.Error {
	p.skipNL()
	return nil
}

// cellStmt 解析：格子 名字 = 表达式（v4 响应式格子）
func (p *Parser) cellStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next() // 格子 / cell
	if !p.at(lexer.IDENT) {
		return nil, errs.SyntaxHint(
			"格子 后面要跟一个名字。", "expected a name after 格子 (cell).",
			"例如：格子 总价 = 单价 * 数量。", line)
	}
	name := p.next()
	if !p.at(lexer.ASSIGN) {
		return nil, errs.SyntaxHint(
			"这里应该是赋值的 = 号。", "expected = in cell definition.",
			"格子由表达式自动维护：格子 总价 = 单价 * 数量。", p.cur().Line)
	}
	p.next()
	val, e := p.expr()
	if e != nil {
		return nil, e
	}
	return &CellStmt{Name: name.Text, Expr: val, Line: line}, nil
}

// importStmt 解析：用 "模块名" [引入]
func (p *Parser) importStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next() // 用 / use
	if !p.at(lexer.STRING) {
		return nil, errs.SyntaxHint(
			"用 后面要跟模块名的文本，例如 用 \"网络\" 引入。",
			"expected a module name string after use/用.",
			"模块名写在引号里：用 \"工具\" 引入；引入 两个词可以省略。", line)
	}
	nameTok := p.next()
	if len(nameTok.Str) == 0 || (len(nameTok.Str) == 1 && nameTok.Str[0].Expr != "") {
		return nil, errs.SyntaxHint(
			"模块名要是一段普通文本，不能包含插值。",
			"the module name must be a plain string without interpolation.", "", line)
	}
	var name strings.Builder
	for _, part := range nameTok.Str {
		name.WriteString(part.Lit)
	}
	if p.at(lexer.IMPORT) {
		p.next()
	}
	return &ImportStmt{Name: name.String(), Line: line}, nil
}

func (p *Parser) tryStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	p.next()
	body, bodyBraced, e := p.blockBody("尝试")
	if e != nil {
		return nil, e
	}
	p.skipNL()
	if !p.at(lexer.CATCH) {
		return nil, errs.SyntaxHint(
			"尝试 块后面要写 接住 错误名。", "expected 接住 (catch) after the 尝试 (try) block.",
			"例如：尝试 { … } 接住 错误信息 { … }。", p.cur().Line)
	}
	p.next()
	if !p.at(lexer.IDENT) {
		return nil, errs.SyntaxHint(
			"接住 后面要跟一个名字，用来接住错误信息。", "expected a name after catch/接住 to hold the error message.",
			"例如：接住 错误信息。错误信息是一个文本。", p.cur().Line)
	}
	varTok := p.next()
	catchBody, catchBraced, e := p.blockBody("接住")
	if e != nil {
		return nil, e
	}
	if bodyBraced != catchBraced {
		return nil, errs.SyntaxHint(
			"尝试 和 接住 的收尾风格要一致（都用花括号或都用 完毕）。",
			"try and catch must use the same block style.",
			"两种收尾风格不要混用。", p.cur().Line)
	}
	if !bodyBraced {
		if e := p.expectEnd("尝试", line); e != nil {
			return nil, e
		}
	}
	return &TryStmt{Body: body, CatchVar: varTok.Text, Catch: catchBody, Line: line}, nil
}

func (p *Parser) exprStmt() (Stmt, *errs.Error) {
	line := p.cur().Line
	x, e := p.expr()
	if e != nil {
		return nil, e
	}
	// 表达式后面跟 = 就是赋值语句（= 不是运算符，规范 3.2）
	if p.at(lexer.ASSIGN) {
		p.next()
		val, e := p.expr()
		if e != nil {
			return nil, e
		}
		switch x.(type) {
		case *Ident, *Index, *Member:
			return &LetStmt{Target: x, Value: val, Line: line}, nil
		default:
			return nil, errs.SyntaxHint(
				"= 的左边只能是要赋值的目标：变量、列表元素或字典字段。", "the left side of = must be a variable, a list element, or a dict field.",
				"例如：总分 = 0、名单[0] = 新值、小猫.名字 = 咪咪。", line)
		}
	}
	return &ExprStmt{X: x, Line: line}, nil
}

// ---- 表达式（优先级见规范 3.2）----

func (p *Parser) expr() (Expr, *errs.Error) {
	// 匿名函数：fn ( 参数 ) => 表达式（体延伸到最右，优先级最低）
	if p.at(lexer.FN) && p.peek().Kind == lexer.LPAREN {
		line := p.cur().Line
		p.next()
		params, e := p.paramList()
		if e != nil {
			return nil, e
		}
		if !p.at(lexer.ARROW) {
			return nil, errs.SyntaxHint(
				"匿名函数的参数表后面要写 =>。", "expected => after the anonymous function's parameter list.",
				"例如：函数(x) => x * 2。", p.cur().Line)
		}
		p.next()
		body, e := p.expr()
		if e != nil {
			return nil, e
		}
		base := Expr(&AnonFn{Params: params, Body: body, Line: line})
		return p.pipeTail(base)
	}
	left, e := p.orExpr()
	if e != nil {
		return nil, e
	}
	return p.pipeTail(left)
}

// pipeTail 解析零个或多个 → 步骤，组装 Pipe 节点（v3 数据流水线）。
func (p *Parser) pipeTail(base Expr) (Expr, *errs.Error) {
	if !p.at(lexer.PIPE) {
		return base, nil
	}
	line := p.cur().Line
	steps := []Expr{}
	for p.at(lexer.PIPE) {
		p.next()
		// 步骤可以是匿名函数（函数(x) => ...），此时函数体延伸到最右
		var step Expr
		var e *errs.Error
		if p.at(lexer.FN) && p.peek().Kind == lexer.LPAREN {
			step, e = p.expr()
		} else {
			step, e = p.orExpr()
		}
		if e != nil {
			return nil, e
		}
		steps = append(steps, step)
	}
	return &Pipe{Base: base, Steps: steps, Line: line}, nil
}

func (p *Parser) orExpr() (Expr, *errs.Error) {
	l, e := p.andExpr()
	if e != nil {
		return nil, e
	}
	for p.at(lexer.OR) {
		line := p.cur().Line
		p.next()
		r, e := p.andExpr()
		if e != nil {
			return nil, e
		}
		l = &Bin{Op: "or", L: l, R: r, Line: line}
	}
	return l, nil
}

func (p *Parser) andExpr() (Expr, *errs.Error) {
	l, e := p.notExpr()
	if e != nil {
		return nil, e
	}
	for p.at(lexer.AND) {
		line := p.cur().Line
		p.next()
		r, e := p.notExpr()
		if e != nil {
			return nil, e
		}
		l = &Bin{Op: "and", L: l, R: r, Line: line}
	}
	return l, nil
}

func (p *Parser) notExpr() (Expr, *errs.Error) {
	if p.at(lexer.NOT) {
		line := p.cur().Line
		p.next()
		x, e := p.notExpr()
		if e != nil {
			return nil, e
		}
		return &Not{X: x, Line: line}, nil
	}
	return p.cmpExpr()
}

var cmpOps = map[string]bool{
	lexer.EQ: true, lexer.NEQ: true, lexer.LT: true,
	lexer.LE: true, lexer.GT: true, lexer.GE: true,
}

func (p *Parser) cmpExpr() (Expr, *errs.Error) {
	l, e := p.addExpr()
	if e != nil {
		return nil, e
	}
	if p.at(cmpKeys()...) {
		line := p.cur().Line
		op := p.next()
		r, e := p.addExpr()
		if e != nil {
			return nil, e
		}
		l = &Bin{Op: op.Kind, L: l, R: r, Line: line}
		// 比较不可连写（规范 3.2）：a < b < c 是错误并给改写建议
		if p.at(cmpKeys()...) {
			return nil, errs.SyntaxHint(
				"比较不能连写，比如 0 < 分数 < 100。", "comparisons cannot be chained, e.g. 0 < 分数 < 100.",
				"用 并且 连接：0 < 分数 并且 分数 < 100。", line)
		}
	}
	return l, nil
}

func cmpKeys() []string {
	return []string{lexer.EQ, lexer.NEQ, lexer.LT, lexer.LE, lexer.GT, lexer.GE}
}

func (p *Parser) addExpr() (Expr, *errs.Error) {
	l, e := p.mulExpr()
	if e != nil {
		return nil, e
	}
	for p.at(lexer.PLUS, lexer.MINUS) {
		op := p.next()
		r, e := p.mulExpr()
		if e != nil {
			return nil, e
		}
		l = &Bin{Op: op.Kind, L: l, R: r, Line: op.Line}
	}
	return l, nil
}

func (p *Parser) mulExpr() (Expr, *errs.Error) {
	l, e := p.unaryExpr()
	if e != nil {
		return nil, e
	}
	for p.at(lexer.STAR, lexer.SLASH, lexer.PERCENT) {
		op := p.next()
		r, e := p.unaryExpr()
		if e != nil {
			return nil, e
		}
		l = &Bin{Op: op.Kind, L: l, R: r, Line: op.Line}
	}
	return l, nil
}

func (p *Parser) unaryExpr() (Expr, *errs.Error) {
	if p.at(lexer.MINUS) {
		line := p.cur().Line
		p.next()
		x, e := p.unaryExpr()
		if e != nil {
			return nil, e
		}
		return &Un{X: x, Line: line}, nil
	}
	return p.postfixExpr()
}

func (p *Parser) postfixExpr() (Expr, *errs.Error) {
	x, e := p.primary()
	if e != nil {
		return nil, e
	}
	for {
		switch p.cur().Kind {
		case lexer.LPAREN:
			line := p.cur().Line
			p.next()
			args, e := p.args()
			if e != nil {
				return nil, e
			}
			if !p.at(lexer.RPAREN) {
				return nil, p.unexpected(")")
			}
			p.next()
			x = &Call{Fn: x, Args: args, Line: line}
		case lexer.LBRACK:
			line := p.cur().Line
			p.next()
			idx, e := p.expr()
			if e != nil {
				return nil, e
			}
			if !p.at(lexer.RBRACK) {
				return nil, p.unexpected("]")
			}
			p.next()
			x = &Index{X: x, Idx: idx, Line: line}
		case lexer.DOT:
			line := p.cur().Line
			p.next()
			if !p.at(lexer.IDENT) {
				return nil, errs.SyntaxHint(
					"点号 . 后面要跟字段名；要按文本键取值请写 [\"键\"]。", "expected a field name after '.'; use [\"key\"] for text keys.",
					"例如：动物.名字 或 动物[\"名字\"]。", line)
			}
			name := p.next()
			x = &Member{X: x, Name: name.Text, Line: line}
		default:
			return x, nil
		}
	}
}

func (p *Parser) args() ([]Arg, *errs.Error) {
	var args []Arg
	seen := map[string]bool{}
	for !p.at(lexer.RPAREN) {
		// 命名实参：名字: 值
		if p.at(lexer.IDENT) && p.peek().Kind == lexer.COLON {
			nameTok := p.next()
			p.next() // :
			val, e := p.expr()
			if e != nil {
				return nil, e
			}
			if seen[nameTok.Text] {
				return nil, errs.SyntaxHint(
					"参数 "+nameTok.Text+" 给了两次。", "argument "+nameTok.Text+" is given twice.",
					"同一个参数只能给一次。", nameTok.Line)
			}
			seen[nameTok.Text] = true
			args = append(args, Arg{Name: nameTok.Text, Value: val})
		} else {
			val, e := p.expr()
			if e != nil {
				return nil, e
			}
			args = append(args, Arg{Value: val})
		}
		if p.at(lexer.COMMA) {
			p.next()
			continue
		}
		break
	}
	return args, nil
}

func (p *Parser) primary() (Expr, *errs.Error) {
	t := p.cur()
	switch t.Kind {
	case lexer.NUMBER:
		p.next()
		rat, ok := new(big.Rat).SetString(t.Text)
		if !ok {
			return nil, errs.Syntax("看不懂的数字："+t.Text+"。", "cannot parse number "+t.Text+".", t.Line)
		}
		return &NumLit{Val: rat, Line: t.Line}, nil
	case lexer.STRING:
		p.next()
		parts := make([]StrPart, 0, len(t.Str))
		for _, sp := range t.Str {
			if sp.Expr != "" {
				sub, e := ParseExprSource(sp.Expr, t.Line)
				if e != nil {
					return nil, e
				}
				parts = append(parts, StrPart{Expr: sub})
			} else {
				parts = append(parts, StrPart{Lit: sp.Lit})
			}
		}
		return &StrLit{Parts: parts, Line: t.Line}, nil
	case lexer.TRUE:
		p.next()
		return boolLit(true, t.Line), nil
	case lexer.FALSE:
		p.next()
		return boolLit(false, t.Line), nil
	case lexer.NULL:
		p.next()
		return &NullLit{Line: t.Line}, nil
	case lexer.IDENT:
		p.next()
		return &Ident{Name: t.Text, Line: t.Line}, nil
	case lexer.LPAREN:
		p.next()
		x, e := p.expr()
		if e != nil {
			return nil, e
		}
		if !p.at(lexer.RPAREN) {
			return nil, p.unexpected(")")
		}
		p.next()
		return x, nil
	case lexer.LBRACK:
		return p.listLit()
	case lexer.LBRACE:
		return p.dictLit()
	case lexer.EOF:
		return nil, errs.SyntaxHint(
			"程序在这里就结束了，后面好像还缺了内容。", "unexpected end of file; something seems missing.",
			"检查是不是少写了 完毕、括号或引号。", t.Line)
	}
	return nil, p.unexpected("一个值或表达式")
}

func boolLit(b bool, line int) Expr {
	if b {
		return &BoolLit{Val: true, Line: line}
	}
	return &BoolLit{Val: false, Line: line}
}

type BoolLit struct {
	Val  bool
	Line int
}

func (e *BoolLit) Pos() int { return e.Line }

type NullLit struct{ Line int }

func (e *NullLit) Pos() int { return e.Line }

func (p *Parser) listLit() (Expr, *errs.Error) {
	line := p.cur().Line
	p.next() // [
	var elems []Expr
	for !p.at(lexer.RBRACK) {
		x, e := p.expr()
		if e != nil {
			return nil, e
		}
		elems = append(elems, x)
		if p.at(lexer.COMMA) {
			p.next()
			continue
		}
		break
	}
	if !p.at(lexer.RBRACK) {
		return nil, p.unexpected("]")
	}
	p.next()
	return &ListLit{Elems: elems, Line: line}, nil
}

func (p *Parser) dictLit() (Expr, *errs.Error) {
	line := p.cur().Line
	p.next()   // {
	p.skipNL() // 花括号不再抑制换行，字典内部由解析器自行跳过
	var items []DictItem
	for !p.at(lexer.RBRACE) {
		p.skipNL()
		key, e := p.expr()
		if e != nil {
			return nil, e
		}
		if !p.at(lexer.COLON) {
			return nil, errs.SyntaxHint(
				"字典的键后面要写冒号 :。", "expected : between a dict key and its value.",
				"例如：{\"名字\": \"咪咪\"}。", p.cur().Line)
		}
		p.next()
		p.skipNL()
		val, e := p.expr()
		if e != nil {
			return nil, e
		}
		items = append(items, DictItem{Key: key, Val: val})
		if p.at(lexer.COMMA) {
			p.next()
			continue
		}
		break
	}
	if !p.at(lexer.RBRACE) {
		return nil, p.unexpected("}")
	}
	p.next()
	return &DictLit{Items: items, Line: line}, nil
}

// Unused import guard
var _ = strings.TrimSpace
