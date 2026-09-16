// 卅命令行入口。
//
//	sahou run 程序.saho   运行一个程序
//	sahou tokens 程序.saho  打印记号流（调试用）
//	sahou ast 程序.saho    打印语法树（调试用）
//	卅                进入交互环境（REPL）
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sahou/internal/encodesrc"
	"sahou/internal/errs"
	"sahou/internal/format"
	"sahou/internal/interp"
	"sahou/internal/lexer"
	"sahou/internal/lsp"
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
	case "打包", "pack":
		if len(rest) < 1 {
			usage()
		}
		packApp(rest[0], flagValue(rest, "-o"))
	case "格式", "fmt":
		if len(rest) < 1 {
			usage()
		}
		formatFile(rest[0])
	case "version", "版本":
		fmt.Println("卅 v0.1 —— 全栈 + 应用 + 工程化（数据库/会话/原生窗口/打包/测试/LSP 补全）")
		fmt.Println("23 个关键字 · 30 个内置函数 · 11 个标准库模块")
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
	fmt.Print(`卅— 比 Python 更简单一点的入门语言

用法:
  sahou run 程序.saho      运行程序
  sahou build 页面.saho -o 页面.js  转译成 JavaScript（浏览器运行）
  sahou tokens 程序.saho   查看记号流
  sahou ast 程序.saho      查看语法树
  sahou 装 <本地路径|网址.zip>  安装一个包（stones/ 目录 + stones.yml 清单；网址需直连 zip）
  sahou 装                 校验 stones.yml 里的包是否齐全
  sahou serve [目录]       起本地静态服务（默认 8000 端口，跑 wasm 网页用）
  sahou lsp                语言服务（编辑器实时诊断+补全，stdio）
  sahou 格式 程序.saho     格式化（重排缩进，就地保存）
  sahou 打包 应用.saho -o 应用.exe  生成独立可执行文件（内嵌脚本，资产目录随 exe 分发）
  卅                    交互环境
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

// packApp sahou 打包 应用.saho -o 应用.exe：生成独立可执行文件。
// 做法：临时 Go 工程内嵌 .saho 脚本（go:embed），replace 指向本仓库源码，go build 出单文件 exe。
// exe 启动时把脚本目录设为 exe 所在目录——静态资产（应用页面/、stones/ 等）随 exe 一起分发即可。
func packApp(scriptPath, outPath string) {
	if outPath == "" {
		outPath = strings.TrimSuffix(filepath.Base(scriptPath), ".saho") + ".exe"
	}
	repoRoot := findRepoRoot()
	if repoRoot == "" {
		fmt.Println("找不到 卅 源码根目录（要有 go.mod）；请在仓库内运行 卅。")
		os.Exit(2)
	}
	srcData, rerr := encodesrc.ReadFile(scriptPath)
	if rerr != nil {
		fmt.Println(rerr.Error())
		os.Exit(2)
	}
	tmp, err := os.MkdirTemp("", "卅-pack-*")
	if err != nil {
		fmt.Println("建不了临时目录：", err)
		os.Exit(2)
	}
	defer os.RemoveAll(tmp)
	if werr := os.WriteFile(filepath.Join(tmp, "main.saho"), []byte(srcData), 0o644); werr != nil {
		fmt.Println("写不了临时脚本：", werr)
		os.Exit(2)
	}
	mainGo := `package main

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sahou/internal/errs"
	"sahou/internal/interp"
	"sahou/internal/lexer"
	"sahou/internal/parser"
)

//go:embed main.saho
var src string

