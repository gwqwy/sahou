// 库内嵌：把 stones/ 标准库包与 wasm 运行时打进可执行文件。
// 发布版单文件即携带全部标准库包（`用 "包名" 引入` 免安装；`sahou 装 包名` 可取出）
// 与 卅.wasm 浏览器运行时（`sahou serve` 时磁盘没有也能按内嵌副本提供）。
//
// 注意：`sahou 打包` 生成的独立 exe 不包含本文件（临时工程只复制 internal/ 与 cmd/），
// 打包应用按提示把 stones/ 目录随 exe 一起分发即可。
package main

import (
	"embed"
	"io/fs"

	"sahou/internal/stonesrc"
)

//go:embed stones
var stonesDir embed.FS

//go:embed wasm_exec.js sahou.wasm
var wasmRuntime embed.FS

func init() {
	if sub, err := fs.Sub(stonesDir, "stones"); err == nil {
		stonesrc.FS = sub
	}
}

// embeddedWasmFile 取内嵌的 wasm 运行时文件（wasm_exec.js 或 sahou.wasm）。
func embeddedWasmFile(name string) ([]byte, bool) {
	data, err := fs.ReadFile(wasmRuntime, name)
	if err != nil {
		return nil, false
	}
	return data, true
}
