// 写页面（uimod）单元测试：无窗口路径 —— 构造、到HTML、事件派发、镜像同步。
package interp

import (
	"strings"
	"testing"
)

// runUISource 跑一段 卅 源码，返回解释器（供断言全局状态）。
func runUISource(t *testing.T, src string) *Interp {
	t.Helper()
	in := New()
	var buf strings.Builder
	in.Stdout = &buf
	in.Stderr = &buf
	if e := in.RunSource(src); e != nil {
		t.Fatalf("运行失败：%s\n输出：%s", e.Zh, buf.String())
	}
	return in
}

// globalWidget 取全局控件值。
func globalWidget(t *testing.T, in *Interp, name string) *UIWidget {
	t.Helper()
	v, ok := in.Globals.Get(name)
	if !ok {
		t.Fatalf("全局变量 %s 不存在", name)
	}
	wd, ok := v.(*UIWidget)
	if !ok {
		t.Fatalf("%s 不是控件（是 %T）", name, v)
	}
	return wd
}

func TestPageToHTML(t *testing.T) {
	in := runUISource(t, `页 = 应用.写页面("乘法小测", 460, 360)
问句 = 页.标签("3 × 4 = ?")
答案框 = 页.输入框("在这里填答案")
反馈 = 页.标签("")
页.行(问句, 答案框)
函数 检查()
    如果 答案框.取文本() == "12"
        反馈.改文本("√ 答对啦！")
    否则
        反馈.改文本("× 再想想")
    完毕
完毕
页.按钮("检查", 检查)
html = 页.到HTML()
`)
	v, _ := in.Globals.Get("html")
	html, ok := v.(string)
	if !ok {
		t.Fatal("到HTML 没有返回文本")
	}
	for _, want := range []string{"乘法小测", "3 × 4 = ?", "placeholder='在这里填答案'", "class='行'", "检查"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML 缺少 %q", want)
		}
	}
	if got := globalWidget(t, in, "答案框").value; got != "" {
		t.Errorf("输入框初始值应为空，得到 %q", got)
	}
	// 按钮事件已登记
	wd := globalWidget(t, in, "问句")
	if wd.kind != "标签" || wd.text != "3 × 4 = ?" {
		t.Errorf("标签控件状态不对：%q %q", wd.kind, wd.text)
	}
}

func TestPageEventDispatch(t *testing.T) {
	var buf strings.Builder
	in := New()
	in.Stdout = &buf
	in.Stderr = &buf
	if e := in.RunSource(`页 = 应用.写页面("事件测试")
函数 收到(内容)
    结果.改文本("收到 " + 内容)
    打印("已派发:" + 内容)
完毕
答案框 = 页.输入框("占位", 收到)
结果 = 页.标签("")
`); e != nil {
		t.Fatalf("构造失败：%s", e.Zh)
	}
	pv, _ := in.Globals.Get("页")
	p := pv.(*UIPage)
	wd := globalWidget(t, in, "答案框")
	label := globalWidget(t, in, "结果")

	if got := p.onEvent(`{"id": "` + wd.id + `", "v": "12"}`); got != "ok" {
		t.Fatalf("onEvent 返回 %q", got)
	}
	if !strings.Contains(buf.String(), "已派发:12") {
		t.Fatalf("事件函数没有被执行，输出：%s", buf.String())
	}
	if label.text != "收到 12" {
		t.Fatalf("标签文本 = %q，想要 %q", label.text, "收到 12")
	}
	// 输入镜像也应同步为 12
	if wd.value != "12" {
		t.Fatalf("输入镜像 = %q，想要 %q", wd.value, "12")
	}
	// 未知控件号：静默忽略
	if got := p.onEvent(`{"id": "不存在", "v": "x"}`); got != "ok" {
		t.Fatalf("未知控件 onEvent 返回 %q", got)
	}
}

func TestPageSyncMirror(t *testing.T) {
	in := New()
	var buf strings.Builder
	in.Stdout = &buf
	if e := in.RunSource(`页 = 应用.写页面("镜像测试")
答题框 = 页.输入框("占位")
勾选框 = 页.复选("记住我")
`); e != nil {
		t.Fatalf("构造失败：%s", e.Zh)
	}
	pv, _ := in.Globals.Get("页")
	p := pv.(*UIPage)
	wd := globalWidget(t, in, "答题框")
	cb := globalWidget(t, in, "勾选框")

	p.onSync(`{"id": "` + wd.id + `", "v": "你好"}`)
	if wd.value != "你好" {
		t.Fatalf("输入镜像 = %q，想要 %q", wd.value, "你好")
	}
	p.onSync(`{"id": "` + cb.id + `", "v": true}`)
	if !cb.checked {
		t.Fatal("复选镜像应为 真")
	}
	// 同步不触发事件函数
	if buf.String() != "" {
		t.Fatalf("同步不应产生输出，得到 %q", buf.String())
	}
	// 复选成员 选中() 读镜像
	checked, e := uiWidgetGetChecked(nil, cb, nil, nil, 0)
	if e != nil {
		t.Fatalf("选中 报错：%s", e.Zh)
	}
	if checked != true {
		t.Fatal("选中() 应返回 真")
	}
}

func TestPageHandlerArityError(t *testing.T) {
	in := New()
	var buf strings.Builder
	in.Stdout = &buf
	if e := in.RunSource(`页 = 应用.写页面("t")
尝试
    页.按钮("x", 函数(a) => a)
接住 错误信息
    打印("接住:" + 错误信息)
完毕
`); e != nil {
		t.Fatalf("%s", e.Zh)
	}
	if !strings.Contains(buf.String(), "点击函数不要参数") {
		t.Fatalf("没报参数个数错误，输出：%s", buf.String())
	}
}
