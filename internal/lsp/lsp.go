// Package lsp 提供 `sahou lsp` 子命令：stdio 上的极简语言服务器。
// 只做一件事——打开/修改 .saho 文件时推送语法诊断（词法 + 解析），
// 让 VS Code 等编辑器实时显示错误波浪线。
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
