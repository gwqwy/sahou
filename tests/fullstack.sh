#!/bin/bash
# v1.6 全栈回归测试：数据库 + 网页渲染 + 表单/会话/Cookie → 起留言板 → curl 断言。
# 用法: bash tests/fullstack.sh
cd "$(dirname "$0")/.." || exit 1
SAHOU=./sahou.exe
[ -f "$SAHOU" ] || { echo "先构建: go build -o sahou.exe ."; exit 1; }

# 清掉可能残留的旧服务（占用 8080 会误导测试）
PID=$(netstat -ano | grep ':8080.*LISTENING' | awk '{print $5}' | head -1)
[ -n "$PID" ] && taskkill //F //PID $PID >/dev/null 2>&1
sleep 0.5

pass=0; fail=0
check() { # check 名称 期望输出 实际输出
  if [ "$2" = "$3" ]; then pass=$((pass+1)); else
    fail=$((fail+1)); echo "FAIL $1"; echo "  期望: $2"; echo "  实际: $3"
  fi
}

# ---------- 1. 数据库模块（本地脚本）----------
rm -f /tmp/fs_全栈.db
cat > /tmp/fs_db.saho <<'EOF'
库 = 数据库.打开("/tmp/fs_全栈.db")
print(库.执行("建 表 如无 用户 (名字, 年龄)"))
库.执行("插入 用户 (名字, 年龄) 值 (?, ?), (?, ?)", "小明", 10, "小红", 12)
print(库.执行("改 用户 设 年龄 = ? 哪里 名字 = ?", 11, "小明"))
for 行 于 库.查询("选择 名字 出 用户 哪里 年龄 >= 11 排序 按 名字 升")
    print(行["名字"])
完毕
print(库.执行("DELETE FROM 用户 哪里 名字 = ?", "小红"))
库2 = 数据库.打开("/tmp/fs_全栈.db")
print("重开 {长度(库2.表())} 表 {长度(库2.查询("SELECT * FROM 用户"))} 行")
EOF
# /tmp 在 Windows 下 Go 写不进去，换成临时目录里跑
TMPD="tests/.tmp_fullstack"
rm -rf "$TMPD"; mkdir -p "$TMPD"
sed -i "s|/tmp/fs_全栈.db|$TMPD/t.db|g" /tmp/fs_db.saho
check "数据库全流程" "0
1
小明
小红
1
重开 1 表 1 行" "$("$SAHOU" run /tmp/fs_db.saho 2>&1 | head -20)"
rm -rf "$TMPD"

# ---------- 2. 网页模块 ----------
cat > /tmp/fs_html.saho <<'EOF'
页 = 网页.渲染("<b>[[名]]</b>", {"名": "<i>小<span>明</i>"})
print(页)
print(网页.渲染("[[[名]]]", {"名": "<i>原样</i>"}))
print(网页.转义("<a href=\"x\">&</a>"))
EOF
out=$("$SAHOU" run /tmp/fs_html.saho 2>&1)
check "渲染转义" "<b>&lt;i&gt;小&lt;span&gt;明&lt;/i&gt;</b>" "$(echo "$out" | sed -n 1p)"
check "渲染原样" "<i>原样</i>" "$(echo "$out" | sed -n 2p)"
check "转义函数" "&lt;a href=&#34;x&#34;&gt;&amp;&lt;/a&gt;" "$(echo "$out" | sed -n 3p)"

# ---------- 3. 留言板端到端：表单 + 会话 + 数据库 + HTML ----------
rm -f 留言板.db
"$SAHOU" run examples/留言板.saho >/dev/null 2>&1 &
SRV=$!
sleep 1.5
home=$(curl -s http://localhost:8080/)
case "$home" in
  *"<h1>留言板</h1>"*) pass=$((pass+1));;
  *) fail=$((fail+1)); echo "FAIL 首页渲染"; echo "  实际: $首页";;
esac
# 表单提交（UTF-8 文件绕开 argv 编码转换）
printf '名字=%%E5%%B0%%8F%%E6%%98%%8E&内容=%%E4%%BD%%A0%%E5%%A5%%BD' > /tmp/fs_form.txt
curl -s -c /tmp/fs_cookie.txt -X POST http://localhost:8080/%E5%8F%91%E7%95%99%E8%A8%80 --data-binary @/tmp/fs_form.txt >/dev/null
# 再带会话 Cookie 访问首页：应回显名字（会话生效）且回显留言（数据库生效）
page=$(curl -s -b /tmp/fs_cookie.txt http://localhost:8080/)
case "$page" in
  *"value='小明'"*) pass=$((pass+1));;
  *) fail=$((fail+1)); echo "FAIL 会话回显名字"; echo "  实际: $page";;
esac
case "$page" in
  *"<b>小明</b>：你好"*) pass=$((pass+1));;
  *) fail=$((fail+1)); echo "FAIL 留言入库并显示"; echo "  实际: $page";;
esac
kill $SRV 2>/dev/null
wait $SRV 2>/dev/null

echo "全栈模块: 通过 $pass / $((pass+fail))"
[ "$fail" = 0 ]
