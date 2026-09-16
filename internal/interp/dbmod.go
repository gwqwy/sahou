// v1.6 数据库模块：内置模块 数据库/db。
// 纯 Go 嵌入式数据库（零依赖）：SQL 子集 + 单文件 JSON 持久化。
// 对应 PHP + SQLite/PDO 的入门体验：
//
//	库 = 数据库.打开("数据.db")
//	库.执行("建 表 用户 (名字, 年龄)")
//	库.执行("插入 用户 (名字, 年龄) 值 (?, ?)", "小明", 10)
//	名单 = 库.查询("选择 * 出 用户 哪里 年龄 > ? 排序 按 年龄 降序 限 10", 5)
//
// 关键词同时接受英文 SQL（CREATE TABLE / INSERT INTO / VALUES / SELECT / FROM /
// WHERE / ORDER BY / ASC / DESC / LIMIT / UPDATE SET / DELETE FROM / DROP TABLE）。
package interp

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strings"
	"sync"

	"sahou/internal/errs"
)

// ---------- 库对象 ----------

// dbTable 一张表：列名顺序 + 行（每行是按列序的值序列）+ 自增列。
type dbTable struct {
	cols     []string
	autoCols map[string]bool // 自增列：插入不给值（空值）时自动取 全表最大+1
	rows     [][]Value
}

func (t *dbTable) nextAuto(col string) int {
	idx := colIndex(t, col)
	max := 0
	for _, row := range t.rows {
		if idx < len(row) {
			if r, ok := row[idx].(*big.Rat); ok && r.IsInt() {
				if n := int(new(big.Int).Quo(r.Num(), r.Denom()).Int64()); n > max {
					max = n
				}
			}
		}
	}
	return max + 1
}

// Database 数据库对象：对用户呈现为"一个普通字典"（与 网络.服务() 同一风格），
// 成员 执行/查询/表/关闭 是 BoundMember。
type Database struct {
	mu     sync.Mutex
	path   string
	tables map[string]*dbTable
}

var dbMembers = map[string]*memberDef{}

func init() {
	dbMembers["执行"] = &memberDef{zh: "执行", en: "execute", fn: dbExec}
	dbMembers["execute"] = dbMembers["执行"]
	dbMembers["查询"] = &memberDef{zh: "查询", en: "query", fn: dbQuery}
	dbMembers["query"] = dbMembers["查询"]
	dbMembers["表"] = &memberDef{zh: "表", en: "tables", fn: dbTables}
	dbMembers["tables"] = dbMembers["表"]
	dbMembers["关闭"] = &memberDef{zh: "关闭", en: "close", fn: dbClose}
	dbMembers["close"] = dbMembers["关闭"]
}

// ---------- 模块注册 ----------

func dbModule() *Dict {
	m := newMod("数据库", "db")
	m.fn("打开", "open", [][]string{{"路径!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		path, e := needTextArg(args, 0, "数据库", "打开", line)
		if e != nil {
			return nil, e
		}
		return openDatabase(path, line)
	})
	return m.d
}

func registerDBModule(in *Interp) {
	m := dbModule()
	in.Globals.Set("数据库", m)
	in.Globals.Set("db", m)
	in.builtinMods["数据库"] = m
	in.builtinMods["db"] = m
}

// openDatabase 打开（或新建）一个数据库文件。
func openDatabase(path string, line int) (Value, *errs.Error) {
	db := &Database{path: path, tables: map[string]*dbTable{}}
	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		var raw map[string]json.RawMessage
		if jerr := json.Unmarshal(data, &raw); jerr != nil {
			return nil, errs.RuntimeHint(
				path+" 不是有效的数据库文件。", path+" is not a valid database file.",
				"换一个新文件名，或删掉坏文件重建。", line)
		}
		for name, blob := range raw {
			var skeleton struct {
				Cols []string            `json:"列"`
				Auto []string            `json:"自增"`
				Rows [][]json.RawMessage `json:"行"`
			}
			if jerr := json.Unmarshal(blob, &skeleton); jerr != nil {
				return nil, errs.RuntimeHint(
					path+" 里的表 "+name+" 读不出来。", "cannot read table "+name+" from "+path+".", "", line)
			}
			t := &dbTable{cols: skeleton.Cols, autoCols: map[string]bool{}}
			for _, a := range skeleton.Auto {
				t.autoCols[a] = true
			}
			for _, rawRow := range skeleton.Rows {
				row := make([]Value, len(t.cols))
				for i, cell := range rawRow {
					if i >= len(t.cols) {
						break
					}
					row[i] = dbCellFromJSON(cell)
				}
				t.rows = append(t.rows, row)
			}
			db.tables[name] = t
		}
	}
	return db, nil
}

// ---------- JSON 单元格编解码（数用字符串保精确，避免浮点失真）----------

func dbCellToJSON(v Value) interface{} {
	switch x := v.(type) {
	case *big.Rat:
		return map[string]interface{}{"数": x.RatString()}
	case string:
		return map[string]interface{}{"文": x}
	case bool:
		return map[string]interface{}{"布": x}
	}
	return nil
}

