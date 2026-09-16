#!/bin/bash
# v1.7 应用与后端写法回归测试：路径参数路由 + 快捷回应 + 渲染文件 + 桌面/手机端静态服务。
# 用法: bash tests/app.sh
cd "$(dirname "$0")/.." || exit 1
SAHOU=./sahou.exe
[ -f "$SAHOU" ] || { echo "先构建: go build -o sahou.exe ."; exit 1; }

for PORT in 8080 8091 8092 8093; do
  PID=$(netstat -ano | grep ":$PORT.*LISTENING" | awk '{print $5}' | head -1)
  [ -n "$PID" ] && taskkill //F //PID $PID >/dev/null 2>&1
done
sleep 0.5

pass=0; fail=0
check() {
  if [ "$2" = "$3" ]; then pass=$((pass+1)); else
    fail=$((fail+1)); echo "FAIL $1"; echo "  期望: $2"; echo "  实际: $3"
  fi
}
like() { # like 名称 子串 整串
  case "$3" in
    *"$2"*) pass=$((pass+1));;
    *) fail=$((fail+1)); echo "FAIL $1"; echo "  应包含: $2"; echo "  实际: $3";;
  esac
}

TMPD="tests/.tmp_app"
rm -rf "$TMPD"; mkdir -p "$TMPD"
printf '<h1>欢迎，[[名]]</h1>' > "$TMPD/模板.html"
printf 'console.log(1)' > "$TMPD/app.js"
cat > "$TMPD/服务.saho" <<'EOF'
函数 看文章(请求)
    编号 = 数(请求["参数"]["编号"])
    名单 = ["第一篇", "第二篇"]
    如果 编号 >= 1 并且 编号 <= 长度(名单)
        返回 网络.JSON回应({"标题": 名单[编号 - 1]})
    完毕
    返回 网络.JSON回应({"错误": "没有这篇文章"}, 404)
完毕
函数 去首页(请求)
    返回 网络.重定向("/")
完毕
函数 模板页(请求)
    返回 网络.网页回应(网页.渲染文件("模板.html", {"名": "<小明>"}))
完毕
函数 首页(请求)
    返回 网络.网页回应(网页.页面("测试", "<b>OK</b>"))
完毕
服务 = 网络.服务()
服务.路由("GET", "/", 首页)
服务.路由("GET", "/文章/:编号", 看文章)
服务.路由("GET", "/旧首页", 去首页)
服务.路由("GET", "/模板", 模板页)
服务.监听(8091)
EOF
"$SAHOU" run "$TMPD/服务.saho" >/dev/null 2>&1 &
SRV=$!
sleep 1.5

like "网页.页面骨架" "<!doctype html><html><head><meta charset='utf-8'>" "$(curl -s http://localhost:8091/)"
check "路径参数" '{"标题":"第一篇"}' "$(curl -s http://localhost:8091/%E6%96%87%E7%AB%A0/1)"
check "路径参数-404" '{"错误":"没有这篇文章"}' "$(curl -s http://localhost:8091/%E6%96%87%E7%AB%A0/9)"
like "重定向302" "Location: /" "$(curl -s -i http://localhost:8091/%E6%97%A7%E9%A6%96%E9%A1%B5)"
check "渲染文件" '<h1>欢迎，&lt;小明&gt;</h1>' "$(curl -s http://localhost:8091/%E6%A8%A1%E6%9D%BF)"
kill $SRV 2>/dev/null; wait $SRV 2>/dev/null

# 桌面应用（静默模式）：静态文件服务
cat > "$TMPD/桌面.saho" <<'EOF'
应用.窗口(".", 8092, 真)
EOF
"$SAHOU" run "$TMPD/桌面.saho" >/dev/null 2>&1 &
SRV=$!
sleep 1.5
check "桌面应用静态服务" "console.log(1)" "$(curl -s http://localhost:8092/app.js)"
kill $SRV 2>/dev/null; wait $SRV 2>/dev/null

# 手机端：0.0.0.0 监听（本机也可访问）
cat > "$TMPD/手机.saho" <<'EOF'
应用.手机(".", 8093)
EOF
"$SAHOU" run "$TMPD/手机.saho" >/dev/null 2>&1 &
SRV=$!
sleep 1.5
check "手机端静态服务" "console.log(1)" "$(curl -s http://127.0.0.1:8093/app.js)"
kill $SRV 2>/dev/null; wait $SRV 2>/dev/null
rm -rf "$TMPD"

echo "应用与后端写法: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
