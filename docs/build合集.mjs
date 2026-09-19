#!/usr/bin/env node
// build合集.mjs —— 重新生成 docs/卅语言文档合集.md。
// 卷首（总目录/怎么读/版本注记）是手工维护的，本脚本原样保留到第一个分篇标记为止；
// 之后按编号顺序把 17 篇文档拼接进去（每篇一个 `<!-- ==== 第 N 篇 ==== -->` 分隔标记）。
// 分篇文档是唯一事实来源，合集只是"一本在手"的整合版。
// 零依赖：node docs/build合集.mjs
"use strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const outFile = path.join(here, "卅语言文档合集.md");

// 按阅读顺序：README + 00..15（编号顺序）
const files = [
  "README.md",
  "00-设计决策简报.md",
  "01-语言设计总览.md",
  "02-语言规范.md",
  "03-入门教程.md",
  "04-编译器技术设计.md",
  "05-前端后端扩展设计.md",
  "06-网络模块教程.md",
  "07-前端模块教程.md",
  "08-模块与包管理.md",
  "09-标准库模块.md",
  "10-纯sahou标准库包.md",
  "11-用户手册.md",
  "12-管道编程.md",
  "13-响应式计算模型.md",
  "14-全栈开发.md",
  "15-应用开发.md",
];

// 卷首：沿用现有合集的开头（到第一个分篇标记为止），没有就要求先手工补一份
let header = `# 卅语言 · 文档合集（整合版）\n`;
if (fs.existsSync(outFile)) {
  const old = fs.readFileSync(outFile, "utf8");
  const idx = old.indexOf("<!-- ============ 第 1 篇");
  if (idx > 0) header = old.slice(0, idx).trimEnd();
}

let body = "";
for (let i = 0; i < files.length; i++) {
  const name = files[i];
  const content = fs.readFileSync(path.join(here, name), "utf8").trim();
  body += `\n---\n\n<!-- ============ 第 ${i + 1} 篇：${name} ============ -->\n\n${content}\n`;
}

fs.writeFileSync(outFile, header + body, "utf8");
const pieces = body.split("<!-- ============ ").length - 1;
console.log(`已生成 ${path.basename(outFile)}：卷首 + ${pieces} 篇正文（${files.length} 篇源文档）`);