func dbCellFromJSON(cell json.RawMessage) Value {
	var tagged map[string]json.RawMessage
	if json.Unmarshal(cell, &tagged) != nil || len(tagged) != 1 {
		return nil
	}
	for k, blob := range tagged {
		switch k {
		case "数":
			var s string
			_ = json.Unmarshal(blob, &s)
			if r, ok := new(big.Rat).SetString(s); ok {
				return r
			}
			return new(big.Rat)
		case "文":
			var s string
			_ = json.Unmarshal(blob, &s)
			return s
		case "布":
			var b bool
			_ = json.Unmarshal(blob, &b)
			return b
		}
	}
	return nil
}

// save 落盘：整个库写成一份 JSON（每次写操作后调用）。
func (db *Database) save() *errs.Error {
	out := map[string]interface{}{}
	for name, t := range db.tables {
		rows := make([][]interface{}, len(t.rows))
		for i, row := range t.rows {
			cells := make([]interface{}, len(row))
			for j, v := range row {
				cells[j] = dbCellToJSON(v)
			}
			rows[i] = cells
		}
		tbl := map[string]interface{}{"列": t.cols, "行": rows}
		if len(t.autoCols) > 0 {
			autos := []string{}
			for _, c := range t.cols {
				if t.autoCols[c] {
					autos = append(autos, c)
				}
			}
			tbl["自增"] = autos
		}
		out[name] = tbl
	}
	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return errs.Runtime("数据库写盘失败："+err.Error(), "failed to write the database file.", 0)
	}
	// 原子落盘：先写临时文件再改名，进程被杀不会留下半个文件
	tmp := db.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return errs.Runtime("数据库写不进 "+db.path+"："+err.Error(), "cannot write "+db.path+".", 0)
	}
	if err := os.Rename(tmp, db.path); err != nil {
		_ = os.Remove(tmp)
		return errs.Runtime("数据库落盘改名失败："+db.path+"："+err.Error(), "cannot commit "+db.path+".", 0)
	}
	return nil
}

// ---------- 成员函数 ----------

// dbExec 库.执行(sql, 参数...)：建表/插入/更新/删行/删表，返回受影响行数。
func dbExec(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	db := recv.(*Database)
	if len(args) < 1 {
		return nil, errs.RuntimeHint(
			"执行 要先给一条 SQL 语句。", "execute needs an SQL statement.",
			"例如：库.执行(\"插入 用户 (名字) 值 (?)\", \"小明\")。", line)
	}
	sqlText, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"执行 的第一个参数要是 SQL 文本，收到的是"+TypeName(args[0])+"。",
			"execute's first argument must be SQL text.", "", line)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	stmt, e := parseSQL(sqlText, line)
	if e != nil {
		return nil, e
	}
	if stmt.kind == "选择" {
		return nil, errs.RuntimeHint(
			"选择语句请用 库.查询(...)。", "use query() for SELECT statements.", "", line)
	}
	binder := &paramBinder{params: args[1:], line: line}
	affected, e := stmt.run(db, binder)
	if e != nil {
		return nil, e
	}
	if e := db.save(); e != nil {
		return nil, e
	}
	return big.NewRat(int64(affected), 1), nil
}

// dbQuery 库.查询(sql, 参数...)：选择语句，返回字典列表。
func dbQuery(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	db := recv.(*Database)
	if len(args) < 1 {
		return nil, errs.RuntimeHint(
			"查询 要先给一条 SQL 语句。", "query needs an SQL statement.",
			"例如：库.查询(\"选择 * 出 用户\")。", line)
	}
	sqlText, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"查询 的第一个参数要是 SQL 文本。", "query's first argument must be SQL text.", "", line)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	stmt, e := parseSQL(sqlText, line)
	if e != nil {
		return nil, e
	}
	if stmt.kind != "选择" {
		return nil, errs.RuntimeHint(
			"查询 只能跑 选择/SELECT 语句，写操作请用 库.执行(...)。",
			"query only runs SELECT; use execute for write statements.", "", line)
	}
	binder := &paramBinder{params: args[1:], line: line}
	rows, e := stmt.selectRows(db, binder)
	if e != nil {
		return nil, e
	}
	items := make([]Value, len(rows))
	for i, row := range rows {
		d := NewDict()
		for j, col := range stmt.selectCols {
			d.SetNew("s:"+col, row[j])
		}
		items[i] = d
	}
	return &List{Items: items}, nil
}

// dbTables 库.表()：列出所有表名。
func dbTables(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	db := recv.(*Database)
	db.mu.Lock()
	defer db.mu.Unlock()
	names := make([]string, 0, len(db.tables))
	for n := range db.tables {
		names = append(names, n)
	}
	sort.Strings(names)
	items := make([]Value, len(names))
	for i, n := range names {
		items[i] = n
	}
	return &List{Items: items}, nil
}

// dbClose 库.关闭()：显式落盘收尾（写操作本来就即时落盘）。
func dbClose(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	db := recv.(*Database)
	db.mu.Lock()
	defer db.mu.Unlock()
	if e := db.save(); e != nil {
		return nil, e
	}
	return nil, nil
}

// ---------- SQL 记号器 ----------

type sqlTok struct {
	kind string // "词" 标识符/关键词、"数"、"文本"、"符号"、"参"（?）
	text string // 标识符原文（保留大小写与中文）
	val  Value  // 数/文本字面量
}

