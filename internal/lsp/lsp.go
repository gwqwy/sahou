// Package lsp 提供 `sahou lsp` 子命令：stdio 上的极简语言服务器。
// 诊断（打开/修改 .saho 时推送词法+解析错误）、上下文补全、悬停文档。
// 悬停的说明数据来自 editors/shared/language.json（main 包注入 SharedJSON；
// 未注入时悬停退化为空，诊断与补全不受影响）。
package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
