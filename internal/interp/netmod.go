// v1.1 网络模块（05 文档第 2 节）：内置模块 网络/net。
// 三步模型：服务 = 网络.服务()；服务.路由(方法, 路径, 函数)；服务.监听(端口)。
// 请求与响应都是普通字典；语言层面保持 v1 的单线程顺序语义，
// 实现上每个 HTTP 请求一个 goroutine，但求值由 Interp.mu 串行化。
package interp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"math/big"

	"sahou/internal/errs"
)

// ---------- 服务对象 ----------

// Server 网络服务：对用户呈现为"一个普通字典"（05 文档 2.2），
// 实现上是原生值，成员 路由/监听 是 BoundMember。
type Server struct {
	routes      map[string]Value // "METHOD path" → 处理函数（后注册覆盖先注册）
	names       []string         // 供报错列出现有路由
	patterns    []patternRoute   // 带路径参数的路由：/文章/:编号
	statics     []staticRoute    // 静态文件路由（前缀 → 本地目录）
	sessMu      sync.Mutex
	sessions    map[string]*Dict // 会话ID → 会话字典（v1.6，PHP $_SESSION 对应物）
	sessionSeen map[string]int64 // 会话ID → 最后活跃时间（Unix 秒，过期清理用）
	sessPath    string           // 会话落盘文件（服务.会话存 设置后启用）
}

const sessionTTL = int64(24 * 3600) // 会话有效期：24 小时无活动即过期

// patternRoute 路径参数路由：段为 "{名}" 时匹配任意一段并捕获。
type patternRoute struct {
	method string
	parts  []string
	key    string // 原始路径，报错用
	fn     Value
}

type staticRoute struct {
	prefix string // URL 前缀，如 "/静态/" 或 "/static/"
	dir    string // 本地目录
}

func newServer() *Server {
	return &Server{routes: map[string]Value{}, sessions: map[string]*Dict{}, sessionSeen: map[string]int64{}}
}

// ---------- 模块注册 ----------

// netModule 构造 网络/net 模块字典：成员全是 Builtin，不占 29 个内置函数名额。
func netModule() *Dict {
	d := NewDict()
	defs := []*builtinDef{
		{zh: "服务", en: "server", params: [][]string{}, fn: netServer},
		{zh: "请求", en: "request", params: [][]string{{"网址!", "url!"}, {"方法", "method"}, {"头", "headers"}, {"体", "body"}}, fn: netRequest},
		{zh: "自文本", en: "json_from", params: [][]string{{"文本!", "text!"}}, fn: netJSONFrom},
		{zh: "到文本", en: "json_to", params: [][]string{{"值!", "value!"}}, fn: netJSONTo},
		// v1.7 快捷回应：直接返回一个完整的响应字典
		{zh: "JSON回应", en: "json_response", params: [][]string{{"值!"}, {"状态", "status"}}, fn: netJSONResp},
		{zh: "网页回应", en: "html_response", params: [][]string{{"体!"}, {"状态", "status"}}, fn: netHTMLResp},
		{zh: "重定向", en: "redirect", params: [][]string{{"网址!"}}, fn: netRedirect},
	}
	defaults := map[string]map[int]Value{
		"请求": {1: "GET", 2: nil, 3: ""},
	}
	_ = defaults
	for _, def := range defs {
		def := def
		b := &Builtin{Zh: def.zh, En: def.en}
		b.Call = func(in *Interp, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
			bound, e := bindArgs(def, args, named, line)
			if e != nil {
				return nil, e
			}
			return def.fn(in, bound, line)
		}
		d.SetNew("s:"+def.zh, b)
		d.SetNew("s:"+def.en, b)
	}

	return d
}

var serverMembers = map[string]*memberDef{}

