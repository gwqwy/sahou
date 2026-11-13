// Package lsp 提供 `sahou lsp` 子命令：stdio 上的极简语言服务器。
// 诊断（打开/修改 .saho 时推送词法+解析错误）、上下文补全、悬停文档。
// 悬停的说明数据来自 editors/shared/language.json（main 包注入 SharedJSON；
// 未注入时悬停退化为空，诊断与补全不受影响）。
package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"sahou/internal/errs"
	"sahou/internal/interp"
	"sahou/internal/lexer"
	"sahou/internal/parser"
	"sahou/internal/stonesrc"
)

// SharedJSON 编辑器共享元数据（editors/shared/language.json 的内容）；
// 由命令行入口注入，供悬停使用。
var SharedJSON string

// sharedLang SharedJSON 的解析结果。
type sharedLang struct {
	Keywords []struct {
		Zh, En, Brief string
	}
	Builtins []struct {
		Zh, En, Signature, Brief, Side string
	}
	Modules []struct {
		Zh, En, Brief string
		Members       []string
	}
	Stones []struct {
		Name, Brief string
		Members     []string
	}
}

var shared *sharedLang

func parseShared() {
	shared = &sharedLang{}
	if SharedJSON == "" {
		return
	}
	_ = json.Unmarshal([]byte(SharedJSON), shared)
}

type rpcMessage struct {
	ID     *json.Number    `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result interface{}     `json:"result,omitempty"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type rangeT struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type diagnostic struct {
	Range    rangeT `json:"range"`
	Severity int    `json:"severity"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   textDocumentItem `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type publishParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

type completionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position position `json:"position"`
}

type codeActionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range   rangeT `json:"range"`
	Context struct {
		Diagnostics []diagnostic `json:"diagnostics"`
	} `json:"context"`
}

type stoneSourceParams struct {
	Name string `json:"name"`
}

// completionItems 按光标前缀给补全：
//
//	`用 "前缀`        -> stones 包名（内嵌 + 本地）
//	`名字.前缀`       -> 内置模块成员 或 stones 包的顶层名字
//	其余              -> 关键字 + 内置函数（原有混合表）
func completionItems(text string, pos position) []map[string]interface{} {
	items := []map[string]interface{}{}
	add := func(names []string, kind int) {
		n := 0
		for _, w := range names {
			if w == "" {
				continue
			}
			items = append(items, map[string]interface{}{
				"label":    w,
				"kind":     kind,
				"sortText": fmt.Sprintf("%04d", n),
			})
			n++
		}
	}
	prefix := linePrefix(text, pos)
	// 用 "xxx  ->  包名
	if strings.Contains(prefix, `"`) && strings.HasSuffix(strings.TrimSpace(prefix), `"`) == false {
		if idx := strings.LastIndex(prefix, `"`); idx >= 0 {
			head := strings.TrimSpace(prefix[:idx])
			if strings.HasSuffix(head, "用") || strings.HasSuffix(head, "use") {
				add(packageNames(), 9) // Module
				return items
			}
		}
	}
	// 模块.成员 / 包名.成员
	if dot := strings.LastIndex(prefix, "."); dot >= 0 {
		tail := prefix[dot+1:]
		head := prefix[:dot]
		if head != "" && !strings.ContainsAny(head, " \t(){}[],!=<>+-*/%") {
			pkg := strings.TrimSpace(head)
			var members []string
			if m, ok := interp.CompletionMembers()[pkg]; ok {
				members = m
			} else {
				members = interp.CompletionStoneMembers(pkg)
			}
			if len(members) > 0 {
				add(filterByPrefix(members, tail), 3) // Function/Field 混合
				return items
			}
			// 不是模块/包名的 xxx. —— 当作某个值（列表/字典）来给成员方法建议
			add(filterByPrefix(interp.ValueMemberNames(), tail), 2) // Method
			return items
		}
	}
	words := []string{}
	words = append(words, interp.CompletionWords()...)
	add(words, 14) // Keyword（混合表，统一按关键词给）
	return items
}