func sqlTokenize(s string, line int) ([]sqlTok, *errs.Error) {
	var toks []sqlTok
	r := []rune(s)
	i := 0
	for i < len(r) {
		c := r[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		switch {
		case c == '?':
			toks = append(toks, sqlTok{kind: "参"})
			i++
		case isIdentRune(c):
			j := i
			for j < len(r) && isIdentRune(r[j]) {
				j++
			}
			word := string(r[i:j])
			if c >= '0' && c <= '9' {
				if num, ok := new(big.Rat).SetString(word); ok {
					toks = append(toks, sqlTok{kind: "数", val: num})
					i = j
					continue
				}
			}
			toks = append(toks, sqlTok{kind: "词", text: word})
			i = j
		case c == '\'' || c == '"':
			quote := c
			j := i + 1
			var sb strings.Builder
			closed := false
			for j < len(r) {
				if r[j] == '\\' && j+1 < len(r) {
					sb.WriteRune(r[j+1])
					j += 2
					continue
				}
				if r[j] == quote {
					closed = true
					j++
					break
				}
				sb.WriteRune(r[j])
				j++
			}
			if !closed {
				return nil, errs.RuntimeHint(
					"SQL 里的文本少了收尾引号。", "an SQL string literal is missing its closing quote.", "", line)
			}
			toks = append(toks, sqlTok{kind: "文本", val: sb.String()})
			i = j
		case c == '<' || c == '>' || c == '!' || c == '=':
			two := ""
			if i+1 < len(r) {
				two = string(r[i : i+2])
			}
			if two == ">=" || two == "<=" || two == "!=" || two == "<>" {
				toks = append(toks, sqlTok{kind: "符号", text: two})
				i += 2
			} else {
				toks = append(toks, sqlTok{kind: "符号", text: string(c)})
				i++
			}
		case c == '(' || c == ')' || c == ',' || c == '*':
			toks = append(toks, sqlTok{kind: "符号", text: string(c)})
			i++
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		default:
			return nil, errs.RuntimeHint(
				"SQL 里有认不出的字符："+string(c)+"。",
				"unexpected character in SQL: "+string(c)+".", "", line)
		}
	}
	return toks, nil
}

func isIdentRune(c rune) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c >= 0x80 // 中文等非 ASCII 标识符
}

// ---------- SQL 语句与条件 ----------

type dbAgg struct {
	fn  string // 数 / 和 / 平均 / 最大 / 最小
	col string // * 或列名
}

type dbStmt struct {
	kind       string  // 建 / 删表 / 插入 / 选择 / 更新 / 删行
	aggs       []dbAgg // 选择语句的聚合项（有聚合时输出单行）
	table      string
	ifNotHave  bool // 建表 如无；删表 如有
	cols       []string
	valueRows  [][]dbOperand // 插入：多行，每行按列序
	selectCols []string      // 查询输出列（* 展开后回填）
	orderBy    string
	orderDesc  bool
	limit      int
	hasLimit   bool
	sets       []dbSet // 更新：列 = 值
	where      dbCond  // 条件树；kind=="" 表示无条件
}

type dbSet struct {
	col string
	val dbOperand
}

// dbCond 条件：比较 或 逻辑组合。
type dbCond struct {
	op   string // = != > < >= <=
	l, r dbOperand
	lop  string // 和 / 或 / 非
	a, b *dbCond
}

// dbOperand 比较/赋值的一侧：列名、参数（?）、字面量。
type dbOperand struct {
	kind  string // "列" "参" "值"
	col   string
	val   Value
	param int // 第几个 ?（从 0 开始，解析期编好号）
}

// paramBinder 按编号绑定 ? 与实参（编号在解析期确定，逐行求值不重复计数）。
type paramBinder struct {
	params []Value
	line   int
}

func (pb *paramBinder) at(i int) (Value, *errs.Error) {
	if i >= len(pb.params) {
		return nil, errs.RuntimeHint(
			fmt.Sprintf("语句里的 ? 比给的参数多（用到第 %d 个 ?，只给了 %d 个参数）。", i+1, len(pb.params)),
			"not enough parameters for the ? placeholders.",
			"每个 ? 都要按顺序给一个值。", pb.line)
	}
	return pb.params[i], nil
}

// ---------- SQL 分析器（递归下降）----------

type sqlParser struct {
	toks     []sqlTok
	pos      int
	line     int
	paramSeq int   // ? 的个数（解析期编号）
	agg      dbAgg // tryAggregate 命中时回传
}

var aggNames = map[string]string{
	"数": "数", "count": "数",
	"和": "和", "sum": "和",
	"平均": "平均", "avg": "平均",
	"最大": "最大", "max": "最大",
	"最小": "最小", "min": "最小",
}

// tryAggregate 尝试吃掉一个聚合项：数(*) / 和(列) / 平均(列) / 最大(列) / 最小(列)。
func (p *sqlParser) tryAggregate() (string, bool) {
	if p.pos+1 >= len(p.toks) {
		return "", false
	}
	w := p.toks[p.pos]
	open := p.toks[p.pos+1]
	if w.kind != "词" || open.kind != "符号" || open.text != "(" {
		return "", false
	}
	fn, isAgg := aggNames[strings.ToLower(w.text)]
	if !isAgg {
		fn, isAgg = aggNames[w.text]
	}
	if !isAgg {
		return "", false
	}
	save := p.pos
	p.pos += 2
	col := "*"
	if !p.eatSym("*") {
		c, e := p.ident("列名")
		if e != nil {
			p.pos = save
			return "", false
		}
		col = c
	}
	if !p.eatSym(")") {
		p.pos = save
		return "", false
	}
	p.agg = dbAgg{fn: fn, col: col}
	return fn, true
}