func init() {
	serverMembers["路由"] = &memberDef{zh: "路由", en: "route", fn: srvRoute}
	serverMembers["route"] = serverMembers["路由"]
	serverMembers["监听"] = &memberDef{zh: "监听", en: "listen", fn: srvListen}
	serverMembers["listen"] = serverMembers["监听"]
	serverMembers["静态"] = &memberDef{zh: "静态", en: "static", fn: srvStatic}
	serverMembers["static"] = serverMembers["静态"]
	serverMembers["会话存"] = &memberDef{zh: "会话存", en: "session_store", fn: srvSessionStore}
	serverMembers["session_store"] = serverMembers["会话存"]
}

// srvSessionStore 服务.会话存(路径)：会话落盘（重启不丢），文件不存在则新建。
func srvSessionStore(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	srv := recv.(*Server)
	pathV, ok := opt(args, 0)
	if !ok {
		return nil, errs.RuntimeHint(
			"会话存 要给一个文件路径。", "session_store needs a file path.",
			"例如：服务.会话存(\"会话.json\")。", line)
	}
	path, ok2 := pathV.(string)
	if !ok2 {
		return nil, errs.RuntimeHint("会话存 的参数要是文本。", "session_store's argument must be text.", "", line)
	}
	srv.sessMu.Lock()
	srv.sessPath = in.ScriptPath(path)
	srv.loadSessionsLocked()
	srv.sessMu.Unlock()
	return nil, nil
}

