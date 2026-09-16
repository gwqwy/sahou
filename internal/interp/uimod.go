// 写页面：纯代码构造图形界面 —— Java Swing / Python Tkinter 的 卅 对应物。
//
//	页 = 应用.写页面("标题", 宽: 420, 高: 320)
//	问句 = 页.标签("3 × 4 = ?")
//	页.按钮("检查", 函数() … 完毕)
//	页.显示()
//
// Windows 下在 WebView2 原生窗口里运行，事件（点击/回车/勾选/选择）直接调用 卅 函数；
// 其他平台退回浏览器展示静态页面（事件不可用）。页.到HTML() 生成完整网页文本，
// 可以交给 网络.网页回应 做服务端页面（结构代码化，无需模板文件）。
package interp

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"sahou/internal/errs"
)

// UIPage 一个纯代码页面。对用户呈现为"一个普通值"，成员（标签/按钮/显示…）是 BoundMember。
type UIPage struct {
	in      *Interp
	mu      sync.Mutex
	title   string
	w, h    int
	widgets []*UIWidget
	seq     int
	wv      uiWebView // 显示 后才有：往窗口里注入 JS
}

// uiWebView 原生窗口的窄接口（避免本文件依赖平台相关的 webview 类型）。
type uiWebView interface {
	Eval(js string)
}

// uiWindowSpec 显示一个纯代码页面的全部所需（平台实现在 nativewin_*.go）。
type uiWindowSpec struct {
	title   string
	w, h    int
	html    string
	onEvent func(payload string) string // 用户事件（点击/回车/勾选/选择）
	onSync  func(payload string)        // 输入镜像同步（不触发事件函数）
	onReady func(uiWebView)             // 窗口就绪：此后 Eval 可用
}

// UIWidget 一个控件。
type UIWidget struct {
	id      string
	kind    string // 标签 / 按钮 / 输入框 / 复选 / 下拉 / 行
	text    string
	pholder string
	options []string
	handler Value  // 事件函数（按钮 0 参；输入框/复选/下拉 1 参）
	value   string // 输入框/下拉 的当前值镜像（页面 JS 同步回来）
	checked bool   // 复选 的勾选状态镜像
	kids    []*UIWidget
	page    *UIPage
}

// ---------- 创建：应用.写页面 ----------

func uiNewPage(in *Interp, args []Value, line int) (Value, *errs.Error) {
	title, e := needTextArg(args, 0, "应用", "写页面", line)
	if e != nil {
		return nil, e
	}
	p := &UIPage{in: in, title: title, w: 420, h: 320}
	if _, ok := opt(args, 1); ok {
		n, e := needIntMod(args, 1, "应用", "写页面", line)
		if e != nil {
			return nil, e
		}
		p.w = n
	}
	if _, ok := opt(args, 2); ok {
		n, e := needIntMod(args, 2, "应用", "写页面", line)
		if e != nil {
			return nil, e
		}
		p.h = n
	}
	return p, nil
}

// add 登记一个新控件并分配编号。
func (p *UIPage) add(kind string) *UIWidget {
	p.seq++
	wd := &UIWidget{id: fmt.Sprintf("件%d", p.seq), kind: kind, page: p}
	p.widgets = append(p.widgets, wd)
	return wd
}

// ---------- 页面成员 ----------

var uiPageMembers = map[string]*memberDef{}

func init() {
	for _, pair := range [][2]string{
		{"标题", "title"}, {"标签", "label"}, {"按钮", "button"},
		{"输入框", "input"}, {"复选", "checkbox"}, {"下拉", "select"},
		{"行", "row"}, {"弹窗", "alert"}, {"显示", "show"}, {"到HTML", "to_html"},
	} {
		uiPageMembers[pair[0]] = &memberDef{zh: pair[0], en: pair[1], fn: nil}
		uiPageMembers[pair[1]] = uiPageMembers[pair[0]]
	}
	uiPageMembers["标题"].fn = uiPageTitle
	uiPageMembers["标签"].fn = uiPageLabel
	uiPageMembers["按钮"].fn = uiPageButton
	uiPageMembers["输入框"].fn = uiPageInput
	uiPageMembers["复选"].fn = uiPageCheck
	uiPageMembers["下拉"].fn = uiPageSelect
	uiPageMembers["行"].fn = uiPageRow
	uiPageMembers["弹窗"].fn = uiPageAlert
	uiPageMembers["显示"].fn = uiPageShow
	uiPageMembers["到HTML"].fn = uiPageToHTML
}