func (p *sqlParser) nextParam() int {
	n := p.paramSeq
	p.paramSeq++
	return n
}

func parseSQL(s string, line int) (*dbStmt, *errs.Error) {
	toks, e := sqlTokenize(s, line)
	if e != nil {
		return nil, e
	}
	if len(toks) == 0 {
		return nil, errs.RuntimeHint("SQL 语句是空的。", "the SQL statement is empty.", "", line)
	}
	p := &sqlParser{toks: toks, line: line}
	return p.parse()
}

func (p *sqlParser) errSyntax(msg string) *errs.Error {
	near := ""
	if p.pos < len(p.toks) {
		t := p.toks[p.pos]
		if t.text != "" {
			near = t.text
		} else {
			near = Str(t.val)
		}
	}
	return errs.RuntimeHint(msg, "SQL syntax error.", "出错位置附近："+near, p.line)
}

func (p *sqlParser) peek() (sqlTok, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos], true
	}
	return sqlTok{}, false
}

func (p *sqlParser) next() (sqlTok, bool) {
	t, ok := p.peek()
	if ok {
		p.pos++
	}
	return t, ok
}

// eatWord 吃掉一个关键词（中文或英文，英文不区分大小写）。
func (p *sqlParser) eatWord(words ...string) bool {
	if t, ok := p.peek(); ok && t.kind == "词" {
		low := strings.ToLower(t.text)
		for _, w := range words {
			if t.text == w || low == w {
				p.pos++
				return true
			}
		}
	}
	return false
}

// eatCompound 吃掉一个可能连写的复合关键词（中文无空格分词，如 如无/哪里/降序）。
func (p *sqlParser) eatCompound(zh, en string) bool {
	if t, ok := p.peek(); ok && t.kind == "词" {
		low := strings.ToLower(t.text)
		if t.text == zh || low == strings.ToLower(en) || low == strings.ReplaceAll(en, " ", "") {
			p.pos++
			return true
		}
	}
	return false
}

// eatPhrase 吃掉一个关键词短语：整串连写（建表 / insert into）或按 parts 分词（建 表）都行。
func (p *sqlParser) eatPhrase(zhParts, enWords []string) bool {
	t, ok := p.peek()
	if !ok || t.kind != "词" {
		return false
	}
	low := strings.ToLower(t.text)
	if low == strings.Join(enWords, "") || t.text == strings.Join(zhParts, "") {
		p.pos++
		return true
	}
	save := p.pos
	i := 0
	for i < len(zhParts) {
		t, ok := p.peek()
		if !ok || t.kind != "词" {
			p.pos = save
			return false
		}
		low := strings.ToLower(t.text)
		matched := false
		// 当前记号可能覆盖连续多个分词（如连写的 如无 = 如 + 无）
		for j := i + 1; j <= len(zhParts); j++ {
			zhJoin := strings.Join(zhParts[i:j], "")
			enJoin := ""
			if i < len(enWords) {
				end := j
				if end > len(enWords) {
					end = len(enWords)
				}
				enJoin = strings.Join(enWords[i:end], "")
			}
			if t.text == zhJoin || (enJoin != "" && low == enJoin) {
				i = j
				matched = true
				break
			}
		}
		if !matched {
			if i < len(enWords) && low == strings.ToLower(enWords[i]) {
				i++
				matched = true
			}
		}
		if !matched {
			p.pos = save
			return false
		}
		p.pos++
	}
	return true
}

func (p *sqlParser) eatSym(sym string) bool {
	if t, ok := p.peek(); ok && t.kind == "符号" && t.text == sym {
		p.pos++
		return true
	}
	return false
}

// ident 吃掉一个标识符（表名/列名）。
func (p *sqlParser) ident(what string) (string, *errs.Error) {
	t, ok := p.next()
	if !ok || t.kind != "词" {
		return "", p.errSyntax("这里要写" + what + "。")
	}
	return t.text, nil
}