// srvStatic 服务.静态(前缀, 目录)：把一个 URL 前缀映射到本地目录，直接发文件。
func srvStatic(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	srv := recv.(*Server)
	prefixV, ok1 := opt(args, 0)
	dirV, ok2 := opt(args, 1)
	if !ok1 || !ok2 {
		return nil, errs.RuntimeHint(
			"静态 要两个参数：URL 前缀和本地目录。",
			"static needs two arguments: url prefix and local dir.",
			"例如：服务.静态(\"/静态/\", \"静态文件\")。", line)
	}
	prefix, ok1 := prefixV.(string)
	dir, ok2 := dirV.(string)
	if !ok1 || !ok2 {
		return nil, errs.RuntimeHint(
			"静态 的前缀和目录都要是文本。", "static's prefix and dir must be text.", "", line)
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	srv.statics = append(srv.statics, staticRoute{prefix: prefix, dir: dir})
	return nil, nil
}

// registerNetModule 在 New() 里调用：把 网络/net 放进全局环境。
func registerNetModule(in *Interp) {
	m := netModule()
	in.Globals.Set("网络", m)
	in.Globals.Set("net", m)
	in.builtinMods["网络"] = m
	in.builtinMods["net"] = m
	registerPageModule(in)
	registerHTMLModule(in)
	registerDBModule(in)
	registerAppModule(in)
	registerTestModule(in)
}

// netServer 网络.服务()：创建服务对象
func netServer(in *Interp, args []Value, line int) (Value, *errs.Error) {
	return newServer(), nil
}

// srvRoute 服务.路由(方法, 路径, 函数)
func srvRoute(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	srv := recv.(*Server)
	method, ok1 := opt(args, 0)
	path, ok2 := opt(args, 1)
	handler, ok3 := opt(args, 2)
	if !ok1 || !ok2 || !ok3 {
		return nil, errs.RuntimeHint(
			"路由 要三个参数：方法、路径、处理函数。",
			"route needs three arguments: method, path, handler.",
			"例如：服务.路由(\"GET\", \"/你好\", 处理)。", line)
	}
	m, isText1 := method.(string)
	pth, isText2 := path.(string)
	if !isText1 || !isText2 {
		return nil, errs.RuntimeHint(
			"路由 的方法和路径都要是文本。", "route's method and path must be text.",
			"例如：服务.路由(\"GET\", \"/待办\", 列出)。", line)
	}
	switch handler.(type) {
	case *SahouFn, *Builtin, *BoundMember:
	default:
		return nil, errs.RuntimeHint(
			"路由 的第三个参数要是一个函数，收到的是"+TypeName(handler)+"。",
			"route's third argument must be a function, got "+TypeName(handler)+".",
			"把函数名直接写上（不要加括号）。", line)
	}
	key := strings.ToUpper(m) + " " + pth
	// 路径参数路由：/文章/:编号 —— ":名" 段匹配任意一段并捕获进 请求["参数"]。
	// （不用 {名} 是因为语言字符串插值已占用花括号。）
	if strings.Contains(pth, ":") {
		parts := strings.Split(strings.Trim(pth, "/"), "/")
		bad := ""
		for _, part := range parts {
			if strings.Contains(part, ":") {
				name := strings.TrimPrefix(part, ":")
				if !strings.HasPrefix(part, ":") || name == "" || strings.Contains(name, ":") {
					bad = part
					break
				}
			}
		}
		if bad != "" {
			return nil, errs.RuntimeHint(
				"路径参数要一整段一个：:名字，这里写的是 "+bad+"。",
				"path parameters must cover a whole segment, got "+bad+".",
				"例如：服务.路由(\"GET\", \"/文章/:编号\", 查看)。", line)
		}
		srv.patterns = append(srv.patterns, patternRoute{method: strings.ToUpper(m), parts: parts, key: key, fn: handler})
		return nil, nil
	}
	if _, dup := srv.routes[key]; !dup {
		srv.names = append(srv.names, key)
	}
	srv.routes[key] = handler
	return nil, nil
}

// srvListen 服务.监听(端口, 主机: "127.0.0.1")
func srvListen(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	srv := recv.(*Server)
	port, ok := opt(args, 0)
	if !ok {
		return nil, errs.RuntimeHint(
			"监听 要告诉它端口号。", "listen needs a port number.",
			"例如：服务.监听(8080)。", line)
	}
	portNum, e := intFrom(port, line)
	if e != nil {
		return nil, e
	}
	host := "127.0.0.1"
	if a, ok := opt(args, 1); ok {
		h, isText := a.(string)
		if !isText {
			return nil, errs.RuntimeHint(
				"主机 参数要是一段文本。", "the host parameter must be a string.", "", line)
		}
		host = h
	}
	addr := fmt.Sprintf("%s:%d", host, portNum)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		in.serveHTTP(srv, w, r)
	})
	server := &http.Server{
		Addr: addr, Handler: mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		return nil, errs.RuntimeHint(
			fmt.Sprintf("服务在 %s 上启动失败：%s。", addr, err.Error()),
			fmt.Sprintf("the server failed to start on %s.", addr),
			"通常是端口被占用：换一个端口，或关掉占用它的程序。", line)
	}
	return nil, nil
}

