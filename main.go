// sahou（卅）命令行入口。
//
//	sahou run 程序.saho   运行一个程序
//	sahou tokens 程序.saho  打印记号流（调试用）
//	sahou ast 程序.saho    打印语法树（调试用）
//	sahou                进入交互环境（REPL）
package main

import (
	"bufio"
	"net/http"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sahou/internal/encodesrc"
	"sahou/internal/errs"
	"sahou/internal/interp"
	"sahou/internal/lsp"
	"sahou/internal/lexer"
	"sahou/internal/parser"
	"sahou/internal/transpile"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		repl()
		return
	}
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "run":
		if len(rest) < 1 {
			usage()
		}
		runFile(rest[0], rest[1:])
	case "tokens", "ast":
		if len(rest) != 1 {
			usage()
		}
		dump(cmd, rest[0])
	case "build":
		if len(rest) < 1 {
			usage()
		}
		buildJS(rest[0], flagValue(rest, "-o"))
	case "version", "版本":
		fmt.Println("sahou（卅）4.3.0 —— v4 响应式计算模型 + wasm 直接运行 + 全栈与应用（表单/会话/数据库/桌面/手机端）")
		fmt.Println("23 个关键字 · 30 个内置函数 · 10 个标准库模块")
	case "装", "install":
		stonesInstall(rest)
	case "serve":
		serveDir(rest)
	case "lsp":
		lsp.Run()
	case "help", "-h", "--help":
		usage()
	default:
		// 允许省略 run：sahou xxx.saho [参数...]
		if strings.HasSuffix(cmd, ".saho") {
			runFile(cmd, rest)
			return
		}
		usage()
	}
}

