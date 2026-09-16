// Package lexer 把 sahou 源码切成记号（规范 2）。
// 缩进只是排版（D1）：空格/Tab 永远被忽略；换行只在括号深度为 0 时产生 NL 记号。
// 双语关键字在词法阶段归一化为同一个记号（doc04 §3.3）：解析器永远只见英文规范记号。
package lexer

import (
	"strings"
	"unicode"

	"sahou/internal/errs"
)

// 记号类别（英文规范拼写；中文关键字映射到同一个类别）。
const (
	EOF      = "EOF"
	NL       = "NL"
	IDENT    = "IDENT"
	NUMBER   = "NUMBER"
	STRING   = "STRING"
	IF       = "IF"       // if / 如果
	ELIF     = "ELIF"     // elif / 又如
	ELSE     = "ELSE"     // else / 否则
	WHILE    = "WHILE"    // while / 当
	FOR      = "FOR"      // for / 遍历
	IN       = "IN"       // in / 于
	END      = "END"      // end / 完毕
	FN       = "FN"       // fn / 函数
	RETURN   = "RETURN"   // return / 返回
	LET      = "LET"      // let / 设
	TRUE     = "TRUE"     // true / 真
	FALSE    = "FALSE"    // false / 假
	NULL     = "NULL"     // null / 空值
	AND      = "AND"      // and / 并且
	OR       = "OR"       // or / 或者
	NOT      = "NOT"      // not / 非
	TRY      = "TRY"      // try / 尝试
	CATCH    = "CATCH"    // catch / 接住
	PIPE     = "PIPE"     // -> （推荐）或全角 → 别名（v3 数据流水线）
	CELL     = "CELL"     // cell / 格子（v4 响应式计算模型）
	BREAK    = "BREAK"    // break / 跳出（v4.1 循环控制）
	CONTINUE = "CONTINUE" // continue / 继续（v4.1 循环控制）
	USE      = "USE"      // use / 用（v2 模块引入）
	IMPORT   = "IMPORT"   // import / 引入（v2，可省略的收尾词）
	// 运算符与分隔符
	PLUS, MINUS, STAR, SLASH, PERCENT = "+", "-", "*", "/", "%"
	EQ, NEQ, LT, LE, GT, GE           = "==", "!=", "<", "<=", ">", ">="
	ASSIGN, ARROW                     = "=", "=>"
	LPAREN, RPAREN                    = "(", ")"
	LBRACK, RBRACK                    = "[", "]"
	LBRACE, RBRACE                    = "{", "}"
	COMMA, COLON, DOT                 = ",", ":", "."
)

// KeyWords 双语关键字的唯一事实来源：中英拼写 → 同一记号（18 个关键字）。
var KeyWords = map[string]string{
	"if": IF, "如果": IF,
	"elif": ELIF, "又如": ELIF,
	"else": ELSE, "否则": ELSE,
	"while": WHILE, "当": WHILE,
	"for": FOR, "遍历": FOR,
	"in": IN, "于": IN,
	"end": END, "完毕": END,
	"fn": FN, "函数": FN,
	"return": RETURN, "返回": RETURN,
	"let": LET, "设": LET,
	"true": TRUE, "真": TRUE,
	"false": FALSE, "假": FALSE,
	"null": NULL, "空值": NULL,
	"and": AND, "并且": AND,
	"or": OR, "或者": OR,
	"not": NOT, "非": NOT,
	"try": TRY, "尝试": TRY,
	"catch": CATCH, "接住": CATCH,
	"cell": CELL, "格子": CELL,
	"break": BREAK, "跳出": BREAK,
	"continue": CONTINUE, "继续": CONTINUE,
	"use": USE, "用": USE,
	"import": IMPORT, "引入": IMPORT,
}

// StrPart 字面量的一部分：Lit 非空是文字；Expr 非空是插值表达式的源码。
type StrPart struct {
	Lit  string
	Expr string
	Col  int // 插值表达式在源码行内的列（报错定位用）
}

