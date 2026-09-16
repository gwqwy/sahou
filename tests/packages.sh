#!/bin/bash
# v2 stones 标准库包回归测试：算术库/文本库/游戏库/中文数字/统计库/单位换算/进制库/几何库/算法库（解释器 + 转译双侧，子目录向上查找）。
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

# 1) 子目录向上查找：examples/pkgdemo 用到仓库根的 stones/
expected="公约数: 6
约分: [3, 4]
质因数分解: [2, 2, 2, 3, 3, 5]
是否质数: 真
阶乘: 720
-----卅-----
回文: 真
字符统计: {\"a\": 5, \"b\": 2, \"r\": 2, \"c\": 1, \"d\": 1}
驼峰: userName
掷骰子: 5
抛硬币: 反
抽奖: [\"小刚\", \"小红\"]
95 = 九十五
12345678 = 一千二百三十四万五千六百七十八
回转: 2026
平均: 74.6
中位数: 72
标准差: 14.7729482500955
体温: 98.6
百公里: 62.1371192237334
255 的十六进制: FF
FF 的十进制: 255
勾股斜边: 5
海伦面积: 6
冒泡排序: [1, 2, 5, 8]
二分查找: 2"
actual=$("$SAHOU" run examples/pkgdemo/演示.saho 2>&1)
# 随机行两侧不同（已知设计差异），洗牌/抽奖行去掉后比较
strip_random() { grep -v "掷骰子\|抛硬币\|抽奖"; }
check "run-包演示(向上查找)" "$(strip_random <<<"$expected")" "$(strip_random <<<"$actual")"

# 2) 转译侧：同一脚本打包后在 node 输出对齐（同样去掉随机行）
if "$SAHOU" build examples/pkgdemo/演示.saho -o "$TMPW/pkg.build.js" >/dev/null 2>&1; then
  check "build-包演示" "$(strip_random <<<"$expected")" "$(strip_random <<<"$("$NODE" "$TMPW/pkg.build.js" 2>&1)")"
else
  fail=$((fail+1)); echo "FAIL build-包演示 (转译失败)"
fi

# 3) sahou 装：安装单文件包到临时项目并引入
W=$(mktemp -d)
mkdir -p "$W/mypkg"
printf '函数 加倍(x)\n    返回 x * 2\n完毕\n' > "$W/mypkg/main.saho"
printf '用 "mypkg" 引入\nprint(mypkg.加倍(21))\n' > "$W/prog.saho"
(cd "$W" && "$OLDPWD/$SAHOU" 装 mypkg >/dev/null 2>&1)
check "装-引入" "42" "$(cd "$W" && "$OLDPWD/$SAHOU" run prog.saho 2>&1)"
check "装-清单" "包:mypkg:local" "$(tr -d '\n \t' < "$W/stones.yml")"

# 4) 单文件包：stones/<名>.saho 形式
printf '函数 三倍(x)\n    返回 x * 3\n完毕\n' > "$W/single.saho"
mv "$W/single.saho" "$W/stones/single.saho" 2>/dev/null || { mkdir -p "$W/stones" && mv "$W/single.saho" "$W/stones/single.saho"; }
printf '用 "single" 引入\nprint(single.三倍(5))\n' > "$W/p2.saho"
check "单文件包" "15" "$(cd "$W" && "$OLDPWD/$SAHOU" run p2.saho 2>&1)"

# 5) 模块可见宿主内置但不可见主程序变量（模块隔离）
printf '输出 = []\n函数 记(值)\n    输出.添加(值)\n    返回 长度(输出)\n完毕\n' > "$W/计数器.saho"
printf '秘密 = "看不见"\n用 "计数器" 引入\nprint(计数器.记(9))\nprint(计数器.记(8))\n' > "$W/iso_main.saho"
check "模块隔离" "1
2" "$(cd "$W" && "$OLDPWD/$SAHOU" run iso_main.saho 2>&1)"

# 6) 循环引入报错（包级别）
printf '用 "乙包" 引入\n' > "$W/甲包.saho"
printf '用 "甲包" 引入\n' > "$W/乙包.saho"
printf '用 "甲包" 引入\n' > "$W/环测.saho"
out=$(cd "$W" && "$OLDPWD/$SAHOU" run 环测.saho 2>&1)
check "包循环引入" "循环引入了自己" "$(grep -o '循环引入了自己' <<<"$out" | head -1)"

rm -rf "$W"
echo "标准库包: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