func usage() {
	fmt.Print(`sahou（卅）— 比 Python 更简单一点的入门语言

用法:
  sahou run 程序.saho      运行程序
  sahou build 页面.saho -o 页面.js  转译成 JavaScript（浏览器运行）
  sahou tokens 程序.saho   查看记号流
  sahou ast 程序.saho      查看语法树
  sahou 装 <本地路径>      安装一个包（stones/ 目录 + stones.yml 清单）
  sahou 装                 校验 stones.yml 里的包是否齐全
  sahou serve [目录]       起本地静态服务（默认 8000 端口，跑 wasm 网页用）
  sahou lsp                语言服务（编辑器实时诊断，stdio）
  sahou                    交互环境
`)
	os.Exit(0)
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// buildJS 转译：sahou build 页面.saho -o 页面.js
func buildJS(inPath, outPath string) {
	src, rerr := encodesrc.ReadFile(inPath)
	if rerr != nil {
		fmt.Println(rerr.Error())
		os.Exit(2)
	}
	toks, e := lexer.Tokenize(string(src))
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	js, e := transpile.Build(prog, filepath.Dir(inPath))
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	if outPath == "" {
		outPath = strings.TrimSuffix(inPath, ".saho") + ".js"
	}
	if werr := os.WriteFile(outPath, []byte(js), 0o644); werr != nil {
		fmt.Printf("写不进 %s：%v\n", outPath, werr)
		os.Exit(2)
	}
	fmt.Printf("已生成 %s（%d 字节）。\n", outPath, len(js))
}

type compiler struct{ prog *parser.Program }

func compileFile(path string) (*compiler, *errs.Error) {
	src, rerr := encodesrc.ReadFile(path)
	if rerr != nil {
		return nil, errs.Syntax(rerr.Error(), rerr.Error(), 0)
	}
	toks, e := lexer.Tokenize(string(src))
	if e != nil {
		return nil, e
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		return nil, e
	}
	return &compiler{prog: prog}, nil
}

func runFile(path string, extraArgs []string) {
	c, e := compileFile(path)
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	in := interp.New()
	if abs, err := filepath.Abs(filepath.Dir(path)); err == nil {
		in.ScriptDir = abs
	}
	std := bufio.NewReaderSize(os.Stdin, 1<<16)
	in.Input = func(prompt string) string {
		fmt.Print(prompt)
		line, _ := std.ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}
	in.ProgramArgs = extraArgs
	if e := in.Run(c.prog); e != nil {
		fmt.Fprintln(os.Stderr, errs.FormatUncaught(e, chainOf(in)))
		os.Exit(1)
	}
	if code := in.ExitCode; code >= 0 {
		os.Exit(code) // 系统.退出(码)
	}
}

// chainOf 读取求值器记录的调用链。
func chainOf(in *interp.Interp) []errs.Frame { return in.Chain() }

func dump(cmd, path string) {
	src, rerr := encodesrc.ReadFile(path)
	if rerr != nil {
		fmt.Println(rerr.Error())
		os.Exit(2)
	}
	_ = rerr
	toks, e := lexer.Tokenize(string(src))
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	if cmd == "tokens" {
		for _, t := range toks {
			text := t.Text
			if text == "" && t.Kind == lexer.STRING {
				text = fmt.Sprint(t.Str)
			}
			fmt.Printf("%4d  %-8s %s\n", t.Line, t.Kind, text)
		}
		return
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	dumpProgram(prog)
}

func repl() {
	fmt.Println("sahou（卅）交互环境 — 输入完一行语句会立刻执行；写函数/如果 等块时按回车继续输入，单独一行 完毕 结束块。退出请按 Ctrl+C。")
	in := interp.New()
	in.ScriptDir, _ = os.Getwd()
	std := bufio.NewReaderSize(os.Stdin, 1<<16)
	in.Input = func(prompt string) string {
		fmt.Print(prompt)
		line, _ := std.ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}
	buf := bufio.NewReader(os.Stdin)
	var pending []string
	for {
		if len(pending) == 0 {
			fmt.Print("sahou> ")
		} else {
			fmt.Print("  ...> ")
		}
		line, err := buf.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		line = strings.TrimRight(line, "\r\n")
		pending = append(pending, line)
		src := strings.Join(pending, "\n")
		toks, e := lexer.Tokenize(src)
		if e != nil {
			if isIncomplete(e) {
				continue
			}
			fmt.Println(errs.Format(e))
			pending = nil
			continue
		}
		prog, e := parser.Parse(toks)
		if e != nil {
			if isIncomplete(e) {
				continue
			}
			fmt.Println(errs.Format(e))
			pending = nil
			continue
		}
		pending = nil
		if e := in.Run(prog); e != nil {
			fmt.Println(errs.FormatUncaught(e, in.Chain()))
		} else if in.HasLastValue && in.LastValue != nil {
			fmt.Println("= " + interp.Str(in.LastValue))
		}
	}
}

// isIncomplete 判断错误是不是"输入还没写完"（REPL 继续等待下一行）。
func isIncomplete(e *errs.Error) bool {
	if e.Zh == "" {
		return false
	}
	return strings.Contains(e.Zh, "程序在这里就结束了") ||
		strings.Contains(e.Zh, "没有收尾") ||
		strings.Contains(e.Zh, "缺少 完毕") ||
		strings.Contains(e.Zh, "后面要换一行") ||
		strings.Contains(e.Zh, "这个块里什么也没有")
}
// ---------- v2 stones 包管理（08 文档 M7）----------

// stonesInstall `sahou 装 <本地路径>`：把包复制进 stones/ 并登记 stones.yml；
// 无参数时校验 stones.yml 里每个包都已在本地。
func stonesInstall(args []string) {
	if len(args) == 0 {
		verifyStones()
		return
	}
	src := args[0]
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		fmt.Println("远程仓库 v2 后期才会提供；现在请给一个本地路径（包目录或单个 .saho 文件）。")
		fmt.Println("remote registries are not available yet; pass a local path.")
		os.Exit(2)
	}
	st, err := os.Stat(src)
	if err != nil {
		fmt.Printf("找不到要安装的包：%s。\n", src)
		os.Exit(2)
	}
	name := strings.TrimSuffix(filepath.Base(src), ".saho")
	if err := os.MkdirAll("stones", 0o755); err != nil {
		fmt.Printf("建不了 stones 目录：%v\n", err)
		os.Exit(2)
	}
	if st.IsDir() {
		dst := filepath.Join("stones", name)
		if err := os.RemoveAll(dst); err != nil {
			fmt.Printf("清理旧包失败：%v\n", err)
			os.Exit(2)
		}
		if err := copyDir(src, dst); err != nil {
			fmt.Printf("复制包失败：%v\n", err)
			os.Exit(2)
		}
	} else {
		data, rerr := os.ReadFile(src)
		if rerr != nil {
			fmt.Printf("读不了包文件：%v\n", rerr)
			os.Exit(2)
		}
		if werr := os.WriteFile(filepath.Join("stones", name+".saho"), data, 0o644); werr != nil {
			fmt.Printf("写不了包文件：%v\n", werr)
			os.Exit(2)
		}
	}
	addStonesYML(name)
	fmt.Printf("已安装包 %s 到 stones/，现在可以 用 \"%s\" 引入。\n", name, name)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// stonesYMLPackages 极简行式解析：`包:` 段下缩进的 `名: 版本`。
func stonesYMLPackages() map[string]string {
	packages := map[string]string{}
	data, err := os.ReadFile("stones.yml")
	if err != nil {
		return packages
	}
	inPackages := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(trimmed, ":") {
			inPackages = trimmed == "包:"
			continue
		}
		if inPackages && strings.Contains(trimmed, ":") {
			idx := strings.Index(trimmed, ":")
			name := strings.TrimSpace(trimmed[:idx])
			ver := strings.TrimSpace(trimmed[idx+1:])
			if name != "" {
				packages[name] = ver
			}
		}
	}
	return packages
}

func addStonesYML(name string) {
	packages := stonesYMLPackages()
	packages[name] = "local"
	var sb strings.Builder
	sb.WriteString("包:\n")
	for n, v := range packages {
		sb.WriteString(fmt.Sprintf("  %s: %s\n", n, v))
	}
	_ = os.WriteFile("stones.yml", []byte(sb.String()), 0o644)
}

func verifyStones() {
	packages := stonesYMLPackages()
	if len(packages) == 0 {
		fmt.Println("stones.yml 里还没有登记任何包。安装：sahou 装 <本地路径>")
		return
	}
	missing := 0
	for name := range packages {
		okDir := false
		if _, err := os.Stat(filepath.Join("stones", name)); err == nil {
			okDir = true
		}
		if _, err := os.Stat(filepath.Join("stones", name+".saho")); err == nil {
			okDir = true
		}
		if okDir {
			fmt.Printf("√ %s\n", name)
		} else {
			missing++
			fmt.Printf("× %s 缺失，请重新运行 sahou 装 <本地路径>\n", name)
		}
	}
	if missing > 0 {
		os.Exit(1)
	}
}

// serveDir 起一个静态文件服务（跑 sahou.wasm 网页用）。
func serveDir(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	addr := "127.0.0.1:8000"
	for _, a := range args {
		if strings.Contains(a, ":") {
			addr = strings.TrimPrefix(a, "http://")
		}
	}
	handler := http.FileServer(http.Dir(dir))
	fmt.Printf("静态服务已启动：http://%s （目录 %s，Ctrl+C 停止）\n", addr, dir)
	if err := http.ListenAndServe(addr, handler); err != nil {
		fmt.Println("启动失败：", err)
		os.Exit(1)
	}
}
