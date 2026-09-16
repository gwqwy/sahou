#!/bin/bash
# v1.5 转译器一致性测试：
#   1) 核心用例转译后在 Node 运行，输出与解释器逐字对齐；
#   2) 计数器页面在 DOM 桩下验证交互逻辑。
# 用法: bash tests/build.sh
cd "$(dirname "$0")/.." || exit 1
SAHOU=./sahou.exe
NODE=${NODE:-node}
TMPW=$(cygpath -m /tmp 2>/dev/null || echo /tmp)
command -v $NODE >/dev/null || { echo "需要 node"; exit 1; }
pass=0; fail=0

# --- 1) 核心用例输出对齐（浏览器安全子集：无 输入/文件）---
for f in tests/cases/010_numbers.saho tests/cases/020_strings.saho \
         tests/cases/030_lists_dicts.saho tests/cases/040_control.saho \
         tests/cases/050_functions.saho tests/cases/060_errors.saho \
         tests/cases/070_builtins2.saho examples/入门演示.saho; do
  name=$(basename "$f" .saho)
  expected="tests/cases/$name.out"
  [ -f "$expected" ] || expected="/tmp/$name.expected"
  if [ ! -f "$expected" ]; then
    "$SAHOU" run "$f" > "$expected" 2>/dev/null
  fi
  js="$TMPW/$name.build.js"
  if ! "$SAHOU" build "$f" -o "$js" >/dev/null 2>/tmp/build.err; then
    fail=$((fail+1)); echo "FAIL $name (转译失败)"; head -3 /tmp/build.err; continue
  fi
  actual=$($NODE "$js" 2>&1)
  if [ "$actual" = "$(cat "$expected")" ]; then
    pass=$((pass+1))
  else
    fail=$((fail+1)); echo "FAIL $name (输出不一致)"; diff <(echo "$actual") "$expected" | head -8
  fi
done

# --- 2) 计数器页面（DOM 桩）---
if "$SAHOU" build examples/计数器.saho -o "$TMPW/计数器.build.js" >/dev/null 2>&1; then
  cat > tests/tmp_counter_run.mjs <<EOF
import { pathToFileURL } from "node:url";
import { document, __saho_test_register, __saho_test_dom, makeEl } from "./domstub.mjs";
globalThis.document = document;
__saho_test_register("显示", makeEl("p", "显示"));
__saho_test_register("按钮", makeEl("button", "按钮"));
await import(pathToFileURL("$TMPW/计数器.build.js").href);
const dom = __saho_test_dom;
const show = dom["显示"], btn = dom["按钮"];
console.log("初始:", show.textContent);
btn.dispatch("click");
btn.dispatch("click");
btn.dispatch("click");
console.log("点3次后:", show.textContent);
if (show.textContent.includes("3 次")) { console.log("COUNTER_OK"); }
else { console.log("COUNTER_FAIL"); process.exit(1); }
EOF
  out=$($NODE tests/tmp_counter_run.mjs 2>&1)
  if grep -q "COUNTER_OK" <<<"$out"; then
    pass=$((pass+1)); echo "$out" | head -2
  else
    fail=$((fail+1)); echo "FAIL 计数器页面"; echo "$out" | head -8
  fi
  rm -f tests/tmp_counter_run.mjs
else
  fail=$((fail+1)); echo "FAIL 计数器页面 (转译失败)"
fi

echo "转译器: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