// serveHTTP 把一个 HTTP 请求接给 sahou 处理函数。
func (in *Interp) serveHTTP(srv *Server, w http.ResponseWriter, r *http.Request) {
	// 静态文件：前缀命中则直接发文件（不进 sahou 处理函数）
	for _, st := range srv.statics {
		if strings.HasPrefix(r.URL.Path, st.prefix) {
			rel := strings.TrimPrefix(r.URL.Path, st.prefix)
			http.ServeFile(w, r, filepath.Join(st.dir, filepath.FromSlash(rel)))
			return
		}
	}
	query := NewDict()
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			query.SetNew("s:"+k, vs[0])
		}
	}
	headers := NewDict()
	for k, vs := range r.Header {
		if len(vs) > 0 {
			headers.SetNew("s:"+k, vs[0])
		}
	}
	body, _ := io.ReadAll(r.Body)

	// 表单：POST/PUT 的 application/x-www-form-urlencoded 或 multipart/form-data，
	// 加上查询串里的键（PHP $_REQUEST 的合并语义）
	form := NewDict()
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 && !form.Has("s:"+k) {
			form.SetNew("s:"+k, vs[0])
		}
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") && len(body) > 0 {
		if vals, err := url.ParseQuery(string(body)); err == nil {
			for k, vs := range vals {
				if len(vs) > 0 && !form.Has("s:"+k) {
					form.SetNew("s:"+k, vs[0])
				}
			}
		}
	} else if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err == nil && r.MultipartForm != nil {
			for k, vs := range r.MultipartForm.Value {
				if len(vs) > 0 && !form.Has("s:"+k) {
					form.SetNew("s:"+k, vs[0])
				}
			}
		}
	}

	// Cookie：请求里的 Cookie 变成字典
	cookies := NewDict()
	for _, c := range r.Cookies() {
		cookies.SetNew("s:"+c.Name, c.Value)
	}

	// 会话：凭 Cookie 里的会话号找到（或新建）会话字典（PHP $_SESSION 对应物）
	sessID, session, isNewSession := srv.openSession(r)
	if isNewSession {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sessID, Path: "/", HttpOnly: true})
	}

	// 文件上传：multipart 里的文件存进临时目录，请求字典里给 文件名/路径/大小
	uploads := NewDict()
	if strings.HasPrefix(ct, "multipart/form-data") && r.MultipartForm != nil {
		for field, fhs := range r.MultipartForm.File {
			if len(fhs) == 0 {
				continue
			}
			fh := fhs[0]
			src, err := fh.Open()
			if err != nil {
				continue
			}
			tmp, err := os.CreateTemp("", "sahou-upload-*-"+filepath.Base(fh.Filename))
			if err == nil {
				n, _ := io.Copy(tmp, src)
				tmp.Close()
				info := NewDict()
				info.SetNew("s:文件名", fh.Filename)
				info.SetNew("s:路径", tmp.Name())
				info.SetNew("s:大小", big.NewRat(n, 1))
				uploads.SetNew("s:"+field, info)
			}
			src.Close()
		}
	}

	req := NewDict()
	req.SetNew("s:方法", strings.ToUpper(r.Method))
	req.SetNew("s:路径", r.URL.Path)
	req.SetNew("s:查询", query)
	req.SetNew("s:表单", form)
	req.SetNew("s:头", headers)
	req.SetNew("s:Cookie", cookies)
	req.SetNew("s:会话", session)
	req.SetNew("s:体", string(body))
	req.SetNew("s:参数", NewDict()) // 路径参数（:名字 捕获）
	req.SetNew("s:文件", uploads)   // 上传的文件（multipart）

	key := strings.ToUpper(r.Method) + " " + r.URL.Path
	handler, found := srv.routes[key]
	if !found {
		// 参数路由匹配：/文章/:编号
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		for _, pr := range srv.patterns {
			if pr.method != strings.ToUpper(r.Method) || len(pr.parts) != len(parts) {
				continue
			}
			captured := NewDict()
			ok := true
			for i, pp := range pr.parts {
				if strings.HasPrefix(pp, ":") && len(pp) > 1 {
					captured.SetNew("s:"+pp[1:], parts[i])
				} else if pp != parts[i] {
					ok = false
					break
				}
			}
			if ok {
				handler = pr.fn
				found = true
				req.SetNew("s:参数", captured)
				break
			}
		}
	}
	if !found {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("没有路由 " + key + "\nno route " + key + "\n"))
		return
	}
	resp, e := in.CallHandler(handler, req)
	if e != nil {
		w.WriteHeader(http.StatusInternalServerError)
		msg := errs.Format(e)
		_, _ = w.Write([]byte(msg + "\n"))
		fmt.Fprintln(in.Stderr, msg)
		return
	}
	rd, _ := resp.(*Dict)
	status := 200
	bodyOut := ""
	if rd != nil {
		if v, ok := rd.Get("s:状态"); ok {
			n, e2 := intFrom(v, 0)
			if e2 != nil {
				http.Error(w, "状态 必须是数", 500)
				return
			}
			status = n
		}
		if v, ok := rd.Get("s:头"); ok {
			if hd, ok2 := v.(*Dict); ok2 {
				for _, k := range hd.Keys() {
					if strings.HasPrefix(k, "s:") {
						if vv, ok3 := hd.Get(k); ok3 {
							w.Header().Set(k[2:], Str(vv))
						}
					}
				}
			}
		}
		// 便捷写 Cookie：响应里的 "Cookie" 字典自动转 Set-Cookie
		if v, ok := rd.Get("s:Cookie"); ok {
			if cd, ok2 := v.(*Dict); ok2 {
				for _, k := range cd.Keys() {
					if strings.HasPrefix(k, "s:") {
						if vv, ok3 := cd.Get(k); ok3 {
							http.SetCookie(w, &http.Cookie{Name: k[2:], Value: Str(vv), Path: "/"})
						}
					}
				}
			}
		}
		if v, ok := rd.Get("s:体"); ok {
			s, ok2 := v.(string)
			if !ok2 {
				http.Error(w,
					"响应的 \"体\" 必须是文本，这里给的是 "+TypeName(v)+"。\nthe response \"body\" must be text, but got "+TypeName(v)+".\n提示：想返回 JSON，写 网络.到文本(结果)。",
					500)
				return
			}
			bodyOut = s
		}
	}
	// 会话内容可能被处理函数改过：存回会话库
	srv.saveSession(sessID, session)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(bodyOut))
}

