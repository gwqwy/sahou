#!/usr/bin/env node
// sync-shared.js —— 把 editors/shared/language.json 同步到各编辑器集成目录。
// shared/language.json 是编辑器元数据（关键字/内置函数/模块/stones 包）的唯一事实来源，
// 各编辑器的扩展只读自己目录里的副本（打包 vsix 等发布物时只能带走目录内文件）。
// 零依赖，node 任意较新版本可跑：node editors/sync-shared.js
"use strict";
const fs = require("fs");
const path = require("path");

const root = __dirname;
const sharedPath = path.join(root, "shared", "language.json");

// 每个编辑器集成：把共享数据复制到自己的 data/language.json
const targets = [path.join(root, "vscode", "sahou", "data", "language.json")];

const raw = fs.readFileSync(sharedPath, "utf8");
const data = JSON.parse(raw); // 顺带校验 JSON 合法
let n = 0;
for (const target of targets) {
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.writeFileSync(target, raw, "utf8");
  console.log("已同步 ->", path.relative(root, target));
  n++;
}
console.log(
  "shared/language.json 同步完成：" + n + " 个编辑器目录，" +
  "关键字 " + data.keywords.length + "，内置函数 " + data.builtins.length +
  "，模块 " + data.modules.length + "，stones 包 " + data.stones.length
);