// linePrefix 取光标位置之前的当前行文本。
// LSP 的 character 以 UTF-16 单位计（一个汉字算 1），先换算成字节偏移。
func linePrefix(text string, pos position) string {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	units := 0
	for i, r := range line {
		if units >= pos.Character {
			return line[:i]
		}
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return line
}

// filterByPrefix 按已输入的前缀过滤（大小写不敏感）。
func filterByPrefix(names []string, prefix string) []string {
	if prefix == "" {
		return names
	}
	p := strings.ToLower(prefix)
	var out []string
	for _, n := range names {
		if strings.HasPrefix(strings.ToLower(n), p) {
			out = append(out, n)
		}
	}
	return out
}

// packageNames 可引入的包名：exe 内嵌包 + 从当前目录向上找到的本地 stones 包。
func packageNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range stonesrc.Names() {
		add(n)
	}
	dir, err := os.Getwd()
	if err == nil {
		for {
			entries, err2 := os.ReadDir(filepath.Join(dir, "stones"))
			if err2 == nil {
				for _, e := range entries {
					if e.IsDir() {
						add(e.Name())
					} else if strings.HasSuffix(e.Name(), ".saho") {
						add(strings.TrimSuffix(e.Name(), ".saho"))
					}
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return out
}

// Run 启动 LSP 主循环（阻塞到客户端断开或 exit）。
func Run() {
	parseShared()
	in := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	docs := map[string]string{} // uri -> 全文（补全上下文用）
	for {
		msg, err := readFrame(os.Stdin, &in, buf)
		if err != nil {
			return
		}
		var m rpcMessage
		if err := json.Unmarshal(msg, &m); err != nil {
			continue
		}
		switch m.Method {
		case "initialize":
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      m.ID,
				"result": map[string]interface{}{
					"capabilities": map[string]interface{}{
						"textDocumentSync": 1, // 全量同步
						"completionProvider": map[string]interface{}{
							"triggerCharacters": []string{".", " "},
						},
<<<<<<< HEAD
						"definitionProvider": true,
						"renameProvider":     true,
=======
						"hoverProvider":      true,
						"definitionProvider": true,
						"codeActionProvider": true,
>>>>>>> 4095b0b28d0b955de4de9cc9c382d16c5db3d432
					},
					"serverInfo": map[string]interface{}{
						"name": "sahou-lsp",
					},
				},
			}))
		case "initialized":
			// 无需处理
		case "textDocument/didOpen":
			var p didOpenParams
			if json.Unmarshal(m.Params, &p) == nil {
				docs[p.TextDocument.URI] = p.TextDocument.Text
				publish(os.Stdout, p.TextDocument.URI, p.TextDocument.Text)
			}
		case "textDocument/didChange":
			var p didChangeParams
			if json.Unmarshal(m.Params, &p) == nil {
				text := p.TextDocument.Text
				if len(p.ContentChanges) > 0 {
					text = p.ContentChanges[len(p.ContentChanges)-1].Text
				}
				docs[p.TextDocument.URI] = text
				publish(os.Stdout, p.TextDocument.URI, text)
			}
		case "textDocument/didClose":
			var p didOpenParams
			if json.Unmarshal(m.Params, &p) == nil {
				writeMessage(os.Stdout, mustJSON(map[string]interface{}{
					"jsonrpc": "2.0",
					"method":  "textDocument/publishDiagnostics",
					"params":  publishParams{URI: p.TextDocument.URI, Diagnostics: []diagnostic{}},
				}))
			}
		case "textDocument/completion":
			var cp completionParams
			_ = json.Unmarshal(m.Params, &cp)
			items := completionItems(docs[cp.TextDocument.URI], cp.Position)
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      m.ID,
				"result":  map[string]interface{}{"isIncomplete": false, "items": items},
			}))
		case "textDocument/hover":
			var hp completionParams
			_ = json.Unmarshal(m.Params, &hp)
			md := hoverMarkdown(docs[hp.TextDocument.URI], hp.Position)
			var result interface{}
			if md != "" {
				result = map[string]interface{}{
					"contents": map[string]interface{}{"kind": "markdown", "value": md},
				}
			}
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      m.ID,
				"result":  result,
			}))
		case "textDocument/definition":
			var dp completionParams
			_ = json.Unmarshal(m.Params, &dp)