// ---------- 会话（v1.6）----------

const sessionCookie = "sahou_sid"

// openSession 从请求 Cookie 里找会话号，找到就取回会话字典，找不到就新建。
func (srv *Server) openSession(r *http.Request) (id string, sess *Dict, isNew bool) {
	now := time.Now().Unix()
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		srv.sessMu.Lock()
		defer srv.sessMu.Unlock()
		srv.gcSessionsLocked(now)
		if seen, ok := srv.sessionSeen[c.Value]; ok && now-seen <= sessionTTL {
			if d, ok2 := srv.sessions[c.Value]; ok2 {
				srv.sessionSeen[c.Value] = now
				return c.Value, d, false
			}
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		for i := range buf {
			buf[i] = byte(i * 7)
		}
	}
	id = hex.EncodeToString(buf)
	sess = NewDict()
	srv.sessMu.Lock()
	srv.sessions[id] = sess
	srv.sessMu.Unlock()
	return id, sess, true
}

func (srv *Server) saveSession(id string, sess *Dict) {
	srv.sessMu.Lock()
	srv.sessions[id] = sess
	srv.sessionSeen[id] = time.Now().Unix()
	srv.dumpSessionsLocked()
	srv.sessMu.Unlock()
}

// gcSessionsLocked 清理超过 24 小时没活动的会话（调用方持锁）。
func (srv *Server) gcSessionsLocked(now int64) {
	for id, seen := range srv.sessionSeen {
		if now-seen > sessionTTL {
			delete(srv.sessions, id)
			delete(srv.sessionSeen, id)
		}
	}
}

// dumpSessionsLocked 会话落盘（未设路径时是空操作；调用方持锁）。
func (srv *Server) dumpSessionsLocked() {
	if srv.sessPath == "" {
		return
	}
	out := map[string]interface{}{}
	for id, d := range srv.sessions {
		seen := srv.sessionSeen[id]
		items := map[string]interface{}{"_活跃": seen}
		for _, k := range d.Keys() {
			if strings.HasPrefix(k, "s:") {
				if v, ok := d.Get(k); ok {
					items[k[2:]] = dbCellToJSON(v)
				}
			}
		}
		out[id] = items
	}
	data, _ := json.MarshalIndent(out, "", " ")
	_ = os.WriteFile(srv.sessPath, data, 0o644)
}

