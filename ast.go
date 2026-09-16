// dumpProgram 以缩进文本形式打印 AST（sahou ast 命令）。
package main

import (
	"fmt"
	"strings"

	"sahou/internal/parser"
)

func dumpProgram(prog *parser.Program) {
	for _, s := range prog.Stmts {
		dumpStmt(s, 0)
	}
}

func ind(n int) string { return strings.Repeat("  ", n) }

func dumpStmt(s parser.Stmt, d int) {
	switch st := s.(type) {
	case *parser.LetStmt:
		fmt.Println(ind(d) + "let")
		dumpExpr(st.Target, d+1)
		dumpExpr(st.Value, d+1)
	case *parser.ExprStmt:
		fmt.Println(ind(d) + "expr")
		dumpExpr(st.X, d+1)
	case *parser.IfStmt:
		for i, c := range st.Conds {
			fmt.Println(ind(d) + "if-cond")
			dumpExpr(c, d+1)
			for _, b := range st.Blocks[i] {
				dumpStmt(b, d+1)
			}
		}
		if st.Else != nil {
			fmt.Println(ind(d) + "else")
			for _, b := range st.Else {
				dumpStmt(b, d+1)
			}
		}
	case *parser.WhileStmt:
		fmt.Println(ind(d) + "while")
		dumpExpr(st.Cond, d+1)
		for _, b := range st.Body {
			dumpStmt(b, d+1)
		}
	case *parser.ForStmt:
		fmt.Println(ind(d) + "for " + st.Var)
		dumpExpr(st.Iter, d+1)
		for _, b := range st.Body {
			dumpStmt(b, d+1)
		}
	case *parser.FnStmt:
		fmt.Println(ind(d) + "fn " + st.Name + " (" + strings.Join(st.Params, ", ") + ")")
		for _, b := range st.Body {
			dumpStmt(b, d+1)
		}
	case *parser.ReturnStmt:
		fmt.Println(ind(d) + "return")
		if st.Value != nil {
			dumpExpr(st.Value, d+1)
		}
	case *parser.TryStmt:
		fmt.Println(ind(d) + "try")
		for _, b := range st.Body {
			dumpStmt(b, d+1)
		}
		fmt.Println(ind(d) + "catch " + st.CatchVar)
		for _, b := range st.Catch {
			dumpStmt(b, d+1)
		}
	}
}

func dumpExpr(e parser.Expr, d int) {
	switch x := e.(type) {
	case *parser.NumLit:
		fmt.Println(ind(d) + "num " + x.Val.RatString())
	case *parser.BoolLit:
		fmt.Println(ind(d) + fmt.Sprintf("bool %v", x.Val))
	case *parser.NullLit:
		fmt.Println(ind(d) + "null")
	case *parser.StrLit:
		parts := make([]string, len(x.Parts))
		for i, p := range x.Parts {
			if p.Expr != nil {
				parts[i] = "{expr}"
			} else {
				parts[i] = p.Lit
			}
		}
		fmt.Println(ind(d) + "str " + strings.Join(parts, "|"))
	case *parser.Ident:
		fmt.Println(ind(d) + "id " + x.Name)
	case *parser.ListLit:
		fmt.Println(ind(d) + "list")
		for _, el := range x.Elems {
			dumpExpr(el, d+1)
		}
	case *parser.DictLit:
		fmt.Println(ind(d) + "dict")
		for _, it := range x.Items {
			dumpExpr(it.Key, d+1)
			dumpExpr(it.Val, d+1)
		}
	case *parser.Bin:
		fmt.Println(ind(d) + "bin " + x.Op)
		dumpExpr(x.L, d+1)
		dumpExpr(x.R, d+1)
	case *parser.Un:
		fmt.Println(ind(d) + "neg")
		dumpExpr(x.X, d+1)
	case *parser.Not:
		fmt.Println(ind(d) + "not")
		dumpExpr(x.X, d+1)
	case *parser.Index:
		fmt.Println(ind(d) + "index")
		dumpExpr(x.X, d+1)
		dumpExpr(x.Idx, d+1)
	case *parser.Member:
		fmt.Println(ind(d) + "member " + x.Name)
		dumpExpr(x.X, d+1)
	case *parser.Call:
		fmt.Println(ind(d) + "call")
		dumpExpr(x.Fn, d+1)
		for _, a := range x.Args {
			if a.Name != "" {
				fmt.Println(ind(d+1) + "arg " + a.Name)
			} else {
				fmt.Println(ind(d+1) + "arg")
			}
			dumpExpr(a.Value, d+2)
		}
	case *parser.AnonFn:
		fmt.Println(ind(d) + "anon (" + strings.Join(x.Params, ", ") + ")")
		dumpExpr(x.Body, d+1)
	}
}
