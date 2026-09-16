#!/bin/bash
# sahou conformance 测试：tests/cases/*.saho 与同名 .out 快照比较。
# 用法: bash tests/run.sh
cd "$(dirname "$0")/.." || exit 1
SAHOU=./sahou.exe
[ -f "$SAHOU" ] || { echo "先构建: go build -o sahou.exe ."; exit 1; }
pass=0; fail=0
for f in tests/cases/*.saho; do
  name=$(basename "$f" .saho)
  expected="tests/cases/$name.out"
  actual=$("$SAHOU" run "$f" 2>/dev/null)
  if [ ! -f "$expected" ]; then
    echo "SKIP $name (无快照)"
    continue
  fi
  if [ "$actual" = "$(cat "$expected")" ]; then
    pass=$((pass+1))
  else
    fail=$((fail+1))
    echo "FAIL $name"
    diff <(echo "$actual") "$expected" | head -15
  fi
done
# 错误用例：*.err 文件第一行是期望退出码，其余是输出应包含的子串
for f in tests/cases/*.saho; do
  name=$(basename "$f" .saho)
  errfile="tests/cases/$name.err"
  [ -f "$errfile" ] || continue
  want_code=$(head -1 "$errfile")
  want_sub=$(tail -n +2 "$errfile")
  out=$("$SAHOU" run "$f" 2>&1)
  code=$?
  if [ "$code" != "$want_code" ]; then
    fail=$((fail+1)); echo "FAIL $name (exit=$code, want $want_code)"; continue
  fi
  if [ -n "$want_sub" ] && ! grep -q "$want_sub" <<<"$out"; then
    fail=$((fail+1)); echo "FAIL $name (缺少错误子串: $want_sub)"; echo "$out" | head -3
    continue
  fi
  pass=$((pass+1))
done
echo "通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