// loadSessionsLocked 从文件读回会话（调用方持锁）。
func (srv *Server) loadSessionsLocked() {
	data, err := os.ReadFile(srv.sessPath)
	if err != nil {
		return
	}
	var raw map[string]map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return
	}
	now := time.Now().Unix()
	for id, m := range raw {
		seen := int64(0)
		if b, ok := m["_活跃"]; ok {
			_ = json.Unmarshal(b, &seen)
		}
		if now-seen > sessionTTL {
			continue // 过期会话不复活
		}
		d := NewDict()
		for k, cell := range m {
			if k == "_活跃" {
				continue
			}
			d.SetNew("s:"+k, dbCellFromJSON(cell))
		}
		srv.sessions[id] = d
		srv.sessionSeen[id] = seen
	}
}

// CallHandler 从宿主（HTTP goroutine）调用一个 sahou 函数；mu 串行化求值。
func (in *Interp) CallHandler(fn Value, req Value) (resp Value, e *errs.Error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			switch sig := r.(type) {
			case errorSig:
				e = sig.e
			case returnSig:
				e = errs.Runtime("处理函数不能在返回响应前结束程序。", "the handler cannot return from the program.", 0)
			default:
				panic(r)
			}
		}
	}()
	if f, ok := fn.(*SahouFn); ok {
		if len(f.Params) != 1 {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("处理函数要有 1 个参数（请求字典），%s 有 %d 个。", fnName(f), len(f.Params)),
				fmt.Sprintf("the handler must take exactly 1 argument, %s takes %d.", fnName(f), len(f.Params)),
				"例如：函数 处理(请求) … 返回 {\"体\": \"你好\"} 完毕。", 0)
		}
		return in.callSahou(f, []Value{req}, nil, nil, 0), nil
	}
	if b, ok := fn.(*Builtin); ok {
		return b.Call(in, []Value{req}, nil, 0)
	}
	if bm, ok := fn.(*BoundMember); ok {
		return bm.Call(in, bm.Recv, []Value{req}, nil, 0)
	}
	return nil, errs.Runtime("处理函数必须是函数。", "the handler must be a function.", 0)
}

func handlerName(f *SahouFn) string {
	if f.Name == "" {
		return "匿名函数"
	}
	return f.Name
}

// ---------- HTTP 客户端 ----------

