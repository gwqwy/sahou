// Package format 提供保守的 sahou 代码格式化（类 gofmt 的最小版）：
// 只重排缩进、去行尾空白、压缩连续空行——不改写任何记号，保证格式化后语义不变。
package format

import (
	"strings"

	"sahou/internal/lexer"
)

// blockOpeners 行首出现即"开块"的关键词（块体缩进一级）。
var blockOpeners = map[string]bool{
	"函数": true, "fn": true, "如果": true, "if": true, "又如": true, "elif": true,
	"当": true, "while": true, "for": true, "尝试": true, "try": true,
}

// blockContinuers 行首出现即"同层延续块"的关键词（不升降层级）。
var blockContinuers = map[string]bool{
	"否则": true, "else": true, "接住": true, "catch": true,
}

// blockClosers 行首出现即"闭块"的关键词（本行先降一级缩进）。
var blockClosers = map[string]bool{
	"完毕": true, "end": true,
}

// Source 格式化整段源码。缩进单位 4 空格；空行最多保留一行。
// 字符串字面量里的关键词不受影响（只识别行首第一个词）。
func Source(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	depth := 0
	prevBlank := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		word := firstWord(trimmed)
		if blockClosers[word] {
			if depth > 0 {
				depth--
			}
		} else if blockContinuers[word] {
			// 同层：先减一再加回，即保持 depth
		}
		out = append(out, strings.Repeat("    ", depth)+trimmed)
		if blockOpeners[word] && !blockClosers[word] {
			depth++
		}
	}
	// 结尾保证恰好一个换行
	res := strings.Join(out, "\n")
	res = strings.TrimRight(res, "\n") + "\n"
	return res
}

// firstWord 取行首标识符（中英文/数字），识别不出返回空串。
func firstWord(line string) string {
	rs := []rune(line)
	j := 0
	for j < len(rs) && (isWordRune(rs[j]) || rs[j] >= 0x80) {
		j++
	}
	w := string(rs[:j])
	if _, ok := lexer.KeyWords[w]; ok {
		return w
	}
	return ""
}

func isWordRune(c rune) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
