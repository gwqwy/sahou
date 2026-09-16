// Package lsp 提供 `sahou lsp` 子命令：stdio 上的极简语言服务器。
// 只做一件事——打开/修改 .saho 文件时推送语法诊断（词法 + 解析），
// 让 VS Code 等编辑器实时显示错误波浪线。
package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"sahou/internal/errs"
	"sahou/internal/interp"
	"sahou/internal/lexer"
	"sahou/internal/parser"
)

type rpcMessage struct {
	ID      *json.Number `json:"id,omitempty"`
	Method  string       `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  interface{}  `json:"result,omitempty"`
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

// Run 启动 LSP 主循环（阻塞到客户端断开或 exit）。
func Run() {
	in := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
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
						"textDocumentSync":  1, // 全量同步
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
				publish(os.Stdout, p.TextDocument.URI, p.TextDocument.Text)
			}
		case "textDocument/didChange":
			var p didChangeParams
			if json.Unmarshal(m.Params, &p) == nil {
				text := p.TextDocument.Text
				if len(p.ContentChanges) > 0 {
					text = p.ContentChanges[len(p.ContentChanges)-1].Text
				}
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
			items := []map[string]interface{}{}
			for i, w := range interp.CompletionWords() {
				items = append(items, map[string]interface{}{
					"label": w,
					"kind":  14, // Keyword（混合表，统一按关键词给）
					"sortText": fmt.Sprintf("%04d", i),
				})
			}
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