<<<<<<< HEAD
			uri := dp.TextDocument.URI
			var result interface{}
			if _, loc, ok := definitionTarget(docs[uri], dp.Position); ok {
				result = location{URI: uri, Range: loc}
			}
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": m.ID, "result": result,
			}))
		case "textDocument/rename":
			var rp renameParams
			_ = json.Unmarshal(m.Params, &rp)
			uri := rp.TextDocument.URI
			var result interface{}
			if _, edits, ok := renamePlan(docs[uri], rp.Position, rp.NewName); ok {
				result = map[string]interface{}{
					"changes": map[string]interface{}{uri: edits},
				}
			}
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": m.ID, "result": result,
=======
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      m.ID,
				"result":  definitionLocation(docs[dp.TextDocument.URI], dp.TextDocument.URI, dp.Position),
			}))
		case "textDocument/codeAction":
			var ap codeActionParams
			_ = json.Unmarshal(m.Params, &ap)
			actions := codeActions(docs[ap.TextDocument.URI], ap.TextDocument.URI, ap.Range.Start.Line, ap.Context.Diagnostics)
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      m.ID,
				"result":  actions,
			}))
		case "sahou/stoneSource":
			var sp stoneSourceParams
			_ = json.Unmarshal(m.Params, &sp)
			var result interface{}
			if src, ok := stonesrc.Source(sp.Name); ok {
				result = map[string]interface{}{"source": src}
			}
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      m.ID,
				"result":  result,
>>>>>>> 4095b0b28d0b955de4de9cc9c382d16c5db3d432
			}))
		case "shutdown":
			writeMessage(os.Stdout, mustJSON(map[string]interface{}{
				"jsonrpc": "2.0", "id": m.ID, "result": nil,
			}))
		case "exit":
			return
		}
	}
}

// diagnostics 词法 + 解析全文，产出诊断（0 起始行号）。
func diagnostics(text string) []diagnostic {
	var out []diagnostic
	toks, e := lexer.Tokenize(text)
	if e != nil {
		out = append(out, toDiag(e))
		return out
	}
	if _, e := parser.Parse(toks); e != nil {
		out = append(out, toDiag(e))
	}
	return out
}

func toDiag(e *errs.Error) diagnostic {
	line := 0
	if e.Line > 0 {
		line = e.Line - 1
	}
	msg := e.Zh
	if e.En != "" {
		msg += "\n" + e.En
	}
	if e.Hint != "" {
		msg += "\n提示：" + e.Hint
	}
	r := rangeT{Start: position{line, 0}, End: position{line, 1 << 20}}
	return diagnostic{Range: r, Severity: 1, Source: "sahou", Message: msg}
}

func publish(w io.Writer, uri, text string) {
	writeMessage(w, mustJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params":  publishParams{URI: uri, Diagnostics: diagnostics(text)},
	}))
}

// ---------- 悬停 ----------

// wordAt 取位置上的完整单词（UTF-16 位置换算，字母/数字/下划线）。
func wordAt(text string, pos position) string {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	units, byteIdx := 0, len(line)
	for i, r := range line {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > pos.Character {
			byteIdx = i
			break
		}
		units += w
		byteIdx = i
	}
	isWord := func(r rune) bool {
		return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
	}
	runes := []rune(line)
	idx := len([]rune(line[:byteIdx]))
	start, end := idx, idx
	for start > 0 && isWord(runes[start-1]) {
		start--
	}
	for end < len(runes) && isWord(runes[end]) {
		end++
	}
	return string(runes[start:end])
}