type Token struct {
	Kind string
	Text string // 原文（数字文本、名字原文；报错回显用）
	Str  []StrPart
	Line int
}

// fullwidth 全角 → 半角等价物（v2 起：全角在词法阶段自动按半角处理，不再报错）。
var fullwidth = map[rune]rune{
	'（': '(', '）': ')', '【': '[', '】': ']', '［': '[', '］': ']',
	'，': ',', '：': ':', '。': '.', '、': ',',
	'“': '"', '”': '"', '‘': '\'', '’': '\'',
	'！': '!', '？': '?', '｜': '|',
	'＜': '<', '＞': '>', '＝': '=', '＋': '+', '－': '-', '＊': '*', '／': '/', '％': '%',
	'｛': '{', '｝': '}', '；': ';',
	'　': ' ',
}

type Lexer struct {
	runes []rune
	pos   int
	line  int
	depth int // ( [ { 的嵌套深度；深度 > 0 时换行不产生 NL
	toks  []Token
}

// Tokenize 切分整个源码；词法错误以 *errs.Error（Kind=Syntax）返回。
func Tokenize(src string) ([]Token, *errs.Error) {
	lx := &Lexer{runes: []rune(src), line: 1}
	if e := lx.run(); e != nil {
		return nil, e
	}
	return lx.toks, nil
}

func (lx *Lexer) run() *errs.Error {
	for {
		lx.skipSpaces()
		if lx.pos >= len(lx.runes) {
			break
		}
		r := lx.runes[lx.pos]
		switch {
		case r == '\n':
			lx.pos++
			if lx.depth == 0 {
				lx.emit(NL, "\n")
			}
			lx.line++
		case r == '#':
			for lx.pos < len(lx.runes) && lx.runes[lx.pos] != '\n' {
				lx.pos++
			}
		case unicode.IsDigit(r):
			if e := lx.lexNumber(); e != nil {
				return e
			}
		case isIdentStart(r):
			lx.lexWord()
		case r == '"' || r == '\'' || r == '“' || r == '‘':
			// 全角引号（“ ” ‘ ’）也是合法的字符串引号（中文输入法默认输出）
			closer := r
			if r == '“' {
				closer = '”'
			}
			if r == '‘' {
				closer = '’'
			}
			if e := lx.lexString(closer); e != nil {
				return e
			}
		case r == '→':
			lx.emit(PIPE, "→")
			lx.pos++
		default:
			if e := lx.lexOp(); e != nil {
				return e
			}
		}
	}
	lx.emit(EOF, "")
	return nil
}

func (lx *Lexer) emit(kind, text string) {
	lx.toks = append(lx.toks, Token{Kind: kind, Text: text, Line: lx.line})
}