func (p *sqlParser) parse() (*dbStmt, *errs.Error) {
	st := &dbStmt{}
	switch {
	case p.eatPhrase([]string{"建", "表"}, []string{"create", "table"}):
		st.kind = "建"
		if p.eatCompound("如无", "if not exists") ||
			p.eatPhrase([]string{"如", "无", "有"}, []string{"if", "not", "exists"}) {
			st.ifNotHave = true
		}
		name, e := p.ident("表名")
		if e != nil {
			return nil, e
		}
		st.table = name
		if !p.eatSym("(") {
			return nil, p.errSyntax("建表要在一对括号里列字段：建 表 " + name + " (字段1, 字段2)。")
		}
		for {
			col, e := p.ident("字段名")
			if e != nil {
				return nil, e
			}
			st.cols = append(st.cols, col)
			if p.eatWord("自增", "auto_increment") || p.eatCompound("自增", "auto increment") {
				st.cols[len(st.cols)-1] = col + " 自增"
			}
			if p.eatSym(",") {
				continue
			}
			break
		}
		if !p.eatSym(")") {
			return nil, p.errSyntax("建表语句少了收尾的 )。")
		}
	case p.eatPhrase([]string{"删", "表"}, []string{"drop", "table"}):
		st.kind = "删表"
		if p.eatCompound("如有", "if exists") || p.eatPhrase([]string{"如", "有"}, []string{"if", "exists"}) {
			st.ifNotHave = true // 如真不存在也不报错
		}
		name, e := p.ident("表名")
		if e != nil {
			return nil, e
		}
		st.table = name
	case p.eatPhrase([]string{"插", "入"}, []string{"insert", "into"}):
		st.kind = "插入"
		name, e := p.ident("表名")
		if e != nil {
			return nil, e
		}
		st.table = name
		if !p.eatSym("(") {
			return nil, p.errSyntax("插入要写列清单：插入 " + name + " (列1, 列2) 值 (…)。")
		}
		for {
			col, e := p.ident("列名")
			if e != nil {
				return nil, e
			}
			st.cols = append(st.cols, col)
			if p.eatSym(",") {
				continue
			}
			break
		}
		if !p.eatSym(")") {
			return nil, p.errSyntax("插入的列清单少了收尾的 )。")
		}
		if !p.eatWord("值", "values") {
			return nil, p.errSyntax("插入语句里列名后面要写 值（VALUES）。")
		}
		for {
			row, e := p.parseValueTuple()
			if e != nil {
				return nil, e
			}
			st.valueRows = append(st.valueRows, row)
			if p.eatSym(",") {
				continue
			}
			break
		}
	case p.eatPhrase([]string{"选", "择"}, []string{"select"}):
		st = p.parseSelectTail()
		if st.table == "" {
			return nil, p.errSyntax("选择语句要写：选择 列 出 表名（FROM）。")
		}
	case p.eatPhrase([]string{"改"}, []string{"update"}) || p.eatCompound("更新", "update"):
		st.kind = "更新"
		name, e := p.ident("表名")
		if e != nil {
			return nil, e
		}
		st.table = name
		if !p.eatWord("设", "set") {
			return nil, p.errSyntax("更新要写：改 表名 设 列 = 值。")
		}
		for {
			col, e := p.ident("列名")
			if e != nil {
				return nil, e
			}
			if !p.eatSym("=") {
				return nil, p.errSyntax("设 子句要写 列 = 值。")
			}
			v, e := p.parseOperand()
			if e != nil {
				return nil, e
			}
			st.sets = append(st.sets, dbSet{col: col, val: v})
			if p.eatSym(",") {
				continue
			}
			break
		}
		p.parseWhere(st)
	case p.eatPhrase([]string{"删", "行"}, []string{"delete", "from"}):
		st.kind = "删行"
		name, e := p.ident("表名")
		if e != nil {
			return nil, e
		}
		st.table = name
		p.parseWhere(st)
	default:
		return nil, p.errSyntax(
			"认不出这条 SQL。支持的语句：建 表、删 表、插入、选择、改（更新）、删行（DELETE FROM）。")
	}
	if _, more := p.peek(); more {
		return nil, p.errSyntax("语句后面还有多余的内容。")
	}
	return st, nil
}

// parseSelectTail 解析 FROM 起的选择语句剩余部分（选择/SELECT 已被吃掉）。
func (p *sqlParser) parseSelectTail() *dbStmt {
	st := &dbStmt{kind: "选择"}
	if p.eatSym("*") {
		st.selectCols = nil // nil 表示 *（运行时按表列展开）
	} else {
		for {
			if fn, ok := p.tryAggregate(); ok {
				st.aggs = append(st.aggs, p.agg)
				st.selectCols = append(st.selectCols, fn)
				if p.eatSym(",") {
					continue
				}
				break
			}
			col, e := p.ident("列名")
			if e != nil {
				return st
			}
			st.selectCols = append(st.selectCols, col)
			if p.eatSym(",") {
				continue
			}
			break
		}
	}
	if !p.eatWord("出", "from") {
		return st
	}
	name, _ := p.ident("表名")
	st.table = name
	p.parseWhere(st)
	// 排序 按 列 升/降（ORDER BY col ASC/DESC；排序 也可直接跟列名）
	if p.eatWord("排序", "order") {
		p.eatWord("按", "by")
		if col, e := p.ident("列名"); e == nil {
			st.orderBy = col
		}
		if p.eatCompound("降序", "desc") || p.eatWord("降", "desc") {
			st.orderDesc = true
		} else if p.eatCompound("升序", "asc") || p.eatWord("升", "asc") {
		}
	}
	if p.eatWord("限", "limit") {
		if t, ok := p.next(); ok && t.kind == "数" {
			n, _ := IntOf(t.val, p.line)
			st.limit = n
			st.hasLimit = true
		}
	}
	return st
}

// parseWhere 哪里/WHERE 子句（可选）。
func (p *sqlParser) parseWhere(st *dbStmt) {
	if p.eatCompound("哪里", "where") || p.eatWord("哪", "where") {
		p.eatWord("里") // 中文双字关键词的分离写法
	} else if !p.eatWord("where") {
		return
	}
	c, e := p.parseOr()
	if e != nil {
		return // 语法问题在 parse() 收尾的"多余内容"检查兜底报错
	}
	st.where = *c
}