// hoverMarkdown 光标所在名字的悬停说明；查不到返回空串。
func hoverMarkdown(text string, pos position) string {
	if shared == nil {
		return ""
	}
	word := wordAt(text, pos)
	if word == "" {
		return ""
	}
	for _, b := range shared.Builtins {
		if word == b.Zh || word == b.En {
			return "**sahou 内置函数** `" + b.Signature + "`\n\n" + b.Brief +
				"\n\n（英文写法：" + b.En + "；" + b.Side + "可用）"
		}
	}
	for _, m := range shared.Modules {
		if word == m.Zh || word == m.En {
			members := strings.Join(m.Members, "、")
			if members == "" {
				members = "输入 . 后按上下文自动补全"
			}
			return "**sahou 标准库模块** " + m.Brief + "\n\n常见成员：" + members
		}
		if containsString(m.Members, word) {
			return "**sahou 标准库模块 " + m.Zh + "/" + m.En + "** 的成员：" + word + "\n\n" + m.Brief
		}
	}
	for _, s := range shared.Stones {
		if word == s.Name {
			members := strings.Join(dynamicStoneMembers(s.Name), "、")
			return "**sahou stones 标准库包** " + s.Brief +
				"\n\n用 `用 \"" + s.Name + "\" 引入` 后以 " + s.Name + ".成员 使用。\n\n顶层成员：" + members
		}
		if containsString(s.Members, word) {
			return "**stones 包 " + s.Name + "** 的成员：" + word + "\n\n" + s.Brief
		}
	}
	for _, k := range shared.Keywords {
		if word == k.Zh {
			return "**sahou 关键字**（英文 " + k.En + "）\n\n" + k.Brief
		}
		if word == k.En {
			return "**sahou keyword**（中文 " + k.Zh + "）\n\n" + k.Brief
		}
	}
	if word == "它" {
		return "**管道占位符** `它`\n\n在 `值 -> 步骤 -> 步骤` 里指代流经当前步骤的值，" +
			"例如 `成绩 -> 它 >= 60 -> 打印`。全角 `→` 与 `->` 等价。"
	}
	return ""
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// dynamicStoneMembers stones 包成员（内嵌/本地包动态解析，保证与实际内容一致）。
func dynamicStoneMembers(pkg string) []string {
	if members := interp.CompletionStoneMembers(pkg); len(members) > 0 {
		return members
	}
	// 元数据兜底（包不在本机时）
	for _, s := range shared.Stones {
		if s.Name == pkg {
			return s.Members
		}
	}
	return nil
}

<<<<<<< HEAD
// ---------- 跳转定义 / 重命名 ----------

type location struct {
	URI   string `json:"uri"`
	Range rangeT `json:"range"`
}

type renameParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position position `json:"position"`
	NewName  string   `json:"newName"`
}

type textEdit struct {
	Range   rangeT `json:"range"`
	NewText string `json:"newText"`
}

func isWordRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') || r > 127
}