func (lx *Lexer) skipSpaces() {
	for lx.pos < len(lx.runes) {
		r := lx.runes[lx.pos]
		if r == ' ' || r == '\t' || r == '\r' {
			lx.pos++
		} else {
			break
		}
	}
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func (lx *Lexer) lexNumber() *errs.Error {
	start := lx.pos
	for lx.pos < len(lx.runes) && unicode.IsDigit(lx.runes[lx.pos]) {
		lx.pos++
	}
	// 小数点前后都必须有数字（规范 2.7）：.5 与 5. 都是错误
	if lx.pos < len(lx.runes) && lx.runes[lx.pos] == '.' {
		if lx.pos+1 < len(lx.runes) && unicode.IsDigit(lx.runes[lx.pos+1]) {
			lx.pos++
			for lx.pos < len(lx.runes) && unicode.IsDigit(lx.runes[lx.pos]) {
				lx.pos++
			}
		} else {
			lx.pos++ // 吃掉孤立的点，报错时回显
			return errs.SyntaxHint(
				"数字 "+string(lx.runes[start:lx.pos])+" 的小数点后面没有数字。",
				"a number cannot end with a bare decimal point.",
				"写成 5.0 或 5，不要写 5.；也不要写 .5，要写 0.5。", lx.line)
		}
	}
	if lx.pos < len(lx.runes) && isIdentStart(lx.runes[lx.pos]) {
		bad := string(lx.runes[start:])
		return errs.SyntaxHint("数字后面不能直接跟字母："+bad+"。", "a number cannot be followed directly by a letter.", "名字和数字之间加一个空格，或者检查是不是写错了。", lx.line)
	}
	lx.emit(NUMBER, string(lx.runes[start:lx.pos]))
	return nil
}

func (lx *Lexer) lexWord() {
	start := lx.pos
	for lx.pos < len(lx.runes) && isIdentPart(lx.runes[lx.pos]) {
		lx.pos++
	}
	word := string(lx.runes[start:lx.pos])
	if kind, ok := KeyWords[word]; ok {
		lx.emit(kind, word)
	} else {
		lx.emit(IDENT, word)
	}
}

const validEscapes = `合法转义只有这几种：\n 换行、\t 制表符、\\ 反斜杠、\" 双引号、\' 单引号、\{ 与 \} 字面花括号。`

func (lx *Lexer) lexString(quote rune) *errs.Error {
	line := lx.line
	lx.pos++ // 跳过开引号
	var parts []StrPart
	var lit strings.Builder
	for {
		if lx.pos >= len(lx.runes) {
			return errs.SyntaxHint("这个文本没有收尾的 "+string(quote)+" 号。", "this string is not closed; missing "+string(quote)+".",
				"文本不能跨行，要换行请写 \\n；检查是不是漏打了收尾引号。", line)
		}
		r := lx.runes[lx.pos]
		switch {
		case r == quote:
			lx.pos++
			lx.flushStr(&parts, &lit)
			lx.toks = append(lx.toks, Token{Kind: STRING, Str: parts, Line: line})
			return nil
		case r == '\n':
			return errs.SyntaxHint("文本里不能直接按回车换行。", "a string literal cannot span multiple lines.",
				"要换行请写 \\n，并给文本补上收尾引号 "+string(quote)+"。", line)
		case r == '\\':
			if lx.pos+1 >= len(lx.runes) {
				return errs.Syntax("孤立的反斜杠 \\。", "a lone backslash is not a valid escape.", line)
			}
			esc := lx.runes[lx.pos+1]
			switch esc {
			case 'n':
				lit.WriteRune('\n')
			case 't':
				lit.WriteRune('\t')
			case '\\', '"', '\'', '{', '}':
				lit.WriteRune(esc)
			default:
				return errs.SyntaxHint(
					"不认识的转义 \\"+string(esc)+"。", "unknown escape \\"+string(esc)+".", validEscapes, line)
			}
			lx.pos += 2
		case r == '{':
			if lx.pos+1 < len(lx.runes) && lx.runes[lx.pos+1] == '{' {
				return errs.SyntaxHint("出现了连续两个 {{。", "found {{ inside a string.",
					"要显示一个字面的 { ，请写 \\{；要插入值，请写 {表达式}。", line)
			}
			lx.flushStr(&parts, &lit)
			col := lx.pos
			src, e := lx.scanInterp(quote)
			if e != nil {
				return e
			}
			parts = append(parts, StrPart{Expr: src, Col: col})
		case r == '}':
			lit.WriteRune('}') // 单个 } 直接当字面内容（宽容处理）
			lx.pos++
		default:
			lit.WriteRune(r)
			lx.pos++
		}
	}
}

func (lx *Lexer) flushStr(parts *[]StrPart, lit *strings.Builder) {
	if lit.Len() > 0 {
		*parts = append(*parts, StrPart{Lit: lit.String()})
		lit.Reset()
	}
}

// scanInterp 捕获 { 与配对 } 之间的表达式源码；内部允许字符串（含花括号）与字典字面量。
func (lx *Lexer) scanInterp(quote rune) (string, *errs.Error) {
	start := lx.pos + 1
	lx.pos++ // 越过 {
	depth := 1
	var inStr rune
	for lx.pos < len(lx.runes) {
		r := lx.runes[lx.pos]
		if inStr != 0 {
			if r == '\\' {
				lx.pos += 2
				continue
			}
			if r == inStr {
				inStr = 0
			}
			lx.pos++
			continue
		}
		switch {
		case r == '"' || r == '\'':
			inStr = r
		case r == '{':
			depth++
		case r == '}':
			depth--
			if depth == 0 {
				src := strings.TrimSpace(string(lx.runes[start:lx.pos]))
				lx.pos++
				if src == "" {
					return "", errs.SyntaxHint("插值 { } 里没有写表达式。", "the interpolation { } is empty.",
						"花括号里放任意表达式，例如 {名字} 或 {单价 * 数量}。", lx.line)
				}
				return src, nil
			}
		case r == '\n':
			return "", errs.SyntaxHint("插值 { } 没有在本行内收尾。", "the interpolation { } is not closed on this line.",
				"插值必须写在一对花括号里，检查是不是漏了 }。", lx.line)
		}
		lx.pos++
	}
	return "", errs.SyntaxHint("文本里的 { 没有配对的 }。", "a { inside the string has no matching }.",
		"补上配对的 }；要显示字面的 { ，请写 \\{。", lx.line)
}

func (lx *Lexer) lexOp() *errs.Error {
	// 全角归一化：当前字符与下一字符都先转成半角等价物再做匹配
	norm := func(r rune) rune {
		if half, ok := fullwidth[r]; ok {
			return half
		}
		return r
	}
	r := norm(lx.runes[lx.pos])
	next := rune(0)
	if lx.pos+1 < len(lx.runes) {
		next = norm(lx.runes[lx.pos+1])
	}
	two := string([]rune{r, next})
	switch two {
	case "==", "!=", "<=", ">=", "=>":
		lx.emit(two, two)
		lx.pos += 2
		return nil
	case "->":
		lx.emit(PIPE, "->")
		lx.pos += 2
		return nil
	}
	switch r {
	case ';':
		// 分号是可选的语句结束符（等价换行）；括号/花括号内忽略
		if lx.depth == 0 {
			lx.emit(NL, ";")
		}
		lx.pos++
		return nil
	case '!':
		return errs.SyntaxHint("sahou 里不用叹号表示否定。", "sahou does not use '!' for negation.",
			"逻辑非写 非（或 not）。", lx.line)
	case '?':
		return errs.SyntaxHint("sahou 里没有问号运算符。", "sahou has no '?' operator.",
			"三目运算请用 如果/否则 语句。", lx.line)
	}
	switch r {
	case '+':
		lx.emit(PLUS, "+")
	case '-':
		lx.emit(MINUS, "-")
	case '*':
		lx.emit(STAR, "*")
	case '/':
		lx.emit(SLASH, "/")
	case '%':
		lx.emit(PERCENT, "%")
	case '<':
		lx.emit(LT, "<")
	case '>':
		lx.emit(GT, ">")
	case '=':
		lx.emit(ASSIGN, "=")
	case '(':
		lx.depth++
		lx.emit(LPAREN, "(")
	case ')':
		lx.closeBracket()
		lx.emit(RPAREN, ")")
	case '[':
		lx.depth++
		lx.emit(LBRACK, "[")
	case ']':
		lx.closeBracket()
		lx.emit(RBRACK, "]")
	case '{':
		// 花括号不抑制换行：块体（v2 花括号风格）内的语句仍靠换行分隔
		lx.emit(LBRACE, "{")
	case '}':
		lx.emit(RBRACE, "}")
	case ',':
		lx.emit(COMMA, ",")
	case ':':
		lx.emit(COLON, ":")
	case '.':
		lx.emit(DOT, ".")
	default:
		return errs.SyntaxHint(
			"看不懂的字符："+string(lx.runes[lx.pos])+"。", "unexpected character "+string(lx.runes[lx.pos])+".",
			"检查这里是不是打错了，或者混入了中文输入法的符号。", lx.line)
	}
	lx.pos++
	return nil
}

func (lx *Lexer) closeBracket() {
	if lx.depth > 0 {
		lx.depth--
	}
}
