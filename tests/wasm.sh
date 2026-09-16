#!/bin/bash
# v4 WASM 直接运行回归测试：构建 sahou.wasm 并在 node 里验证响应式格子。
cd "$(dirname "$0")/.." || exit 1
GO=${GO:-go}
NODE=${NODE:-node}
command -v $GO >/dev/null || export PATH="$PATH:/e/tools/go/bin"
command -v $NODE >/dev/null || { echo "需要 node"; exit 1; }
$GO build -o sahou.exe . || exit 1
GOOS=js GOARCH=wasm $GO build -o sahou.wasm ./cmd/sahouwasm || { echo "wasm 构建失败"; exit 1; }
cp sahou.wasm examples/响应式/sahou.wasm
$NODE "C:/Users/find/AppData/Local/Temp/wasm_reactive.mjs" 2>/dev/null || $NODE tests/wasm_reactive.mjs
