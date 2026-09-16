// WASM 响应式回归测试：sahou.wasm 在 node 里直接解释 .saho，验证格子联动。
import fs from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
const require_ = createRequire(import.meta.url);
import path from "node:path";
const ROOT = path.resolve(path.dirname(decodeURI(new URL(import.meta.url).pathname).replace(/^\/(\w:)/, "$1")), "..");
function makeEl(id) {
  return { id, textContent: "", value: "3.5", style: {}, listeners: {},
           addEventListener(t, fn) { (this.listeners[t] = this.listeners[t] || []).push(fn); },
           dispatch(t) { (this.listeners[t] || []).forEach(fn => fn({ preventDefault() {} })); } };
}
const els = {};
for (const id of ["单价", "数量", "总价", "提示"]) { els[id] = makeEl(id); }
globalThis.document = { querySelector: (sel) => els[sel.replace("#", "")] || null };
globalThis.__SAHO_SOURCE = `
单价 = 3.5
数量 = 2
格子 总价 = 单价 * 数量
格子 提示 = "单价 {单价} 元 × 数量 {数量} = {总价} 元"
单价输入 = 页面.取元素("#单价")
数量输入 = 页面.取元素("#数量")
总价显示 = 页面.取元素("#总价")
提示显示 = 页面.取元素("#提示")
页面.绑输入(单价输入, "单价")
页面.绑输入(数量输入, "数量")
页面.绑定(总价显示, "总价")
页面.绑定(提示显示, "提示")
`;
(await import(pathToFileURL(path.join(ROOT, "wasm_exec.js")).href));
const go = new Go();
const wasmPath = path.join(ROOT, "sahou.wasm");
WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject)
  .then(res => go.run(res.instance))
  .catch(e => { console.error("WASM 错误:", e); process.exit(1); });

setTimeout(() => {
  const $ = (id) => els[id];
  let failed = 0;
  function check(name, actual, expected) {
    if (actual === expected) console.log(`√ ${name}: ${actual}`);
    else { failed++; console.error(`× ${name}: 期望 ${expected}，实际 ${actual}`); }
  }
  console.error("S1:", JSON.stringify([$("单价").textContent, $("数量").textContent, $("总价").textContent, $("提示").textContent]));
  check("初始总价", $("总价").textContent, "12.25");
  $("数量").value = "5"; $("数量").dispatch("input");
  console.error("S2:", JSON.stringify([$("单价").textContent, $("数量").textContent, $("总价").textContent, $("提示").textContent]));
  check("数量改 5", $("总价").textContent, "17.5");
  $("单价").value = "2"; $("单价").dispatch("input");
  console.error("S3:", JSON.stringify([$("单价").textContent, $("数量").textContent, $("总价").textContent, $("提示").textContent]));
  console.error("DEBUG 总价 el:", JSON.stringify({text: $("总价").textContent, listeners: Object.keys($("总价").listeners)}));
  console.error("DEBUG 单价 el:", JSON.stringify({value: $("单价").value, listeners: Object.keys($("单价").listeners)}));
  check("单价改 2", $("总价").textContent, "10");
  check("提示同步", $("提示").textContent, "单价 2 元 × 数量 5 = 10 元");
  if (failed > 0) process.exit(1);
  console.log("WASM_REACTIVE_OK");
  process.exit(0);
}, 1500);
