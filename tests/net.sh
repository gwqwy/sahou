#!/bin/bash
# v1.1 网络模块回归测试：启动待办服务 → curl 增删查 → 断言 → 结束。
# 用法: bash tests/net.sh
cd "$(dirname "$0")/.." || exit 1
SAHOU=./sahou.exe
[ -f "$SAHOU" ] || { echo "先构建: go build -o sahou.exe ."; exit 1; }

# 清掉可能残留的旧服务（占用 8080 会误导测试）
PID=$(netstat -ano | grep ':8080.*LISTENING' | awk '{print $5}' | head -1)
[ -n "$PID" ] && taskkill //F //PID $PID >/dev/null 2>&1
sleep 0.5

"$SAHOU" run examples/待办服务.saho >/dev/null 2>&1 &
SRV=$!
sleep 1.5
pass=0; fail=0
check() { # check 名称 期望输出 实际输出
  if [ "$2" = "$3" ]; then pass=$((pass+1)); else
    fail=$((fail+1)); echo "FAIL $1"; echo "  期望: $2"; echo "  实际: $3"
  fi
}
URL="http://localhost:8080/%E5%BE%85%E5%8A%9E"

# 空列表
check "初始为空" "[]" "$(curl -s $URL)"
# 新增两条（请求体走 UTF-8 文件，绕开 Git Bash→Windows curl 的 argv 编码转换）
printf '{"事项":"task-a"}' > /tmp/body1.json
printf '{"事项":"task-b"}' > /tmp/body2.json
check "新增-201体" '{"事项":"task-a","序号":1}' "$(curl -s -X POST $URL --data-binary @/tmp/body1.json)"
check "新增-序号自增" '{"事项":"task-b","序号":2}' "$(curl -s -X POST $URL --data-binary @/tmp/body2.json)"
# 列表保持插入顺序
check "列表顺序" '[{"事项":"task-a","序号":1},{"事项":"task-b","序号":2}]' "$(curl -s $URL)"
# 删除
check "删除-存在" "已删除序号 1" "$(curl -s -X DELETE "$URL?%E5%BA%8F%E5%8F%B7=1")"
check "删除-不存在404" "没有序号 9 的待办" "$(curl -s -X DELETE "$URL?%E5%BA%8F%E5%8F%B7=9")"
check "删除后列表" '[{"事项":"task-b","序号":2}]' "$(curl -s $URL)"
# 未定义路由 404
check "无路由404" "没有路由 GET /nope
no route GET /nope" "$(curl -s http://localhost:8080/nope)"
# 客户端 网络.请求 + 自文本
cat > /tmp/net_client_test.saho <<'EOF'
响应 = 网络.请求("http://localhost:8080/%E5%BE%85%E5%8A%9E")
数据 = 网络.自文本(响应["体"])
print("状态 {响应["状态"]} 条目 {长度(数据)} 首事项 {数据[0]["事项"]}")
EOF
check "客户端请求" "状态 200 条目 1 首事项 task-b" "$("$SAHOU" run /tmp/net_client_test.saho 2>/dev/null)"

kill $SRV 2>/dev/null
wait $SRV 2>/dev/null
echo "网络模块: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
