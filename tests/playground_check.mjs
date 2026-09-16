// 在线体验页等价测试：__sahou_run 全局函数（与页面同一 wasm 产物）
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
const require_ = createRequire(import.meta.url);
const ROOT = "E:/文件/代码系统";
globalThis.__SAHO_PLAYGROUND = true;
require_(path.join(ROOT, "wasm_exec.js"));
const go = new require_("node:module") && (await import("file:///" + "wasm_exec.js"), new (require_(path.join(ROOT, "wasm_exec.js")).Go || globalThis.Go))();