func main() {
	toks, e := lexer.Tokenize(src)
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		fmt.Println(errs.Format(e))
		os.Exit(2)
	}
	exe := os.Args[0]
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	in := interp.New()
	in.ScriptDir = filepath.Dir(exe) // 资产目录按 exe 所在目录算
	std := bufio.NewReader(os.Stdin)
	in.Input = func(prompt string) string {
		fmt.Print(prompt)
		line, _ := std.ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}
	if e := in.Run(prog); e != nil {
		fmt.Fprintln(os.Stderr, errs.FormatUncaught(e, in.Chain()))
		os.Exit(1)
	}
	if code := in.ExitCode; code >= 0 {
		os.Exit(code)
	}
}
`
	if werr := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(mainGo), 0o644); werr != nil {
		fmt.Println("写不了 main.go：", werr)
		os.Exit(2)
	}
	// internal 包不允许跨模块引用：把 卅 源码复制进临时工程，同一模块内打包
	for _, dir := range []string{"internal", "cmd"} {
		if err := copyDir(dir, filepath.Join(tmp, dir)); err != nil {
			fmt.Println("复制源码失败：", err)
			os.Exit(2)
		}
	}
	// go.mod/go.sum：沿用本仓库的依赖（只换模块名），保证离线可构建
	gomodData, _ := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	gomodStr := strings.Replace(string(gomodData), "module 卅", "module 卅app", 1)
	gomodStr = gomodStr + "\n"
	if werr := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(gomodStr), 0o644); werr != nil {
		fmt.Println("写不了 go.mod：", werr)
		os.Exit(2)
	}
	if gosum, _ := os.ReadFile(filepath.Join(repoRoot, "go.sum")); gosum != nil {
		_ = os.WriteFile(filepath.Join(tmp, "go.sum"), gosum, 0o644)
	}
	// 同一模块内引用：所有源码的 卅/internal 前缀改成 卅app/internal
	mainGo = strings.ReplaceAll(mainGo, "\"sahou/internal/", "\"卅app/internal/")
	_ = os.WriteFile(filepath.Join(tmp, "main.go"), []byte(mainGo), 0o644)
	_ = filepath.WalkDir(tmp, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		fixed := strings.ReplaceAll(string(data), "\"sahou/internal/", "\"卅app/internal/")
		if fixed != string(data) {
			_ = os.WriteFile(path, []byte(fixed), 0o644)
		}
		return nil
	})
	// 仓库根的 main.go 会与生成的主程序冲突：临时工程里只留 internal
	_ = os.Remove(filepath.Join(tmp, "main_root.go"))
	if abs, err := filepath.Abs(outPath); err == nil {
		outPath = abs // go build 在临时目录里跑，-o 必须给绝对路径
	}
	fmt.Printf("正在编译 %s …\n", outPath)
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("go mod tidy 失败：%v\n%s\n", err, out)
		os.Exit(2)
	}
	cmd = exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", outPath)
	cmd.Dir = tmp
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("go build 失败：", err)
		os.Exit(2)
	}
	absOut, _ := filepath.Abs(outPath)
	fmt.Printf("已打包 %s。\n分发时把脚本用到的资产目录（如 应用页面/、stones/、*.db）放在 exe 旁边即可。\n", absOut)
}

// findRepoRoot 从当前目录向上找 卅 源码根（含 go.mod 且 module 名为 卅）。
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		data, rerr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if rerr == nil && strings.Contains(string(data), "module 卅") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// formatFile sahou 格式 文件.saho：保守格式化（只重排缩进），就地保存。
func formatFile(path string) {
	src, rerr := encodesrc.ReadFile(path)
	if rerr != nil {
		fmt.Println(rerr.Error())
		os.Exit(2)
	}
	out := format.Source(string(src))
	if werr := os.WriteFile(path, []byte(out), 0o644); werr != nil {
		fmt.Printf("写不进 %s：%v\n", path, werr)
		os.Exit(2)
	}
	fmt.Printf("已格式化 %s（%d 字节）。\n", path, len(out))
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
	fmt.Println("卅交互环境 — 输入完一行语句会立刻执行；写函数/如果 等块时按回车继续输入，单独一行 完毕 结束块。退出请按 Ctrl+C。")
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
			fmt.Print("卅> ")
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
		installRemoteZip(src)
		return
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

// installRemoteZip `sahou 装 https://…/包名.zip`：下载 zip 并解压进 stones/包名。
// zip 里可以有一个顶层目录（常见于压缩文件夹），会自动剥掉。
func installRemoteZip(url string) {
	name := strings.TrimSuffix(filepath.Base(url), ".zip")
	if name == "" || name == "." {
		fmt.Println("从网址上看不出包名；请让文件名是 包名.zip。")
		os.Exit(2)
	}
	fmt.Printf("正在下载 %s …\n", url)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("下载失败：%v\n", err)
		os.Exit(2)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Printf("下载失败：服务器返回 %d。\n", resp.StatusCode)
		os.Exit(2)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("下载失败：%v\n", err)
		os.Exit(2)
	}
	zipr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fmt.Println("这不是一个有效的 zip 文件。")
		os.Exit(2)
	}
	// 剥掉可选的顶层目录
	prefix := ""
	for _, f := range zipr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) == 2 && parts[0] != "" {
			prefix = parts[0] + "/"
		}
		break
	}
	dst := filepath.Join("stones", name)
	if err := os.RemoveAll(dst); err == nil || os.IsNotExist(err) {
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		fmt.Printf("建不了 stones 目录：%v\n", err)
		os.Exit(2)
	}
	n := 0
	for _, f := range zipr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, _ := io.ReadAll(rc)
		rc.Close()
		target := filepath.Join(dst, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		if werr := os.WriteFile(target, content, 0o644); werr != nil {
			fmt.Printf("写不了 %s：%v\n", target, werr)
			os.Exit(2)
		}
		n++
	}
	if n == 0 {
		fmt.Println("zip 里没有文件。")
		os.Exit(2)
	}
	addStonesYML(name)
	fmt.Printf("已安装包 %s（%d 个文件）到 stones/，现在可以 用 \"%s\" 引入。\n", name, n, name)
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
		fmt.Println("stones.yml 里还没有登记任何包。安装：sahou 装 <本地路径或网址.zip>")
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

// serveDir 起一个静态文件服务（跑 卅.wasm 网页用）。
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