func netRequest(in *Interp, args []Value, line int) (Value, *errs.Error) {
	urlV, ok := opt(args, 0)
	if !ok {
		return nil, errs.RuntimeHint(
			"请求 要告诉它网址。", "request needs a url.",
			"例如：网络.请求(\"http://localhost:8080/待办\")。", line)
	}
	url, ok := urlV.(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"网址要是一段文本，收到的是"+TypeName(urlV)+"。",
			"the url must be a string, got "+TypeName(urlV)+".", "", line)
	}
	method := "GET"
	if a, ok := opt(args, 1); ok {
		s, ok2 := a.(string)
		if !ok2 {
			return nil, errs.RuntimeHint(
				"方法 参数要是一段文本。", "the method parameter must be a string.", "", line)
		}
		method = strings.ToUpper(s)
	}
	var bodyReader io.Reader
	if a, ok := opt(args, 3); ok {
		s, ok2 := a.(string)
		if !ok2 {
			return nil, errs.RuntimeHint(
				"体 参数要是一段文本。", "the body parameter must be a string.", "", line)
		}
		if s != "" {
			bodyReader = bytes.NewBufferString(s)
		}
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, errs.RuntimeHint(
			"这个网址造不出请求："+url+"。", "cannot build a request for "+url+".",
			"检查网址写法，例如 \"http://localhost:8080/待办\"。", line)
	}
	if a, ok := opt(args, 2); ok {
		hd, ok2 := a.(*Dict)
		if !ok2 {
			return nil, errs.RuntimeHint(
				"头 参数要是一个字典。", "the headers parameter must be a dict.",
				"例如：网络.请求(网址, 头: {\"Content-Type\": \"application/json\"})。", line)
		}
		for _, k := range hd.Keys() {
			if strings.HasPrefix(k, "s:") {
				if vv, ok3 := hd.Get(k); ok3 {
					req.Header.Set(k[2:], Str(vv))
				}
			}
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errs.RuntimeHint(
			"连不上 "+url+"。", "cannot reach "+url+".",
			"检查服务是否已启动、端口是否正确。", line)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	outHeaders := NewDict()
	for k, vs := range resp.Header {
		if len(vs) > 0 {
			outHeaders.SetNew("s:"+k, vs[0])
		}
	}
	out := NewDict()
	out.SetNew("s:状态", big.NewRat(int64(resp.StatusCode), 1))
	out.SetNew("s:头", outHeaders)
	out.SetNew("s:体", string(bodyBytes))
	return out, nil
}

// ---------- 快捷回应（v1.7 后端写法糖）----------

// netJSONResp 网络.JSON回应(值, 状态: 200)：一步返回 JSON 响应字典。
func netJSONResp(in *Interp, args []Value, line int) (Value, *errs.Error) {
	if argIsOmitted(args[0]) {
		return nil, errs.RuntimeHint("JSON回应 要给一个值。", "json_response needs a value.", "", line)
	}
	var buf bytes.Buffer
	if e := jsonWrite(&buf, args[0], map[*List]bool{}, map[*Dict]bool{}, line); e != nil {
		return nil, e
	}
	status := big.NewRat(200, 1)
	if !argIsOmitted(args[1]) {
		status = args[1].(*big.Rat)
	}
	d := NewDict()
	d.SetNew("s:状态", status)
	d.SetNew("s:头", respHeaders("application/json; charset=utf-8"))
	d.SetNew("s:体", buf.String())
	return d, nil
}

// netHTMLResp 网络.网页回应(体, 状态: 200)：一步返回 HTML 响应字典。
func netHTMLResp(in *Interp, args []Value, line int) (Value, *errs.Error) {
	body, ok := args[0].(string)
	if !ok {
		if argIsOmitted(args[0]) {
			return nil, errs.RuntimeHint("网页回应 要给页面文本。", "html_response needs page text.", "", line)
		}
		body = Str(args[0])
	}
	status := big.NewRat(200, 1)
	if !argIsOmitted(args[1]) {
		status = args[1].(*big.Rat)
	}
	d := NewDict()
	d.SetNew("s:状态", status)
	d.SetNew("s:头", respHeaders("text/html; charset=utf-8"))
	d.SetNew("s:体", body)
	return d, nil
}

// netRedirect 网络.重定向(网址)：302 跳转响应。
func netRedirect(in *Interp, args []Value, line int) (Value, *errs.Error) {
	url, ok := args[0].(string)
	if !ok || argIsOmitted(args[0]) {
		return nil, errs.RuntimeHint("重定向 要给目标网址。", "redirect needs a url.",
			"例如：返回 网络.重定向(\"/\")。", line)
	}
	hd := NewDict()
	hd.SetNew("s:Location", url)
	d := NewDict()
	d.SetNew("s:状态", big.NewRat(302, 1))
	d.SetNew("s:头", hd)
	return d, nil
}

func respHeaders(contentType string) *Dict {
	hd := NewDict()
	hd.SetNew("s:Content-Type", contentType)
	return hd
}

// ---------- JSON 编解码（网络.到文本 / 网络.自文本）----------

func netJSONTo(in *Interp, args []Value, line int) (Value, *errs.Error) {
	if argIsOmitted(args[0]) {
		return nil, errs.RuntimeHint(
			"到文本() 里没有写要转换的值。", "json_to() needs a value.", "", line)
	}
	var buf bytes.Buffer
	if e := jsonWrite(&buf, args[0], map[*List]bool{}, map[*Dict]bool{}, line); e != nil {
		return nil, e
	}
	return buf.String(), nil
}

func jsonWrite(buf *bytes.Buffer, v Value, ls map[*List]bool, ds map[*Dict]bool, line int) *errs.Error {
	switch x := v.(type) {
	case *big.Rat:
		buf.WriteString(FmtNum(x)) // JSON 数字（15 位有效十进制）
	case string:
		data, _ := json.Marshal(x)
		buf.Write(data)
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	case *List:
		if ls[x] {
			return errs.Runtime("值里有自己套自己的结构，转不成 JSON。", "cannot convert self-referencing structure to JSON.", line)
		}
		ls[x] = true
		buf.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				buf.WriteByte(',')
			}
			if e := jsonWrite(buf, it, ls, ds, line); e != nil {
				return e
			}
		}
		buf.WriteByte(']')
		delete(ls, x)
	case *Dict:
		if ds[x] {
			return errs.Runtime("值里有自己套自己的结构，转不成 JSON。", "cannot convert self-referencing structure to JSON.", line)
		}
		ds[x] = true
		buf.WriteByte('{')
		kvs := x.KeysAsValues()
		for i, k := range x.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			key := kvs[i]
			ks, ok := key.(string)
			if !ok {
				ks = FmtNum(key.(*big.Rat)) // 数键转成文本键（JSON 键必须是字符串）
			}
			data, _ := json.Marshal(ks)
			buf.Write(data)
			buf.WriteByte(':')
			if e := jsonWrite(buf, x.m[k], ls, ds, line); e != nil {
				return e
			}
		}
		buf.WriteByte('}')
		delete(ds, x)
	default:
		if _, isSrv := x.(*Server); isSrv {
			return errs.RuntimeHint(
				"服务对象转不成 JSON。", "a server cannot be converted to JSON.", "", line)
		}
		return errs.RuntimeHint(
			"函数转不成 JSON。", "a function cannot be converted to JSON.", "", line)
	}
	return nil
}