func (p *sqlParser) parseValueTuple() ([]dbOperand, *errs.Error) {
	if !p.eatSym("(") {
		return nil, p.errSyntax("值 要写在括号里：值 (?, ?)。")
	}
	var row []dbOperand
	for {
		v, e := p.parseOperand()
		if e != nil {
			return nil, e
		}
		row = append(row, v)
		if p.eatSym(",") {
			continue
		}
		break
	}
	if !p.eatSym(")") {
		return nil, p.errSyntax("值的括号少了收尾的 )。")
	}
	return row, nil
}

func (p *sqlParser) parseOperand() (dbOperand, *errs.Error) {
	t, ok := p.next()
	if !ok {
		return dbOperand{}, p.errSyntax("这里还缺一个值。")
	}
	switch t.kind {
	case "参":
		return dbOperand{kind: "参", param: p.nextParam()}, nil
	case "数", "文本":
		return dbOperand{kind: "值", val: t.val}, nil
	case "词":
		switch strings.ToLower(t.text) {
		case "真", "true":
			return dbOperand{kind: "值", val: true}, nil
		case "假", "false":
			return dbOperand{kind: "值", val: false}, nil
		case "空", "null":
			return dbOperand{kind: "值", val: nil}, nil
		}
		return dbOperand{kind: "列", col: t.text}, nil
	}
	return dbOperand{}, p.errSyntax("这里要是列名、?、或字面量。")
}

// 条件：或 ← 和 ← 比较
func (p *sqlParser) parseOr() (*dbCond, *errs.Error) {
	left, e := p.parseAnd()
	if e != nil {
		return nil, e
	}
	for p.eatWord("或", "or") {
		right, e := p.parseAnd()
		if e != nil {
			return nil, e
		}
		left = &dbCond{lop: "或", a: left, b: right}
	}
	return left, nil
}

func (p *sqlParser) parseAnd() (*dbCond, *errs.Error) {
	left, e := p.parseCmp()
	if e != nil {
		return nil, e
	}
	for p.eatWord("和", "and") {
		right, e := p.parseCmp()
		if e != nil {
			return nil, e
		}
		left = &dbCond{lop: "和", a: left, b: right}
	}
	return left, nil
}

func (p *sqlParser) parseCmp() (*dbCond, *errs.Error) {
	if p.eatSym("(") {
		c, e := p.parseOr()
		if e != nil {
			return nil, e
		}
		if !p.eatSym(")") {
			return nil, p.errSyntax("条件的括号少了收尾的 )。")
		}
		return c, nil
	}
	if p.eatWord("非", "not") {
		inner, e := p.parseCmp()
		if e != nil {
			return nil, e
		}
		return &dbCond{lop: "非", a: inner}, nil
	}
	l, e := p.parseOperand()
	if e != nil {
		return nil, e
	}
	t, ok := p.next()
	if !ok || t.kind != "符号" {
		return nil, p.errSyntax("条件要写完整：列 = 值、列 > 值 这样的比较。")
	}
	op := t.text
	if op == "<>" {
		op = "!="
	}
	r, e := p.parseOperand()
	if e != nil {
		return nil, e
	}
	return &dbCond{op: op, l: l, r: r}, nil
}

// ---------- 执行 ----------

func (st *dbStmt) run(db *Database, pb *paramBinder) (int, *errs.Error) {
	switch st.kind {
	case "建":
		if _, exists := db.tables[st.table]; exists {
			if st.ifNotHave {
				return 0, nil
			}
			return 0, errs.RuntimeHint(
				"表 "+st.table+" 已经存在。", "table "+st.table+" already exists.",
				"想避免报错可以写：建 表 如无 "+st.table+" (…)。", pb.line)
		}
		db.tables[st.table] = &dbTable{cols: colNames(st.cols), autoCols: autoColsOf(st.cols)}
		return 0, nil
	case "删表":
		if _, exists := db.tables[st.table]; !exists {
			if st.ifNotHave {
				return 0, nil
			}
			return 0, errs.RuntimeHint(
				"没有叫 "+st.table+" 的表。", "no table named "+st.table+".", "", pb.line)
		}
		delete(db.tables, st.table)
		return 0, nil
	case "插入":
		t, e := db.tableOf(st.table, pb.line)
		if e != nil {
			return 0, e
		}
		inserted := 0
		for _, tuple := range st.valueRows {
			if len(tuple) != len(st.cols) {
				return 0, errs.RuntimeHint(
					fmt.Sprintf("插入的列有 %d 个，值却给了 %d 个。", len(st.cols), len(tuple)),
					"column/value count mismatch in INSERT.", "", pb.line)
			}
			row := make([]Value, len(t.cols))
			for ci, col := range t.cols {
				for vi, vc := range st.cols {
					if vc == col {
						v, e := tuple[vi].bind(pb)
						if e != nil {
							return 0, e
						}
						if t.autoCols[col] && v == nil {
							v = big.NewRat(int64(t.nextAuto(col)), 1) // 自增列：不给值就自动取号
						}
						row[ci] = v
					}
				}
			}
			for ci, col := range t.cols {
				if t.autoCols[col] && row[ci] == nil {
					row[ci] = big.NewRat(int64(t.nextAuto(col)), 1) // 列没写或写了空值都自动取号
				}
			}
			t.rows = append(t.rows, row)
			inserted++
		}
		return inserted, nil
	case "更新":
		t, e := db.tableOf(st.table, pb.line)
		if e != nil {
			return 0, e
		}
		updated := 0
		for _, row := range t.rows {
			ok, e := st.where.eval(pb, t, row)
			if e != nil {
				return 0, e
			}
			if !ok {
				continue
			}
			for _, set := range st.sets {
				ci := colIndex(t, set.col)
				if ci < 0 {
					return 0, errs.RuntimeHint(
						"表 "+st.table+" 没有列 "+set.col+"。",
						"table "+st.table+" has no column "+set.col+".", "", pb.line)
				}
				v, e := set.val.bind(pb)
				if e != nil {
					return 0, e
				}
				row[ci] = v
			}
			updated++
		}
		return updated, nil
	case "删行":
		t, e := db.tableOf(st.table, pb.line)
		if e != nil {
			return 0, e
		}
		deleted := 0
		kept := t.rows[:0]
		for _, row := range t.rows {
			ok, e := st.where.eval(pb, t, row)
			if e != nil {
				return 0, e
			}
			if ok {
				deleted++
			} else {
				kept = append(kept, row)
			}
		}
		t.rows = kept
		return deleted, nil
	}
	return 0, errs.RuntimeHint("还不支持的语句。", "unsupported statement.", "", pb.line)
}