func uiPageTitle(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	t, e := needTextArg(args, 0, "页面", "标题", line)
	if e != nil {
		return nil, e
	}
	p.mu.Lock()
	p.title = t
	p.mu.Unlock()
	return nil, nil
}

func uiPageLabel(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	t, e := needTextArg(args, 0, "页面", "标签", line)
	if e != nil {
		return nil, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wd := p.add("标签")
	wd.text = t
	return wd, nil
}

func uiPageButton(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	t, e := needTextArg(args, 0, "页面", "按钮", line)
	if e != nil {
		return nil, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wd := p.add("按钮")
	wd.text = t
	if h, ok := opt(args, 1); ok {
		if e := uiCheckHandler(h, 0, "按钮", "点击函数不要参数：函数() … 完毕。", line); e != nil {
			return nil, e
		}
		wd.handler = h
	}
	return wd, nil
}

func uiPageInput(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	ph := ""
	if a, ok := opt(args, 0); ok {
		s, ok2 := a.(string)
		if !ok2 {
			return nil, errs.RuntimeHint("输入框 的占位提示要是一段文本。", "the input placeholder must be a string.", "", line)
		}
		ph = s
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wd := p.add("输入框")
	wd.pholder = ph
	if h, ok := opt(args, 1); ok {
		if e := uiCheckHandler(h, 1, "输入框", "回车函数要有 1 个参数（当前内容）：函数(内容) … 完毕。", line); e != nil {
			return nil, e
		}
		wd.handler = h
	}
	return wd, nil
}

func uiPageCheck(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	t, e := needTextArg(args, 0, "页面", "复选", line)
	if e != nil {
		return nil, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wd := p.add("复选")
	wd.text = t
	if h, ok := opt(args, 1); ok {
		if e := uiCheckHandler(h, 1, "复选", "勾选函数要有 1 个参数（真/假）：函数(勾了吗) … 完毕。", line); e != nil {
			return nil, e
		}
		wd.handler = h
	}
	return wd, nil
}

func uiPageSelect(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	lst, ok := opt(args, 0)
	if !ok {
		return nil, errs.RuntimeHint("下拉 要给一个选项列表。", "select needs a list of options.",
			"例如：页.下拉([\"小杯\", \"中杯\", \"大杯\"], 函数(选中项) … 完毕)。", line)
	}
	l, ok2 := lst.(*List)
	if !ok2 {
		return nil, errs.RuntimeHint("下拉 的选项要是一个列表，收到的是"+TypeName(lst)+"。", "select options must be a list, got "+TypeName(lst)+".", "", line)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wd := p.add("下拉")
	for _, it := range l.Items {
		wd.options = append(wd.options, Str(it))
	}
	if len(wd.options) > 0 {
		wd.value = wd.options[0]
	}
	if h, ok := opt(args, 1); ok {
		if e := uiCheckHandler(h, 1, "下拉", "选择函数要有 1 个参数（选中项）：函数(选中项) … 完毕。", line); e != nil {
			return nil, e
		}
		wd.handler = h
	}
	return wd, nil
}

func uiPageRow(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	if len(args) == 0 {
		return nil, errs.RuntimeHint("行 里至少要放一个控件。", "a row needs at least one widget.",
			"例如：页.行(页.标签(\"名字：\"), 页.输入框(\"请输入\"))。", line)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wd := p.add("行")
	for i, a := range args {
		kid, ok := a.(*UIWidget)
		if !ok || kid == nil {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("行 的第 %d 个东西不是控件（收到的是%s）。", i+1, TypeName(a)),
				fmt.Sprintf("row argument %d is not a widget (got %s).", i+1, TypeName(a)),
				"控件要用 页.标签(...)/页.按钮(...) 这样创建。", line)
		}
		// 从页面顶层移走，避免既在行里又在顶层出现两份
		for j, top := range p.widgets {
			if top == kid {
				p.widgets = append(p.widgets[:j], p.widgets[j+1:]...)
				break
			}
		}
		wd.kids = append(wd.kids, kid)
	}
	return wd, nil
}

func uiPageAlert(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	t, e := needTextArg(args, 0, "页面", "弹窗", line)
	if e != nil {
		return nil, e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.wv == nil {
		return nil, errs.RuntimeHint(
			"弹窗 要等 页.显示() 之后才能用。", "alert needs the page to be shown first.",
			"把 弹窗 写进事件函数里（点按钮、填内容时），窗口还没打开时不能用。", line)
	}
	p.wv.Eval("alert(" + uiJSON(t) + ")")
	return nil, nil
}

func uiPageShow(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	p.mu.Lock()
	if p.wv != nil {
		p.mu.Unlock()
		return nil, errs.RuntimeHint(
			"这个页面已经在显示了。", "this page is already shown.",
			"显示 会一直等到用户关窗；同一个页面不要 显示 两次。", line)
	}
	html := p.buildHTML()
	p.mu.Unlock()
	spec := uiWindowSpec{
		title: p.title, w: p.w, h: p.h, html: html,
		onEvent: p.onEvent,
		onSync:  p.onSync,
		onReady: func(w uiWebView) {
			p.mu.Lock()
			p.wv = w
			p.mu.Unlock()
		},
	}
	runNativePage(spec) // 阻塞到用户关窗
	p.mu.Lock()
	p.wv = nil
	p.mu.Unlock()
	return nil, nil
}

func uiPageToHTML(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	p := recv.(*UIPage)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buildHTML(), nil
}

// ---------- 控件成员 ----------

var uiWidgetMembers = map[string]*memberDef{}

func init() {
	for _, pair := range [][2]string{
		{"改文本", "set_text"}, {"取文本", "text"}, {"改占位", "set_placeholder"},
		{"选中", "checked"}, {"勾", "set_checked"}, {"选中项", "selected"}, {"改选项", "set_options"},
	} {
		uiWidgetMembers[pair[0]] = &memberDef{zh: pair[0], en: pair[1], fn: nil}
		uiWidgetMembers[pair[1]] = uiWidgetMembers[pair[0]]
	}
	uiWidgetMembers["改文本"].fn = uiWidgetSetText
	uiWidgetMembers["取文本"].fn = uiWidgetGetText
	uiWidgetMembers["改占位"].fn = uiWidgetSetPlaceholder
	uiWidgetMembers["选中"].fn = uiWidgetGetChecked
	uiWidgetMembers["勾"].fn = uiWidgetSetChecked
	uiWidgetMembers["选中项"].fn = uiWidgetGetSelected
	uiWidgetMembers["改选项"].fn = uiWidgetSetOptions
}

func uiWidgetSetText(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	wd := recv.(*UIWidget)
	t, e := needTextArg(args, 0, "控件", "改文本", line)
	if e != nil {
		return nil, e
	}
	p := wd.page
	p.mu.Lock()
	defer p.mu.Unlock()
	switch wd.kind {
	case "标签", "按钮":
		wd.text = t
		p.evalEl(wd.id, "el.textContent = "+uiJSON(t))
	case "复选":
		wd.text = t
		p.evalEl(wd.id+"字", "el.textContent = "+uiJSON(t))
	case "输入框":
		wd.value = t
		p.evalEl(wd.id, "el.value = "+uiJSON(t))
	default:
		return nil, errs.RuntimeHint("行 不能改文本。", "a row has no text.", "", line)
	}
	return nil, nil
}

func uiWidgetGetText(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	wd := recv.(*UIWidget)
	wd.page.mu.Lock()
	defer wd.page.mu.Unlock()
	switch wd.kind {
	case "标签", "按钮", "复选":
		return wd.text, nil
	case "输入框":
		return wd.value, nil
	}
	return nil, errs.RuntimeHint("行 没有文本。", "a row has no text.", "", line)
}

func uiWidgetSetPlaceholder(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	wd := recv.(*UIWidget)
	t, e := needTextArg(args, 0, "控件", "改占位", line)
	if e != nil {
		return nil, e
	}
	p := wd.page
	p.mu.Lock()
	defer p.mu.Unlock()
	if wd.kind != "输入框" {
		return nil, errs.RuntimeHint("只有 输入框 有占位提示。", "only inputs have a placeholder.", "", line)
	}
	wd.pholder = t
	p.evalEl(wd.id, "el.placeholder = "+uiJSON(t))
	return nil, nil
}

func uiWidgetGetChecked(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	wd := recv.(*UIWidget)
	if wd.kind != "复选" {
		return nil, errs.RuntimeHint("只有 复选 能问 选中。", "only checkboxes support checked.", "", line)
	}
	wd.page.mu.Lock()
	defer wd.page.mu.Unlock()
	return wd.checked, nil
}

func uiWidgetSetChecked(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	wd := recv.(*UIWidget)
	if wd.kind != "复选" {
		return nil, errs.RuntimeHint("只有 复选 能 勾。", "only checkboxes support set_checked.", "", line)
	}
	if len(args) < 1 {
		return nil, errs.RuntimeHint("勾 要给 真 或 假。", "set_checked needs true or false.", "", line)
	}
	on := Truthy(args[0])
	p := wd.page
	p.mu.Lock()
	defer p.mu.Unlock()
	wd.checked = on
	p.evalEl(wd.id, "el.checked = "+fmt.Sprintf("%v", on))
	return nil, nil
}

func uiWidgetGetSelected(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	wd := recv.(*UIWidget)
	if wd.kind != "下拉" {
		return nil, errs.RuntimeHint("只有 下拉 能问 选中项。", "only selects support selected.", "", line)
	}
	wd.page.mu.Lock()
	defer wd.page.mu.Unlock()
	return wd.value, nil
}

func uiWidgetSetOptions(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	wd := recv.(*UIWidget)
	if wd.kind != "下拉" {
		return nil, errs.RuntimeHint("只有 下拉 能 改选项。", "only selects support set_options.", "", line)
	}
	lst, ok := opt(args, 0)
	if !ok {
		return nil, errs.RuntimeHint("改选项 要给一个选项列表。", "set_options needs a list of options.", "", line)
	}
	l, ok2 := lst.(*List)
	if !ok2 {
		return nil, errs.RuntimeHint("改选项 的选项要是一个列表，收到的是"+TypeName(lst)+"。", "set_options needs a list, got "+TypeName(lst)+".", "", line)
	}
	p := wd.page
	p.mu.Lock()
	defer p.mu.Unlock()
	wd.options = nil
	for _, it := range l.Items {
		wd.options = append(wd.options, Str(it))
	}
	if len(wd.options) > 0 {
		wd.value = wd.options[0]
	}
	p.evalEl(wd.id, "el.innerHTML = "+uiJSON(uiOptionsHTML(wd.options)))
	return nil, nil
}

// ---------- HTML 生成 ----------

const uiPageCSS = `body{margin:0;font-family:"Microsoft YaHei",system-ui,sans-serif;background:#f5f6f8}
.页{display:flex;flex-direction:column;gap:12px;padding:20px;max-width:720px;margin:0 auto}
.行{display:flex;gap:10px;align-items:center}
.行>*{flex:0 0 auto}
.页标签{font-size:15px;color:#222;line-height:1.5;white-space:pre-wrap}
button{font:inherit;padding:8px 18px;border:none;border-radius:6px;background:#2f6fdb;color:#fff;cursor:pointer}
button:hover{background:#2559b8}
input[type=text]{font:inherit;padding:8px 10px;border:1px solid #ccd2da;border-radius:6px;flex:1}
select{font:inherit;padding:8px;border:1px solid #ccd2da;border-radius:6px}
label.复选框{display:flex;gap:6px;align-items:center;font-size:15px}
`

const uiPageJS = `function 事件(id, v){ window.__saho_event(JSON.stringify({id: id, v: v === undefined ? null : v})); }
function 同步(id, v){ window.__saho_sync(JSON.stringify({id: id, v: v === undefined ? null : v})); }`

func (p *UIPage) buildHTML() string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset='utf-8'>")
	b.WriteString("<title>" + htmlEscape(p.title) + "</title>")
	b.WriteString("<style>" + uiPageCSS + "</style></head><body><div class='页'>")
	for _, wd := range p.widgets {
		b.WriteString(wd.html())
	}
	b.WriteString("</div><script>" + uiPageJS + "</script></body></html>")
	return b.String()
}

func (wd *UIWidget) html() string {
	switch wd.kind {
	case "标签":
		return "<div class='页标签' id='" + wd.id + "'>" + htmlEscape(wd.text) + "</div>"
	case "按钮":
		return "<button id='" + wd.id + "' onclick=\"事件('" + wd.id + "')\">" + htmlEscape(wd.text) + "</button>"
	case "输入框":
		return "<input type='text' id='" + wd.id + "' placeholder='" + htmlAttr(wd.pholder) +
			"' value='" + htmlAttr(wd.value) + "' oninput=\"同步('" + wd.id + "',this.value)\"" +
			" onchange=\"事件('" + wd.id + "',this.value)\"" +
			" onkeydown=\"if(event.key==='Enter'){事件('" + wd.id + "',this.value)}\">"
	case "复选":
		勾 := ""
		if wd.checked {
			勾 = " checked"
		}
		return "<label class='复选框'><input type='checkbox' id='" + wd.id + "'" + 勾 +
			" onchange=\"同步('" + wd.id + "',this.checked);事件('" + wd.id + "',this.checked)\">" +
			"<span id='" + wd.id + "字'>" + htmlEscape(wd.text) + "</span></label>"
	case "下拉":
		return "<select id='" + wd.id + "' onchange=\"同步('" + wd.id + "',this.value);事件('" + wd.id + "',this.value)\">" +
			uiOptionsHTML(wd.options) + "</select>"
	case "行":
		s := "<div class='行' id='" + wd.id + "'>"
		for _, kid := range wd.kids {
			s += kid.html()
		}
		return s + "</div>"
	}
	return ""
}

func uiOptionsHTML(options []string) string {
	var b strings.Builder
	for _, o := range options {
		b.WriteString("<option>" + htmlEscape(o) + "</option>")
	}
	return b.String()
}

// htmlAttr 属性值转义（单引号包裹的属性）。
func htmlAttr(s string) string {
	s = htmlEscape(s)
	return strings.ReplaceAll(s, "'", "&#39;")
}

// uiJSON 把文本变成 JS 字符串字面量（json.Marshal 保证引号与转义正确）。
func uiJSON(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}

// ---------- 事件派发与窗口联动 ----------

// evalEl 对某个控件元素执行一段赋值 JS（显示后有效；须在主线程上调用）。
func (p *UIPage) evalEl(id, expr string) {
	if p.wv != nil {
		p.wv.Eval("var el=document.getElementById('" + id + "');if(el){" + expr + "}")
	}
}

// findWidget 按编号找控件（含 行 里的子控件）。
func (p *UIPage) findWidget(id string) *UIWidget {
	var walk func(list []*UIWidget) *UIWidget
	walk = func(list []*UIWidget) *UIWidget {
		for _, wd := range list {
			if wd.id == id {
				return wd
			}
			if wd.kind == "行" {
				if hit := walk(wd.kids); hit != nil {
					return hit
				}
			}
		}
		return nil
	}
	return walk(p.widgets)
}

// onEvent 原生窗口事件回调（主线程同步调用）：派发给控件的 卅 事件函数。
// 永不上抛：出错转弹窗 + 控制台，保证窗口不崩。
func (p *UIPage) onEvent(payload string) (result string) {
	var m struct {
		ID string          `json:"id"`
		V  json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return "ok"
	}
	p.mu.Lock()
	wd := p.findWidget(m.ID)
	if wd == nil || wd.handler == nil {
		p.mu.Unlock()
		return "ok"
	}
	kind := wd.kind
	handler := wd.handler
	// 同步镜像值并准备实参
	var arg Value
	hasArg := false
	switch kind {
	case "输入框", "下拉":
		var s string
		_ = json.Unmarshal(m.V, &s)
		wd.value = s
		arg, hasArg = s, true
	case "复选":
		var on bool
		_ = json.Unmarshal(m.V, &on)
		wd.checked = on
		arg, hasArg = on, true
	}
	p.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			text := "页面事件出错：\n"
			if sig, ok := r.(errorSig); ok {
				text += errs.Format(sig.e)
			} else {
				text += fmt.Sprintf("%v", r)
			}
			p.mu.Lock()
			wv := p.wv
			p.mu.Unlock()
			if wv != nil {
				wv.Eval("alert(" + uiJSON(text) + ")")
			}
			fmt.Fprintln(p.in.Stderr, text)
			result = "ok"
		}
	}()
	callUIHandler(p.in, handler, arg, hasArg)
	return "ok"
}

// onSync 输入镜像同步（oninput 等高频事件）：只更新镜像，不触发事件函数。
func (p *UIPage) onSync(payload string) {
	var m struct {
		ID string          `json:"id"`
		V  json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	wd := p.findWidget(m.ID)
	if wd == nil {
		return
	}
	switch wd.kind {
	case "输入框", "下拉":
		var s string
		_ = json.Unmarshal(m.V, &s)
		wd.value = s
	case "复选":
		var on bool
		_ = json.Unmarshal(m.V, &on)
		wd.checked = on
	}
}

// callUIHandler 在解释器锁内调用控件的 事件函数（镜像 netmod 的处理函数调用约定）。
func callUIHandler(in *Interp, fn Value, arg Value, hasArg bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	pos := []Value{}
	if hasArg {
		pos = append(pos, arg)
	}
	switch f := fn.(type) {
	case *SahouFn:
		if hasArg && len(f.Params) == 0 {
			pos = nil // 事件函数不收参数也行：多余的实参丢弃
		}
		in.callSahou(f, pos, nil, nil, 0)
	case *Builtin:
		_, _ = f.Call(in, pos, nil, 0)
	case *BoundMember:
		_, _ = f.Call(in, f.Recv, pos, nil, 0)
	}
}

// uiCheckHandler 校验事件函数：必须是函数，参数个数符合控件要求。
func uiCheckHandler(fn Value, wantParams int, cn, hint string, line int) *errs.Error {
	switch f := fn.(type) {
	case *SahouFn:
		if len(f.Params) != wantParams {
			return errs.RuntimeHint(
				fmt.Sprintf("%s 的函数要有 %d 个参数，这个函数有 %d 个。%s", cn, wantParams, len(f.Params), hint),
				fmt.Sprintf("the %s handler must take %d argument(s), got %d.", cn, wantParams, len(f.Params)),
				hint, line)
		}
		return nil
	case *Builtin, *BoundMember:
		return nil
	}
	return errs.RuntimeHint(
		cn+" 的函数要是一个函数值。", "the "+cn+" handler must be a function.",
		hint, line)
}