func netJSONFrom(in *Interp, args []Value, line int) (Value, *errs.Error) {
	s, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"自文本() 的参数要是一段 JSON 文本，收到的是"+TypeName(args[0])+"。",
			"json_from() needs a string, got "+TypeName(args[0])+".",
			"请求的 体 已是文本，直接传进来：网络.自文本(请求[\"体\"])。", line)
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw interface{}
	err := dec.Decode(&raw)
	v := raw
	if err != nil {
		pos := 0
		if se, isSyn := err.(*json.SyntaxError); isSyn {
			pos = int(se.Offset)
		}
		head := s
		if len(head) > 40 {
			head = head[:40] + "…"
		}
		return nil, errs.RuntimeHint(
			fmt.Sprintf("这段 JSON 在第 %d 个字符附近解析不了。", pos),
			fmt.Sprintf("this JSON cannot be parsed near character %d.", pos),
			"出错位置附近的内容："+head, line)
	}
	return jsonToValue(v), nil
}

// jsonToValue 把 encoding/json 的解码结果转成 sahou 值：
// 对象→字典、数组→列表、null→空值、数字→精确有理数（UseNumber 保证）。
func jsonToValue(v interface{}) Value {
	switch x := v.(type) {
	case nil:
		return nil
	case bool:
		return x
	case string:
		return x
	case json.Number:
		if r, ok := new(big.Rat).SetString(x.String()); ok {
			return r
		}
		if f, _, err := big.ParseFloat(x.String(), 10, 200, big.ToNearestEven); err == nil {
			r2, _ := new(big.Rat).SetString(f.Text('f', -1))
			return r2
		}
		return new(big.Rat)
	case []interface{}:
		items := make([]Value, len(x))
		for i, it := range x {
			items[i] = jsonToValue(it)
		}
		return &List{Items: items}
	case map[string]interface{}:
		d := NewDict()
		for k, val := range x {
			d.SetNew("s:"+k, jsonToValue(val))
		}
		return d
	}
	return nil
}