// selectRows 跑选择语句，返回行（列序与 selectCols 一致）。
func (st *dbStmt) selectRows(db *Database, pb *paramBinder) ([][]Value, *errs.Error) {
	t, e := db.tableOf(st.table, pb.line)
	if e != nil {
		return nil, e
	}
	cols := st.selectCols
	if cols == nil {
		cols = t.cols // * 展开
	}
	var out [][]Value
	// 聚合：数(*) / 和(列) / 平均(列) / 最大(列) / 最小(列) → 输出单行
	if len(st.aggs) > 0 {
		var matched [][]Value
		for _, row := range t.rows {
			ok, e := st.where.eval(pb, t, row)
			if e != nil {
				return nil, e
			}
			if ok {
				matched = append(matched, row)
			}
		}
		aggRow := make([]Value, len(st.aggs))
		for ai, ag := range st.aggs {
			v, e := aggCompute(ag, t, matched, pb.line)
			if e != nil {
				return nil, e
			}
			aggRow[ai] = v
		}
		return [][]Value{aggRow}, nil
	}
	for _, row := range t.rows {
		ok, e := st.where.eval(pb, t, row)
		if e != nil {
			return nil, e
		}
		if !ok {
			continue
		}
		outRow := make([]Value, len(cols))
		for i, col := range cols {
			ci := colIndex(t, col)
			if ci < 0 {
				return nil, errs.RuntimeHint(
					"表 "+st.table+" 没有列 "+col+"。",
					"table "+st.table+" has no column "+col+".",
					"现有的列有："+strings.Join(t.cols, "、")+"。", pb.line)
			}
			outRow[i] = row[ci]
		}
		out = append(out, outRow)
	}
	if st.orderBy != "" {
		// 排序键 = 该列在输出列里的位置；选了 * 时输出列即表列
		keyIdx := -1
		for i, c := range cols {
			if c == st.orderBy {
				keyIdx = i
				break
			}
		}
		if keyIdx < 0 {
			return nil, errs.RuntimeHint(
				"排序的列 "+st.orderBy+" 不在查询结果里。", "order column is not in the result set.",
				"排序的列要出现在选择列表里。", pb.line)
		}
		idx := keyIdx
		desc := st.orderDesc
		sort.SliceStable(out, func(a, b int) bool {
			c := compareValues(out[a][idx], out[b][idx])
			if desc {
				return c > 0
			}
			return c < 0
		})
	}
	if st.hasLimit && st.limit >= 0 && st.limit < len(out) {
		out = out[:st.limit]
	}
	st.selectCols = cols
	return out, nil
}

func aggCompute(ag dbAgg, t *dbTable, rows [][]Value, line int) (Value, *errs.Error) {
	if ag.fn == "数" {
		return big.NewRat(int64(len(rows)), 1), nil
	}
	idx := -1
	if ag.col != "*" {
		idx = colIndex(t, ag.col)
		if idx < 0 {
			return nil, errs.RuntimeHint("表里没有列 "+ag.col+"。", "no such column: "+ag.col+".", "", line)
		}
	}
	var sum *big.Rat
	var count int
	var mx, mn Value
	for _, row := range rows {
		v := row[idx]
		r, ok := v.(*big.Rat)
		if !ok {
			continue // 聚合只算数，空值/文本跳过
		}
		count++
		if sum == nil {
			sum = new(big.Rat).Set(r)
		} else {
			sum.Add(sum, r)
		}
		if mx == nil || r.Cmp(mx.(*big.Rat)) > 0 {
			mx = r
		}
		if mn == nil || r.Cmp(mn.(*big.Rat)) < 0 {
			mn = r
		}
	}
	switch ag.fn {
	case "和":
		if sum == nil {
			return big.NewRat(0, 1), nil
		}
		return sum, nil
	case "平均":
		if sum == nil {
			return nil, errs.RuntimeHint("没有可求平均的数。", "no numeric values to average.", "", line)
		}
		return new(big.Rat).Quo(sum, big.NewRat(int64(count), 1)), nil
	case "最大":
		return mx, nil
	case "最小":
		return mn, nil
	}
	return nil, nil
}