// utf16Of 统计前 n 个 rune 的 UTF-16 单位数（LSP character 口径）。
func utf16Of(runes []rune) int {
	units := 0
	for _, r := range runes {
=======
// ---------- 跳转定义与代码动作 ----------

// locationMap 一个跳转目标：uri + 整行范围。
func locationMap(uri string, line0 int) map[string]interface{} {
	r := rangeT{Start: position{line0, 0}, End: position{line0, 1 << 20}}
	return map[string]interface{}{"uri": uri, "range": r}
}

// definitionLocation 光标名字的跳转目标：
//  1. 当前文档顶层函数（函数定义语句）；
//  2. `用 "名" 引入` 的模块：本地磁盘文件优先（向上查找规则与解释器 loadModule 一致），
//     磁盘没有而 exe 内嵌有该 stones 包时返回 sahou-stone:// 虚拟文档
//     （内容由 sahou/stoneSource 提供，客户端注册内容提供者打开）。
//
// 查不到返回 nil。
func definitionLocation(text, uri string, pos position) map[string]interface{} {
	word := wordAt(text, pos)
	if word == "" {
		return nil
	}
	// 词法级扫描而不是完整解析：文件有语法错误（写了一半）时跳转照常可用。
	toks, _ := lexer.Tokenize(text)
	for i, t := range toks {
		if t.Kind == lexer.FN && i+1 < len(toks) && toks[i+1].Kind == lexer.IDENT && toks[i+1].Text == word {
			return locationMap(uri, toks[i+1].Line-1)
		}
	}
	for i, t := range toks {
		if t.Kind == lexer.USE && i+1 < len(toks) && toks[i+1].Kind == lexer.STRING && firstStrLit(toks[i+1]) == word {
			if p := moduleFilePath(moduleSearchDir(uri), word); p != "" {
				return locationMap(pathToURI(p), 0)
			}
			if stonesrc.Has(word) {
				return locationMap("sahou-stone:///"+word+"/main.saho", 0)
			}
		}
	}
	return nil
}

// firstStrLit 取字符串记号里第一段字面文本（插值表达式不算）；没有则返回空串。
func firstStrLit(tok lexer.Token) string {
	for _, p := range tok.Str {
		if p.Expr == "" {
			return p.Lit
		}
	}
	return ""
}

// moduleSearchDir 找模块文件的起点目录：文档所在目录；非 file URI 时退回进程工作目录。
func moduleSearchDir(uri string) string {
	if p := uriToPath(uri); p != "" {
		return filepath.Dir(p)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// moduleFilePath 从 dir 逐级向上找模块文件，候选顺序与解释器 loadModule 一致
// （名.saho / 名/main.saho / stones/名.saho / stones/名/main.saho）；找不到返回空串。
func moduleFilePath(dir, name string) string {
	for {
		candidates := []string{
			filepath.Join(dir, name+".saho"),
			filepath.Join(dir, name, "main.saho"),
			filepath.Join(dir, "stones", name+".saho"),
			filepath.Join(dir, "stones", name, "main.saho"),
		}
		for _, c := range candidates {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				abs, _ := filepath.Abs(c)
				return abs
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if strings.HasPrefix(p, "/") && len(p) > 2 && p[2] == ':' { // windows 盘符：/E:/...
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

func pathToURI(p string) string {
	abs, _ := filepath.Abs(p)
	return "file:///" + strings.TrimPrefix(filepath.ToSlash(abs), "/")
}

// codeActions 光标行的快速修复：
//   - 诊断消息含「点号」的点号赋值 → 改用方括号（D11：点读只读）；
//   - 行内代码有全角标点（字符串字面量里的不动，避免改变输出）→ 一键换半角。
func codeActions(text, uri string, line int, diags []diagnostic) []map[string]interface{} {
	out := []map[string]interface{}{}
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return out
	}
	lineText := lines[line]
	dotDiag := false
	for _, d := range diags {
		if strings.Contains(d.Message, "点号") {
			dotDiag = true
			break
		}
	}
	if dotDiag {
		if act := dotAssignAction(uri, lineText, line); act != nil {
			out = append(out, act)
		}
	}
	if act := fullwidthAction(uri, lineText, line); act != nil {
		out = append(out, act)
	}
	return out
}

// utf16Offsets 行内每个字符（rune）起始的 UTF-16 偏移，末尾多一个行尾偏移。
// LSP 的 character 以 UTF-16 单位计，一个汉字算 1。
func utf16Offsets(line string) []int {
	offs := make([]int, 0, len([]rune(line))+1)
	units := 0
	for _, r := range line {
		offs = append(offs, units)
>>>>>>> 4095b0b28d0b955de4de9cc9c382d16c5db3d432
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
<<<<<<< HEAD
	return units
}

type defItem struct {
	name string
	loc  rangeT
}

// stripLineComment 去掉行内注释（# 之后，引号里的 # 不算）。
func stripLineComment(line string) string {
	inQuote := false
	for i, r := range line {
		if r == '"' {
			inQuote = !inQuote
		}
		if r == '#' && !inQuote {
			return line[:i]
		}
	}
	return line
}

// firstWord 取去掉首尾空白后的第一个完整单词（中文/字母/数字/下划线）。
func firstWord(s string) string {
	rs := []rune(strings.TrimSpace(s))
	i := 0
	for i < len(rs) && !isWordRune(rs[i]) {
		i++
	}
	start := i
	for i < len(rs) && isWordRune(rs[i]) {
		i++
	}
	return string(rs[start:i])
}

// isPlainWord 整串都是单词字符（用于判断赋值左值是一个单纯的名字）。
func isPlainWord(s string) bool {
=======
	offs = append(offs, units)
	return offs
}

// fullwidthAction 把这一行代码里的全角标点换成半角；字符串字面量里的一律不动。
func fullwidthAction(uri, lineText string, line int) map[string]interface{} {
	fw := lexer.FullwidthMap()
	offs := utf16Offsets(lineText)
	runes := []rune(lineText)
	var edits []map[string]interface{}
	inStr := false
	for i, r := range runes {
		if r == '"' && (i == 0 || runes[i-1] != '\\') {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		half, ok := fw[r]
		if !ok {
			continue
		}
		edits = append(edits, map[string]interface{}{
			"range":   rangeT{Start: position{line, offs[i]}, End: position{line, offs[i+1]}},
			"newText": string(half),
		})
	}
	if len(edits) == 0 {
		return nil
	}
	return map[string]interface{}{
		"title": "把这一行的全角标点换成半角",
		"kind":  "quickfix",
		"edit":  map[string]interface{}{"changes": map[string]interface{}{uri: edits}},
	}
}

// dotAssignAction 把 字典.字段 = 值 改写成 字典["字段"] = 值（点号赋值是编译期错误，D11）。
func dotAssignAction(uri, lineText string, line int) map[string]interface{} {
	runes := []rune(lineText)
	eq := -1
	inStr := false
	for i, r := range runes {
		if r == '"' && (i == 0 || runes[i-1] != '\\') {
			inStr = !inStr
			continue
		}
		if !inStr && r == '=' {
			if i+1 < len(runes) && runes[i+1] == '=' { // == 是比较，不是赋值
				continue
			}
			eq = i
			break
		}
	}
	if eq <= 0 {
		return nil
	}
	trimmed := strings.TrimRight(string(runes[:eq]), " \t")
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 {
		return nil
	}
	for _, p := range parts {
		if !isIdentWord(strings.TrimSpace(p)) {
			return nil
		}
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(parts[0]))
	for _, p := range parts[1:] {
		b.WriteString("[\"" + strings.TrimSpace(p) + "\"]")
	}
	lead := len(trimmed) - len(strings.TrimLeft(trimmed, " \t"))
	offs := utf16Offsets(lineText)
	return map[string]interface{}{
		"title": "点号只能读取——赋值改用方括号",
		"kind":  "quickfix",
		"edit": map[string]interface{}{"changes": map[string]interface{}{uri: []map[string]interface{}{{
			"range":   rangeT{Start: position{line, offs[lead]}, End: position{line, offs[eq]}},
			"newText": b.String(),
		}}}},
	}
}

// isIdentWord 是否为合法标识符文本（字母/数字/下划线，含中文）。
func isIdentWord(s string) bool {
>>>>>>> 4095b0b28d0b955de4de9cc9c382d16c5db3d432
	if s == "" {
		return false
	}
	for _, r := range s {
<<<<<<< HEAD
		if !isWordRune(r) {
=======
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
>>>>>>> 4095b0b28d0b955de4de9cc9c382d16c5db3d432
			return false
		}
	}
	return true
}

<<<<<<< HEAD
// findAssign 找行内「赋值等号」的字节下标（排除 == != >= <= 与箭头 ->）；找不到返回 -1。
func findAssign(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] != '=' {
			continue
		}
		if i+1 < len(line) && line[i+1] == '=' {
			i++
			continue
		}
		if i > 0 {
			switch line[i-1] {
			case '=', '!', '>', '<', '-':
				continue
			}
		}
		return i
	}
	return -1
}

// definitionsInFile 扫描全文的用户级定义（v1 文本级、同文件、不区分作用域）：
//
//	函数/func 名字(   → 函数定义
//	设 名字 为        → 变量定义
//	名字 =            → 赋值定义
//
// 返回按行序的定义（列为 UTF-16 单位，0 起始行号）。
func definitionsInFile(text string) []defItem {
	var out []defItem
	lines := strings.Split(text, "\n")
	for li, raw := range lines {
		line := stripLineComment(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		name := ""
		switch {
		case strings.HasPrefix(trimmed, "函数 "), strings.HasPrefix(trimmed, "func "):
			prefix := "函数 "
			if strings.HasPrefix(trimmed, "func ") {
				prefix = "func "
			}
			name = firstWord(strings.TrimPrefix(trimmed, prefix))
		case strings.HasPrefix(trimmed, "设 "):
			name = firstWord(strings.TrimPrefix(trimmed, "设 "))
		default:
			if eq := findAssign(line); eq >= 0 {
				left := strings.TrimSpace(line[:eq])
				if isPlainWord(left) {
					name = left
				}
			}
		}
		if name == "" || !isPlainWord(name) {
			continue
		}
		// 定义名列的 UTF-16 起点：在该行里找 name 的首次整词出现
		col := utf16Of([]rune(line[:strings.Index(line, name)]))
		out = append(out, defItem{name, rangeT{
			Start: position{li, col}, End: position{li, col + utf16Of([]rune(name))}}})
	}
	return out
}

// wordRangeAt2 取位置上的完整单词及其范围（UTF-16 列，0 起始行）。
func wordRangeAt2(text string, pos position) (string, rangeT, bool) {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return "", rangeT{}, false
	}
	runes := []rune(lines[pos.Line])
	// UTF-16 位置 → rune 下标
	idx := len(runes)
	units := 0
	for i, r := range runes {
		if units >= pos.Character {
			idx = i
			break
		}
		units += 1
		if r >= 0x10000 {
			units++
		}
	}
	start, end := idx, idx
	for start > 0 && isWordRune(runes[start-1]) {
		start--
	}
	for end < len(runes) && isWordRune(runes[end]) {
		end++
	}
	if start == end {
		return "", rangeT{}, false
	}
	loc := rangeT{Start: position{pos.Line, utf16Of(runes[:start])},
		End: position{pos.Line, utf16Of(runes[:end])}}
	return string(runes[start:end]), loc, true
}

// definitionTarget 光标所在名字及其定义位置：优先取光标之前最近的一个定义，
// 之前没有则取第一个（观感上支持「先调用后定义」）。查不到返回 false。
func definitionTarget(text string, pos position) (string, rangeT, bool) {
	name, _, ok := wordRangeAt2(text, pos)
	if !ok {
		return "", rangeT{}, false
	}
	var first, before rangeT
	haveFirst, haveBefore := false, false
	for _, d := range definitionsInFile(text) {
		if d.name != name {
			continue
		}
		if !haveFirst {
			first, haveFirst = d.loc, true
		}
		if d.loc.Start.Line <= pos.Line {
			before, haveBefore = d.loc, true
		}
	}
	if haveBefore {
		return name, before, true
	}
	if haveFirst {
		return name, first, true
	}
	return "", rangeT{}, false
}

// renamePlan 生成重命名计划：定义位置 + 该名字在全文所有整词出现处的编辑列表
// （与定义查找同一口径：同文件、不区分作用域）。
func renamePlan(text string, pos position, newName string) (rangeT, []textEdit, bool) {
	name, defRange, ok := definitionTarget(text, pos)
	if !ok || newName == "" || newName == name || !isPlainWord(newName) {
		return rangeT{}, nil, false
	}
	edits := []textEdit{}
	lines := strings.Split(text, "\n")
	for li, line := range lines {
		runes := []rune(line)
		i := 0
		for i < len(runes) {
			if !isWordRune(runes[i]) {
				i++
				continue
			}
			start := i
			for i < len(runes) && isWordRune(runes[i]) {
				i++
			}
			if string(runes[start:i]) == name {
				edits = append(edits, textEdit{
					Range:   rangeT{Start: position{li, utf16Of(runes[:start])}, End: position{li, utf16Of(runes[:i])}},
					NewText: newName,
				})
			}
		}
	}
	if len(edits) == 0 {
		return rangeT{}, nil, false
	}
	return defRange, edits, true
}

=======
>>>>>>> 4095b0b28d0b955de4de9cc9c382d16c5db3d432
// ---------- JSON-RPC 帧 ----------

func mustJSON(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func writeMessage(w io.Writer, body []byte) {
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body))
	w.Write(body)
}

// readFrame 读取一帧（Content-Length 头 + JSON 体）；carry 保存跨读取的残留字节。
func readFrame(r io.Reader, carry *[]byte, buf []byte) ([]byte, error) {
	for {
		if data, ok := extractFrame(carry); ok {
			return data, nil
		}
		n, err := r.Read(buf)
		if err != nil {
			return nil, err
		}
		*carry = append(*carry, buf[:n]...)
	}
}

func extractFrame(carry *[]byte) ([]byte, bool) {
	data := *carry
	headEnd := strings.Index(string(data), "\r\n\r\n")
	if headEnd < 0 {
		return nil, false
	}
	header := string(data[:headEnd])
	length := -1
	for _, line := range strings.Split(header, "\r\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			fmt.Sscanf(strings.TrimPrefix(strings.ToLower(line), "content-length:"), "%d", &length)
		}
	}
	if length < 0 || headEnd+4+length > len(data) {
		return nil, false
	}
	body := data[headEnd+4 : headEnd+4+length]
	*carry = data[headEnd+4+length:]
	return body, true
}
