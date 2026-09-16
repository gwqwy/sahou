#!/bin/bash
# v4.2 打包与自带库回归测试：sahou 打包（exe 自带 stones）、sahou stones、
# sahou 装 <内置包名>、sahou serve 内嵌 wasm 运行时与 .saho 壳页。
cd "$(dirname "$0")/.." || exit 1
SAHOU=./sahou.exe
GO=${GO:-go}
TMPW=$(cygpath -m /tmp 2>/dev/null || echo /tmp)
pass=0; fail=0
check() {
  if [ "$2" = "$3" ]; then pass=$((pass+1)); else
    fail=$((fail+1)); echo "FAIL $1"; echo "  期望: $2"; echo "  实际: $3"
  fi
}

# 1) sahou stones：列出内嵌包
out=$("$SAHOU" stones 2>&1)
check "stones-统计库" "有" "$(grep -q "统计库" <<<"$out" && echo 有 || echo 无)"
check "stones-单位换算" "有" "$(grep -q "单位换算" <<<"$out" && echo 有 || echo 无)"

# 2) sahou 装 <内置包名>：离线取出
W=$(mktemp -d)
printf '用 "统计库" 引入\nprint(统计库.中位数([5, 1, 3]))\n' > "$W/p.saho"
(cd "$W" && "$OLDPWD/$SAHOU" 装 统计库 >/dev/null 2>&1)
check "装-内置包" "3" "$(cd "$W" && "$OLDPWD/$SAHOU" run p.saho 2>&1)"
check "装-清单" "有" "$(grep -q "统计库" "$W/stones.yml" 2>/dev/null && echo 有 || echo 无)"

# 3) 打包：exe 自带 stones（旁边不需要 stones/ 目录）
printf '用 "统计库" 引入\n用 "单位换算" 引入\nprint("平均: {统计库.平均值([2, 4, 6])}")\nprint("体温: {单位换算.温度换算(37, "摄氏", "华氏")}")\n' > "$W/带库.saho"
if "$SAHOU" 打包 "$W/带库.saho" -o "$W/带库.exe" >/dev/null 2>&1; then
  out=$("$W/带库.exe" 2>&1)
  check "打包-自带库" "平均: 4
体温: 98.6" "$out"
else
  fail=$((fail+1)); echo "FAIL 打包-编译失败"
fi

# 4) serve：内嵌 wasm 运行时兜底 + .saho 壳页
mkdir -p "$W/网页"
printf 'print("你好")\n' > "$W/网页/hello.saho"
"$SAHOU" serve "$W/网页" >/dev/null 2>&1 &
SRV=$!
sleep 1
check "serve-wasm_exec.js" "200" "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8000/wasm_exec.js)"
check "serve-sahou.wasm" "200" "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8000/sahou.wasm)"
check "serve-别名卅.wasm" "200" "$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:8000/%E5%8D%85.wasm")"
check "serve-壳页" "有" "$(curl -s http://127.0.0.1:8000/hello.saho | grep -q "__SAHO_PAGE" && echo 有 || echo 无)"
check "serve-缺页404" "404" "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8000/没有.saho)"
kill $SRV 2>/dev/null
taskkill //F //IM sahou.exe >/dev/null 2>&1

rm -rf "$W"
echo "打包与自带库: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
