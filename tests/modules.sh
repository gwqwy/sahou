#!/bin/bash
# v2 模块系统 + stones 回归测试。
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
WORK=$(mktemp -d)
cp tests/modproject/算术.saho "$WORK/" 
cp tests/modproject/主程序.saho "$WORK/"
cp tests/modproject/期望.txt "$WORK/"

# 1) 解释器：引入用户模块 + 内置模块
check "run-模块" "$(cat "$WORK/期望.txt")" "$(cd "$WORK" && "$OLDPWD/$SAHOU" run 主程序.saho 2>&1)"

# 2) 循环引入报错
printf '用 "乙" 引入\n' > "$WORK/甲.saho"
printf '用 "甲" 引入\n' > "$WORK/乙.saho"
printf '用 "甲" 引入\nprint("x")\n' > "$WORK/环.saho"
out=$(cd "$WORK" && "$OLDPWD/$SAHOU" run 环.saho 2>&1); code=$?
check "循环引入-退出码" "1" "$code"
check "循环引入-消息" "循环引入了自己" "$(grep -o '循环引入了自己' <<<"$out")"

# 3) 模块顶层 返回 报错
printf '返回 42\n' > "$WORK/早退.saho"
printf '用 "早退" 引入\n' > "$WORK/用早退.saho"
out=$(cd "$WORK" && "$OLDPWD/$SAHOU" run 用早退.saho 2>&1)
check "顶层返回-消息" "模块里不能写顶层的 返回" "$(grep -o '模块里不能写顶层的 返回' <<<"$out")"

# 4) 找不到模块的报错
printf '用 "不存在" 引入\n' > "$WORK/缺.saho"
out=$(cd "$WORK" && "$OLDPWD/$SAHOU" run 缺.saho 2>&1)
check "缺模块-消息" "找不到模块" "$(grep -o '找不到模块' <<<"$out")"

# 5) 转译：打包用户模块 + 内置绑定，Node 运行对齐
if "$SAHOU" build "$WORK/主程序.saho" -o "$TMPW/mod.build.js" >/dev/null 2>&1; then
  check "build-模块" "$(cat "$WORK/期望.txt")" "$($NODE "$TMPW/mod.build.js" 2>&1)"
else
  fail=$((fail+1)); echo "FAIL build-模块 (转译失败)"
fi

# 6) stones 流程：装目录包 → 引入
mkdir -p "$WORK/pkgdemo"
printf '函数 你好(名字)\n    返回 "你好, {名字}!"\n完毕\n' > "$WORK/pkgdemo/main.saho"
printf '用 "pkgdemo" 引入\nprint(pkgdemo.你好("stones"))\n' > "$WORK/包主.saho"
out=$(cd "$WORK" && "$OLDPWD/$SAHOU" 装 pkgdemo 2>&1)
check "装-提示" "已安装包 pkgdemo" "$(grep -o '已安装包 pkgdemo' <<<"$out")"
check "stones.yml" "包:pkgdemo:local" "$(tr -d '
 	' < "$WORK/stones.yml")"
check "run-stones包" "你好, stones!" "$(cd "$WORK" && "$OLDPWD/$SAHOU" run 包主.saho 2>&1)"
out=$(cd "$WORK" && "$OLDPWD/$SAHOU" 装 2>&1); code=$?
check "装-校验通过" "0" "$code"

# 7) 转译页面模块引入
printf '用 "页面" 引入\nprint("页面模块已引入")\n' > "$WORK/页测.saho"
"$SAHOU" build "$WORK/页测.saho" -o "$TMPW/page.build.js" >/dev/null 2>&1
check "build-页面模块" "页面模块已引入" "$($NODE "$TMPW/page.build.js" 2>&1)"

rm -rf "$WORK"
echo "模块系统: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
