#!/bin/bash
# v2 标准库模块回归测试：随机/时间/数学/编码/系统（解释器 + 转译双侧）。
cd "$(dirname "$0")/.." || exit 1
SAHOU=./sahou.exe
NODE=${NODE:-node}
TMPW=$(cygpath -m /tmp 2>/dev/null || echo /tmp)
pass=0; fail=0
check() {
  if [ "$2" = "$3" ]; then pass=$((pass+1)); else
    fail=$((fail+1)); echo "FAIL $1"; echo "  期望: $2"; echo "  实际: $3"
  fi
}
W=$(mktemp -d)

cat > "$W/t.saho" <<'EOF'
随机.种子(42)
甲 = 随机.数(1, 10)
随机.种子(42)
乙 = 随机.数(1, 10)
print("种子可复现: {甲 == 乙}")
print("范围内: {甲 >= 1 并且 甲 <= 10}")
名单 = 随机.洗牌([1, 2, 3, 4, 5])
print("洗牌不改原列表: {[1, 2, 3, 4, 5] == [1, 2, 3, 4, 5]}")
print("洗牌保元素: {长度(名单) == 5}")
print(数学.幂(2, 10))
print(数学.圆周率)
print(数学.最大公约数(12, 18))
print(数学.最小公倍数(4, 6))
print(数学.向下取整(3.9))
print(数学.向上取整(3.1))
print(编码.网址解码(编码.网址("卅 语言 abc123")))
print(编码.六十四解码(编码.六十四("hello 卅")))
print(编码.十六进制解码(编码.十六进制("测试")))
print(时间.文本(时间.解析("2026-09-15 08:05:03")))
起点 = 时间.计时()
print("耗时非负: {时间.耗时(起点) >= 0}")
print(系统.平台())
EOF
cat > "$W/期望.txt" <<'EOF'
种子可复现: 真
范围内: 真
洗牌不改原列表: 真
洗牌保元素: 真
1024
3.14159265358979
6
12
3
4
卅 语言 abc123
hello 卅
测试
2026-09-15 08:05:03
耗时非负: 真
windows
EOF
check "run-标准库" "$(cat "$W/期望.txt")" "$(cd "$W" && "$OLDPWD/$SAHOU" run t.saho 2>&1)"

# 转译侧（node）：随机序列算法不同，比较去掉随机值两行
if "$SAHOU" build "$W/t.saho" -o "$TMPW/std.build.js" >/dev/null 2>&1; then
  $NODE "$TMPW/std.build.js" > "$W/js.out" 2>&1
  # 转译产物运行在 JS 宿主：系统.平台() 报 browser（其余行为与解释器逐字一致）
  go_out=$(tail -n +3 "$W/期望.txt" | grep -v "种子可复现\|范围内" | sed '$s/windows/browser/')
  js_out=$(tail -n +3 "$W/js.out" | grep -v "种子可复现\|范围内")
  check "build-标准库" "$go_out" "$js_out"
else
  fail=$((fail+1)); echo "FAIL build-标准库 (转译失败)"
fi

# 错误路径：空列表挑、负数对数、非法解码
printf '随机.挑([])\n' > "$W/e1.saho"
check "挑空列表" "1" "$(cd "$W" && "$OLDPWD/$SAHOU" run e1.saho >/dev/null 2>&1; echo $?)"
printf '数学.对数(0)\n' > "$W/e2.saho"
check "对数0" "0 和负数没有对数" "$(cd "$W" && "$OLDPWD/$SAHOU" run e2.saho 2>&1 | grep -o '0 和负数没有对数' | head -1)"
printf '编码.网址解码("%%zz")\n' > "$W/e3.saho"
check "非法解码" "遇到非十六进制字符" "$(cd "$W" && "$OLDPWD/$SAHOU" run e3.saho 2>&1 | grep -o '遇到非十六进制字符')"
printf '数学.幂(-8, 0.5)\n' > "$W/e4.saho"
check "负数非整数次幂" "1" "$(cd "$W" && "$OLDPWD/$SAHOU" run e4.saho >/dev/null 2>&1; echo $?)"

# 系统.参数 与 退出
printf 'print(系统.参数())\n系统.退出(7)\n' > "$W/args.saho"
out=$(cd "$W" && "$OLDPWD/$SAHOU" run args.saho a b); code=$?
check "参数与退出码" '["a", "b"]
7' "$out
$code"

rm -rf "$W"
echo "标准库: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
