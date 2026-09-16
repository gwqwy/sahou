#!/bin/bash
# v4 WASM 直接运行回归测试：构建 sahou.wasm 并在 node 里验证响应式格子。
# 构建前把仓库根 stones/ 同步进 cmd/sahouwasm/stones/（embed 无法引用上级目录）。
cd "$(dirname "$0")/.." || exit 1
GO=${GO:-go}
rm -rf cmd/sahouwasm/stones
cp -r stones cmd/sahouwasm/stones
GOOS=js GOARCH=wasm $GO build -o sahou.wasm ./cmd/sahouwasm || { echo "wasm 构建失败"; exit 1; }
cp sahou.wasm examples/响应式/sahou.wasm
node tests/wasm_reactive.mjs
node tests/playground_check.mjs