func (t *dbTable) rowCol(row []Value, col string) (Value, bool) {
	for i, c := range t.cols {
		if c == col {
			if i < len(row) {
				return row[i], true
			}
			return nil, false
		}
	}
	return nil, false
}

func colNames(cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(c), "自增"))
	}
	return out
}

func autoColsOf(cols []string) map[string]bool {
	m := map[string]bool{}
	for _, c := range cols {
		if strings.HasSuffix(strings.TrimSpace(c), "自增") {
			m[strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(c), "自增"))] = true
		}
	}
	return m
}

func colIndex(t *dbTable, col string) int {
	for i, c := range t.cols {
		if c == col {
			return i
		}
	}
	return -1
}

func (db *Database) tableOf(name string, line int) (*dbTable, *errs.Error) {
	t, ok := db.tables[name]
	if !ok {
		have := make([]string, 0, len(db.tables))
		for n := range db.tables {
			have = append(have, n)
		}
		hint := ""
		if len(have) > 0 {
			hint = "现有的表有：" + strings.Join(have, "、") + "。"
		} else {
			hint = "先建表：库.执行(\"建 表 " + name + " (列1, 列2)\")。"
		}
		return nil, errs.RuntimeHint(
			"没有叫 "+name+" 的表。", "no table named "+name+".", hint, line)
	}
	return t, nil
}

// ---------- 条件求值 ----------

// eval 无条件（kind 为空）时恒真。
func (c *dbCond) eval(pb *paramBinder, t *dbTable, row []Value) (bool, *errs.Error) {
	if c.lop == "" && c.op == "" {
		return true, nil
	}
	switch c.lop {
	case "和":
		a, e := c.a.eval(pb, t, row)
		if e != nil || !a {
			return a, e
		}
		return c.b.eval(pb, t, row)
	case "或":
		a, e := c.a.eval(pb, t, row)
		if e != nil {
			return false, e
		}
		if a {
			return true, nil
		}
		return c.b.eval(pb, t, row)
	case "非":
		a, e := c.a.eval(pb, t, row)
		if e != nil {
			return false, e
		}
		return !a, nil
	}
	l, e := c.l.bindRow(pb, t, row)
	if e != nil {
		return false, e
	}
	r, e := c.r.bindRow(pb, t, row)
	if e != nil {
		return false, e
	}
	switch c.op {
	case "=":
		return DeepEqual(l, r, map[[2]interface{}]bool{})
	case "!=":
		eq, e := DeepEqual(l, r, map[[2]interface{}]bool{})
		return !eq, e
	}
	cmp := compareValues(l, r)
	switch c.op {
	case ">":
		return cmp > 0, nil
	case "<":
		return cmp < 0, nil
	case ">=":
		return cmp >= 0, nil
	case "<=":
		return cmp <= 0, nil
	}
	return false, errs.Runtime("不支持的比较："+c.op+"。", "unsupported comparison: "+c.op+".", pb.line)
}

// bindRow 操作数求值：列取当前行的值，参取实参，值原样。
func (o dbOperand) bindRow(pb *paramBinder, t *dbTable, row []Value) (Value, *errs.Error) {
	switch o.kind {
	case "值":
		return o.val, nil
	case "参":
		return pb.at(o.param)
	case "列":
		if v, ok := t.rowCol(row, o.col); ok {
			return v, nil
		}
		return nil, errs.RuntimeHint(
			"表 "+stJoin(t)+" 里没有列 "+o.col+"。",
			"no such column: "+o.col+".", "", pb.line)
	}
	return nil, nil
}

// bind 非 WHERE 场景（插入/设值）的参数绑定：不能引用列。
func (o dbOperand) bind(pb *paramBinder) (Value, *errs.Error) {
	switch o.kind {
	case "值":
		return o.val, nil
	case "参":
		return pb.at(o.param)
	case "列":
		return nil, errs.RuntimeHint(
			"这里只能是 ? 或字面量，不能引用列 "+o.col+"。",
			"a column reference is not allowed here.", "", pb.line)
	}
	return nil, nil
}

func stJoin(t *dbTable) string { return strings.Join(t.cols, "、") }

// compareValues 通用比较：数比数值，文本比字典序，布尔 真>假；类型不同按类型名排（仅排序用）。
func compareValues(a, b Value) int {
	ra, aok := a.(*big.Rat)
	rb, bok := b.(*big.Rat)
	if aok && bok {
		return ra.Cmp(rb)
	}
	sa, aok := a.(string)
	sb, bok := b.(string)
	if aok && bok {
		return strings.Compare(sa, sb)
	}
	ba, aok := a.(bool)
	bb, bok := b.(bool)
	if aok && bok {
		if ba == bb {
			return 0
		}
		if ba {
			return 1
		}
		return -1
	}
	return strings.Compare(TypeName(a), TypeName(b))
}
