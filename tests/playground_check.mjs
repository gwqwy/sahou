// 在线体验页等价测试：__sahou_run 全局函数（与页面同一 wasm 产物），
// 同时验证 wasm 内嵌 stones（浏览器 `用 "包名" 引入` 免安装）。
import path from "node:path";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
const ROOT = path.resolve(path.dirname(decodeURI(new URL(import.meta.url).pathname).replace(/^\/(\w:)/, "$1")), "..");
globalThis.__SAHO_PLAYGROUND = true;
await import(pathToFileURL(path.join(ROOT, "wasm_exec.js")).href);
const go = new Go();
const bytes = await readFile(path.join(ROOT, "sahou.wasm"));
const r = await WebAssembly.instantiate(bytes, go.importObject);
go.run(r.instance);

function run(src) {
  const raw = globalThis.__sahou_run(src);
  const parsed = JSON.parse(raw);
  return parsed;
}
let failed = 0;
function check(name, got, want) {
  if (got === want) { console.log(`√ ${name}: ${got}`); }
  else { failed++; console.log(`× ${name}: 期望 ${want}，实际 ${got}`); }
}

const r1 = run('print(1 + 1)');
check("算术", r1.output.trim(), "2");
check("无报错", r1.error, undefined);

const r2 = run('用 "统计库" 引入\nprint(统计库.平均值([1, 2, 3]))');
check("内嵌 stones", r2.output.trim(), "2");
check("stones 无报错", r2.error, undefined);

const r3 = run('print([3, 1, 2] -> 排序)');
check("管道 ->", r3.output.trim(), "[1, 2, 3]");

const r4 = run('格子 总价 = 0\nprint("格子可用")');
check("格子", r4.output.trim(), "格子可用");

const alias = globalThis.__卅_run ? "有" : "无";
check("别名 __卅_run", alias, "有");

if (failed > 0) { throw new Error(`${failed} 项失败`); }
console.log("PLAYGROUND_OK");
