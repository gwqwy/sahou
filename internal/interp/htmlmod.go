// v1.6 网页模块（服务端渲染）：内置模块 网页/html。
// PHP 的 <?php echo ... ?> 混写模板在 sahou 里的对应物：
// 网页.渲染(模板文本, 数据字典)，模板里 [[名]] 取值并自动 HTML 转义，[[[名]]] 不转义。
// （不用 {{ }} 是因为语言字符串插值已占用花括号。）
package interp

import (
	"os"
	"strings"

	"sahou/internal/errs"
)

func htmlMod() *Dict {
	m := newMod("网页", "html")
	m.fn("转义", "escape", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "网页", "转义", line)
		if e != nil {
			return nil, e
		}
		return htmlEscape(s), nil
	})
	m.fn("渲染", "render", [][]string{{"模板!"}, {"数据"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		tmpl, e := needTextArg(args, 0, "网页", "渲染", line)
		if e != nil {
			return nil, e
		}
		data := NewDict()
		if a, ok := opt(args, 1); ok {
			if d, ok2 := a.(*Dict); ok2 {
				data = d
			} else {
				return nil, errs.RuntimeHint(
					"渲染 的第二个参数要是字典，收到的是"+TypeName(a)+"。",
					"render's second argument must be a dict, got "+TypeName(a)+".",
					"例如：网页.渲染(模板, {\"名字\": \"小明\"})。", line)
			}
		}
		return htmlRender(tmpl, data, line)
	})
	// v1.7 写页面：模板放进 .html 文件（设计与代码分离），页面() 套 HTML 骨架
	m.fn("渲染文件", "render_file", [][]string{{"路径!"}, {"数据"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		path, e := needTextArg(args, 0, "网页", "渲染文件", line)
		if e != nil {
			return nil, e
		}
		data := NewDict()
		if a, ok := opt(args, 1); ok {
			if d, ok2 := a.(*Dict); ok2 {
				data = d
			}
		}
		src, rerr := os.ReadFile(in.ScriptPath(path))
		if rerr != nil {
			return nil, errs.RuntimeHint(
				"读不了模板文件 "+path+"。", "cannot read the template file "+path+".",
				"相对路径按脚本所在目录算。", line)
		}
		return htmlRender(string(src), data, line)
	})
	m.fn("页面", "page", [][]string{{"标题!"}, {"内容!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		title, e := needTextArg(args, 0, "网页", "页面", line)
		if e != nil {
			return nil, e
		}
		body, e := needTextArg(args, 1, "网页", "页面", line)
		if e != nil {
			return nil, e
		}
		return "<!doctype html><html><head><meta charset='utf-8'>" +
			"<meta name='viewport' content='width=device-width, initial-scale=1'>" +
			"<title>" + htmlEscape(title) + "</title></head><body>" +
			body + "</body></html>", nil
	})
	return m.d
}

// htmlEscape 把文本里的 HTML 特殊字符转义（防注入，默认行为）。
func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

// htmlRender 模板语法：[[名]] 转义取值；[[[名]]] 原样取值（写可信内容时用）。
// 取不到的键报错并列出现有键（与字典下标同一风格）。
func htmlRender(tmpl string, data *Dict, line int) (Value, *errs.Error) {
	var sb strings.Builder
	rest := tmpl
	for {
		i := strings.Index(rest, "[[")
		if i < 0 {
			sb.WriteString(rest)
			break
		}
		sb.WriteString(rest[:i])
		rest = rest[i:]
		raw := strings.HasPrefix(rest, "[[[")
		if raw {
			end := strings.Index(rest[3:], "]]]")
			if end < 0 {
				return nil, errs.RuntimeHint(
					"模板里有 [[[ 没有对应的 ]]]。", "the template has [[[ without a matching ]]].",
					"每个取值都要收尾，例如 [[[名字]]]。", line)
			}
			key := strings.TrimSpace(rest[3 : 3+end])
			v, e := lookupKey(data, key, line)
			if e != nil {
				return nil, e
			}
			sb.WriteString(Str(v))
			rest = rest[3+end+3:]
			continue
		}
		end := strings.Index(rest[2:], "]]")
		if end < 0 {
			return nil, errs.RuntimeHint(
				"模板里有 [[ 没有对应的 ]]。", "the template has [[ without a matching ]].",
				"每个取值都要收尾，例如 [[名字]]。", line)
		}
		key := strings.TrimSpace(rest[2 : 2+end])
		v, e := lookupKey(data, key, line)
		if e != nil {
			return nil, e
		}
		s := Str(v)
		s = htmlEscape(s)
		sb.WriteString(s)
		rest = rest[2+end+2:]
	}
	return sb.String(), nil
}

// lookupKey 在数据字典里取键；支持 点 分层的嵌套字典（user.name）。
func lookupKey(data *Dict, key string, line int) (Value, *errs.Error) {
	parts := strings.Split(key, ".")
	d := data
	for i, p := range parts {
		v, ok := d.Get("s:" + p)
		if !ok {
			path := strings.Join(parts[:i+1], ".")
			cands := displayKeys(d)
			hint := "数据字典里现有的键有：" + strings.Join(cands, "、") + "。"
			return nil, errs.RuntimeHint(
				"模板要取 \""+path+"\"，但数据里没有这个键。",
				"the template references \""+path+"\", but the data has no such key.",
				hint, line)
		}
		if i == len(parts)-1 {
			return v, nil
		}
		next, ok := v.(*Dict)
		if !ok {
			p := strings.Join(parts[:i+1], ".")
			return nil, errs.RuntimeHint(
				"模板取 \""+p+"\" 时，中间的 \""+parts[i]+"\" 不是字典。",
				"the template path \""+p+"\" goes through a non-dict value.",
				"嵌套取值只能一层一层穿过字典，例如 用户.名字。", line)
		}
		d = next
	}
	return nil, nil
}

// registerHTMLModule 在 New() 里调用（由 registerNetModule 顺带注册亦可，这里独立便于阅读）。
func registerHTMLModule(in *Interp) {
	m := htmlMod()
	in.Globals.Set("网页", m)
	in.Globals.Set("html", m)
	in.builtinMods["网页"] = m
	in.builtinMods["html"] = m
}
