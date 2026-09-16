//go:build js && wasm

// 浏览器运行时的内嵌标准库：cmd/sahouwasm/stones/ 目录（由 tests/wasm.sh
// 在构建前从仓库根 stones/ 同步过来，embed 无法引用上级目录）。
// 注入后，浏览器里 `用 "统计库" 引入` 等七包免安装即用（与 exe 同口径）。
package main

import (
	"embed"
	"io/fs"

	"sahou/internal/stonesrc"
)

//go:embed stones
var stonesDir embed.FS

func init() {
	if sub, err := fs.Sub(stonesDir, "stones"); err == nil {
		stonesrc.FS = sub
	}
}
