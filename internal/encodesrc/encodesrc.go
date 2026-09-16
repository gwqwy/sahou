// Package encodesrc 读取 sahou 源文件并归一为 UTF-8 文本。
// 支持带 BOM 的 UTF-8 / UTF-16LE / UTF-16BE；无 BOM 时校验 UTF-8，
// 不是合法 UTF-8（比如 GBK/ANSI 旧编码）则给出双语提示。
package encodesrc

import (
	"fmt"
	"os"
	"unicode/utf16"
	"unicode/utf8"
)

// ReadFile 读源文件并返回 UTF-8 文本。
func ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return Decode(data, path)
}

// Decode 把源文件字节归一为 UTF-8 文本（自动识别 BOM 与 UTF-16）。
func Decode(data []byte, path string) (string, error) {
	switch {
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		return string(data[3:]), nil // UTF-8 BOM：去掉即可
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return utf16Decode(data[2:], true), nil // UTF-16LE
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return utf16Decode(data[2:], false), nil // UTF-16BE
	}
	if utf8.Valid(data) {
		return string(data), nil
	}
	return "", fmt.Errorf(
		"文件 %s 不是 UTF-8 编码（可能是 GBK/ANSI）。请用 VS Code 打开后点右下角编码，选择\"通过编码保存\"→ UTF-8，再运行。\n"+
			"file %s is not UTF-8 encoded; re-save it as UTF-8 (VS Code: click the encoding in the status bar).",
		path, path)
}

func utf16Decode(data []byte, little bool) string {
	u := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if little {
			u = append(u, uint16(data[i])|uint16(data[i+1])<<8)
		} else {
			u = append(u, uint16(data[i])<<8|uint16(data[i+1]))
		}
	}
	return string(utf16.Decode(u))
}
