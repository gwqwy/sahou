// Package errs 定义 sahou 的双语错误体系（规范 7：报错即教学）。
// 语法错误发生在运行之前，不能被 尝试/接住 捕获；
// 运行时错误可被捕获，被接住时只取中文消息（错误对象就是一句话，D8）。
package errs

import (
	"fmt"
	"strings"
)

type Kind int

const (
	KindSyntax Kind = iota
	KindRuntime
)

type Frame struct {
	Line int
	Func string
}

type Error struct {
	Kind Kind
	Zh   string
	En   string
	Hint string
	Line int
	// Exit >=0 表示"以该退出码结束程序"（系统.退出），不作为错误显示；
	// 零值必须是 -1（"这不是退出"），由构造函数统一设置。
	Exit int
}

func (e *Error) Error() string { return e.Zh }

func Syntax(zh, en string, line int) *Error {
	return &Error{Kind: KindSyntax, Zh: zh, En: en, Line: line, Exit: -1}
}

func SyntaxHint(zh, en, hint string, line int) *Error {
	return &Error{Kind: KindSyntax, Zh: zh, En: en, Hint: hint, Line: line, Exit: -1}
}

func Runtime(zh, en string, line int) *Error {
	return &Error{Kind: KindRuntime, Zh: zh, En: en, Line: line, Exit: -1}
}

func RuntimeHint(zh, en, hint string, line int) *Error {
	return &Error{Kind: KindRuntime, Zh: zh, En: en, Hint: hint, Line: line, Exit: -1}
}

// Format 输出规范 7.1 的三行式：中文行、英文行、可选提示行。
func Format(e *Error) string {
	var b strings.Builder
	locZh, locEn := "", ""
	if e.Line > 0 {
		locZh = fmt.Sprintf("第 %d 行：", e.Line)
		locEn = fmt.Sprintf("line %d: ", e.Line)
	}
	fmt.Fprintf(&b, "%s%s", locZh, e.Zh)
	fmt.Fprintf(&b, "\n%s%s", locEn, e.En)
	if e.Hint != "" {
		fmt.Fprintf(&b, "\n  提示：%s", e.Hint)
	}
	return b.String()
}

// FormatUncaught 输出未被接住的运行时错误：消息 + 调用链 + 停止语。
func FormatUncaught(e *Error, chain []Frame) string {
	var b strings.Builder
	b.WriteString(Format(e))
	for _, f := range chain {
		fmt.Fprintf(&b, "\n  调用位置：第 %d 行，在函数 %q 中", f.Line, f.Func)
		fmt.Fprintf(&b, "\n  called from: line %d, in function %q", f.Line, f.Func)
	}
	b.WriteString("\n程序已停止。program stopped.")
	return b.String()
}

// DidYouMean 返回最接近的候选名；编辑距离太远就放弃，避免给出莫名其妙的建议。
func DidYouMean(name string, candidates []string) string {
	best, bestDist := "", -1
	for _, c := range candidates {
		if c == name {
			continue
		}
		d := editDistance(name, c)
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}
	if best == "" {
		return ""
	}
	limit := 2
	if len([]rune(name)) > 4 {
		limit = 3
	}
	if bestDist <= limit {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	prev := make([]int, len(ra)+1)
	for i := range prev {
		prev[i] = i
	}
	for j, cb := range rb {
		cur := make([]int, len(ra)+1)
		cur[0] = j + 1
		for i, ca := range ra {
			cost := 1
			if ca == cb {
				cost = 0
			}
			cur[i+1] = min3(prev[i]+cost, cur[i]+1, prev[i+1]+1)
		}
		prev = cur
	}
	return prev[len(ra)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
