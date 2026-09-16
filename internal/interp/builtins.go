package interp

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sahou/internal/encodesrc"
	"sahou/internal/errs"
	"sahou/internal/lexer"
	"sahou/internal/parser"
	"sahou/internal/stonesrc"
)

// ---------- 内置函数注册表（规范 6.2：共 29 个，另有 1 个别名不占名额）----------

type builtinDef struct {
	zh, en string
	// params: 每个参数的多个别名（如 结尾 与 end）；"!" 后缀表示必填。
	// 调用时位置实参按顺序、命名实参按别名匹配（规范 6.1 参数名中英双名）。
	params [][]string
	fn     func(in *Interp, args []Value, line int) (Value, *errs.Error)
}

var (
	builtins     map[string]*Builtin
	builtinNames []string
	builtinTable []*builtinDef
	nameToDef    = map[string]*builtinDef{}
	listMembers  = map[string]*memberDef{}
	dictMembers  = map[string]*memberDef{}
)

type memberDef struct {
	zh, en string
	fn     func(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error)
}

func reg(d *builtinDef) {
	builtinTable = append(builtinTable, d)
	nameToDef[d.zh] = d
	nameToDef[d.en] = d
	builtinNames = append(builtinNames, d.zh)
}

func IsBuiltinName(name string) bool {
	_, ok := nameToDef[name]
	return ok
}

func init() {
	builtins = make(map[string]*Builtin)

	reg(&builtinDef{zh: "打印", en: "print", params: [][]string{{"值!", "value!"}, {"结尾", "end"}}, fn: biPrint})
	reg(&builtinDef{zh: "输入", en: "input", params: [][]string{{"提示", "prompt"}}, fn: biInput})
	numDef := &builtinDef{zh: "数", en: "num", params: [][]string{{"值!", "value!"}}, fn: biNum}
	reg(numDef)
	nameToDef["文本转数"] = numDef // 简报 D9 的别名，与 数/num 是同一个函数
	nameToDef["str_to_num"] = numDef
	reg(&builtinDef{zh: "文本", en: "str", params: [][]string{{"值!", "value!"}}, fn: biStr})
	reg(&builtinDef{zh: "列表", en: "list", params: [][]string{{"值", "value"}}, fn: biList})
	reg(&builtinDef{zh: "字典", en: "dict", params: [][]string{{"配对列表", "pairs"}}, fn: biDict})
	reg(&builtinDef{zh: "从a到b", en: "range_to", params: [][]string{{"a!"}, {"b!"}, {"步长", "step"}}, fn: biRangeTo})
	reg(&builtinDef{zh: "长度", en: "len", params: [][]string{{"值!", "value!"}}, fn: biLen})
	reg(&builtinDef{zh: "取", en: "slice", params: [][]string{{"序列!", "seq!"}, {"起", "start"}, {"止", "stop"}, {"步长", "step"}}, fn: biSlice})
	reg(&builtinDef{zh: "抛出", en: "throw", params: [][]string{{"消息!", "message!"}}, fn: biThrow})
	reg(&builtinDef{zh: "类型", en: "type_of", params: [][]string{{"值!", "value!"}}, fn: biType})
	reg(&builtinDef{zh: "包含", en: "contains", params: [][]string{{"容器!", "container!"}, {"值!", "value!"}}, fn: biContains})
	reg(&builtinDef{zh: "求和", en: "sum", params: [][]string{{"列表!", "items!"}}, fn: biSum})
	reg(&builtinDef{zh: "最大值", en: "max", params: [][]string{{"列表!", "items!"}}, fn: biMax})
	reg(&builtinDef{zh: "最小值", en: "min", params: [][]string{{"列表!", "items!"}}, fn: biMin})
	reg(&builtinDef{zh: "排序", en: "sorted", params: [][]string{{"列表!", "items!"}, {"降序", "reverse"}}, fn: biSorted})
	reg(&builtinDef{zh: "反转", en: "reversed", params: [][]string{{"值!", "value!"}}, fn: biReversed})
	reg(&builtinDef{zh: "连接", en: "join", params: [][]string{{"列表!", "items!"}, {"分隔", "sep"}}, fn: biJoin})
	reg(&builtinDef{zh: "分割", en: "split", params: [][]string{{"文本!", "text!"}, {"分隔", "sep"}}, fn: biSplit})
	reg(&builtinDef{zh: "替换", en: "replace", params: [][]string{{"文本!", "text!"}, {"旧!", "old!"}, {"新!", "new!"}}, fn: biReplace})
	reg(&builtinDef{zh: "修剪", en: "trim", params: [][]string{{"文本!", "text!"}}, fn: biTrim})
	reg(&builtinDef{zh: "转大写", en: "upper", params: [][]string{{"文本!", "text!"}}, fn: biUpper})
	reg(&builtinDef{zh: "转小写", en: "lower", params: [][]string{{"文本!", "text!"}}, fn: biLower})
	reg(&builtinDef{zh: "绝对值", en: "abs", params: [][]string{{"数!", "value!"}}, fn: biAbs})
	reg(&builtinDef{zh: "平方根", en: "sqrt", params: [][]string{{"数!", "value!"}}, fn: biSqrt})
	reg(&builtinDef{zh: "四舍五入", en: "round", params: [][]string{{"数!", "value!"}, {"小数位", "digits"}}, fn: biRound})
	reg(&builtinDef{zh: "读取文件", en: "read_file", params: [][]string{{"路径!", "path!"}}, fn: biReadFile})
	reg(&builtinDef{zh: "写入文件", en: "write_file", params: [][]string{{"路径!", "path!"}, {"内容!", "content!"}}, fn: biWriteFile})
	reg(&builtinDef{zh: "文件存在", en: "file_exists", params: [][]string{{"路径!", "path!"}}, fn: biFileExists})
	reg(&builtinDef{zh: "位置", en: "find", params: [][]string{{"文本!", "text!"}, {"子文本!", "sub!"}}, fn: biFind})

	for _, d := range builtinTable {
		b := &Builtin{Zh: d.zh, En: d.en}
		b.Call = func(in *Interp, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
			bound, e := bindArgs(d, args, named, line)
			if e != nil {
				return nil, e
			}
			return d.fn(in, bound, line)
		}
		builtins[d.zh] = b
		builtins[d.en] = b
	}
	// 别名（文本转数/str_to_num）与正名是同一个 Builtin，不占 29 个名额
	for name, def := range nameToDef {
		if def == numDef {
			builtins[name] = builtins["数"]
		}
	}

	// 成员函数（规范 6.4：成员函数原地修改，全局函数返回新值）
	listMembers["添加"] = &memberDef{zh: "添加", en: "append", fn: mAppend}
	listMembers["append"] = listMembers["添加"]
	listMembers["去重"] = &memberDef{zh: "去重", en: "dedupe", fn: mDedupe}
	listMembers["dedupe"] = listMembers["去重"]
	dictMembers["删除"] = &memberDef{zh: "删除", en: "remove", fn: mDictRemove}
	dictMembers["remove"] = dictMembers["删除"]
}

func listMember(name string) *memberDef { return listMembers[name] }

func dictMember(name string) *memberDef { return dictMembers[name] }

// bindArgs 位置实参按序绑定，命名实参按别名表匹配；未提供且无默认值的槽位填 omitted 哨兵。
func bindArgs(d *builtinDef, pos []Value, named map[string]Value, line int) ([]Value, *errs.Error) {
	out := make([]Value, len(d.params))
	filled := make([]bool, len(d.params))
	defaults := builtinDefaults[d.zh]
	for i, v := range pos {
		if i >= len(d.params) {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("%s 只要 %d 个参数，但给了至少 %d 个。", d.zh, len(d.params), len(pos)),
				fmt.Sprintf("%s takes %d arguments but got %d.", d.zh, len(d.params), len(pos)),
				"用法："+usageOf(d), line)
		}
		out[i] = v
		filled[i] = true
	}
	for alias, v := range named {
		matched := false
		for i, aliases := range d.params {
			for _, a := range aliases {
				if a == alias {
					if filled[i] {
						return nil, errs.RuntimeHint(
							"参数 "+alias+" 既按位置又按名字给了一次。", "argument "+alias+" is given both positionally and by name.",
							"同一个参数只能给一次。", line)
					}
					out[i] = v
					filled[i] = true
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			var all []string
			for _, aliases := range d.params {
				all = append(all, aliases...)
			}
			sug := errs.DidYouMean(alias, all)
			zh := fmt.Sprintf("%s 没有名叫 %s 的参数。", d.zh, alias)
			en := fmt.Sprintf("%s has no parameter named %s.", d.zh, alias)
			if sug != "" {
				zh += fmt.Sprintf("你是不是想写 %s？", sug)
				en += fmt.Sprintf(" Did you mean %s?", sug)
			}
			return nil, errs.RuntimeHint(zh, en, "用法："+usageOf(d), line)
		}
	}
	for i, aliases := range d.params {
		required := false
		for _, a := range aliases {
			if strings.HasSuffix(a, "!") {
				required = true
			}
		}
		if required && !filled[i] {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("%s 缺少参数 %s。", d.zh, strings.TrimSuffix(aliases[0], "!")),
				fmt.Sprintf("%s is missing argument %s.", d.zh, strings.TrimSuffix(aliases[0], "!")),
				"用法："+usageOf(d), line)
		}
		if !filled[i] {
			if defaults != nil {
				if def, ok := defaults[i]; ok {
					out[i] = def
					continue
				}
			}
			out[i] = omitted{} // 未提供
		}
	}
	return out, nil
}

func usageOf(d *builtinDef) string {
	var parts []string
	for _, aliases := range d.params {
		p := strings.TrimSuffix(aliases[0], "!")
		if !strings.HasSuffix(aliases[0], "!") {
			p += ": 可省略"
		}
		parts = append(parts, p)
	}
	return d.zh + "(" + strings.Join(parts, ", ") + ")"
}

// builtinDefaults 内置函数的可选参数默认值（下标对应 params）。
var builtinDefaults = map[string]map[int]Value{
	"打印":   {1: "\n"},
	"输入":   {0: ""},
	"从a到b": {2: big.NewRat(1, 1)},
	"取":    {1: big.NewRat(0, 1), 2: omitted{}, 3: big.NewRat(1, 1)},
	"排序":   {1: false},
	"连接":   {1: ""},
	"分割":   {1: " "},
	"四舍五入": {1: big.NewRat(0, 1)},
}

// omitted 表示"没有提供"（取 的 止 参数、缺省槽位）。
type omitted struct{}

func argIsOmitted(v Value) bool {
	_, ok := v.(omitted)
	return ok
}

// ---------- 各内置函数实现 ----------

func biPrint(in *Interp, args []Value, line int) (Value, *errs.Error) {
	if argIsOmitted(args[0]) {
		return nil, errs.RuntimeHint(
			"打印 里没有写要打印的值。", "nothing to print.",
			"括号里放任意一个值，例如 打印(\"你好\")。", line)
	}
	end := "\n"
	if a, ok := opt(args, 1); ok {
		s, ok2 := a.(string)
		if !ok2 {
			return nil, errs.RuntimeHint(
				"结尾 参数要是一段文本。", "the end parameter must be a string.",
				"例如：打印(\"你好\", 结尾: \"\")。", line)
		}
		end = s
	}
	fmt.Fprint(in.Stdout, Str(args[0])+end)
	return nil, nil
}

// opt 取第 i 个参数；未提供（omitted）或越界时 ok=false。
func opt(args []Value, i int) (Value, bool) {
	if i < len(args) && !argIsOmitted(args[i]) {
		return args[i], true
	}
	return nil, false
}

func biInput(in *Interp, args []Value, line int) (Value, *errs.Error) {
	prompt := ""
	if a, ok := opt(args, 0); ok {
		prompt = Str(a)
	}
	return in.Input(prompt), nil
}

func biNum(in *Interp, args []Value, line int) (Value, *errs.Error) {
	v := args[0]
	switch x := v.(type) {
	case *big.Rat:
		return x, nil
	case bool:
		if x {
			return big.NewRat(1, 1), nil
		}
		return new(big.Rat), nil
	case string:
		r, ok := new(big.Rat).SetString(strings.TrimSpace(x))
		if ok {
			return r, nil
		}
		if f, _, err := big.ParseFloat(strings.TrimSpace(x), 10, 200, big.ToNearestEven); err == nil {
			r2, _ := new(big.Rat).SetString(f.Text('f', -1))
			return r2, nil
		}
		sug := zhNumeralSuggestion(x)
		zh := "文本 \"" + x + "\" 不是数字。"
		en := "the text \"" + x + "\" is not a number."
		if sug != "" {
			zh += "你是不是想写 " + sug + "？"
			en += " Did you mean " + sug + "?"
		}
		return nil, errs.RuntimeHint(zh, en, "文本里只能有数字和小数点，例如 \"95\" 或 \"3.5\"。", line)
	}
	return nil, errs.RuntimeHint(
		"数() 只能转换 文本 或 布尔，收到的是"+TypeName(v)+"。", "num() converts text or bool, got "+TypeName(v)+".",
		"例如：数(\"95\") 得 95。", line)
}

// zhNumeralSuggestion 识别简单中文数字（如 九十五 → 95），只用于"你是不是想写"建议。
func zhNumeralSuggestion(s string) string {
	digits := map[rune]int{'零': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, section, current, any := 0, 0, 0, false
	for _, r := range s {
		if d, ok := digits[r]; ok {
			if current < 10 {
				current = current*10 + d
			} else {
				return ""
			}
			any = true
		} else if u, ok := units[r]; ok {
			if current == 0 {
				current = 1
			}
			section += current * u
			current = 0
			any = true
		} else if r == '万' {
			section = (section + current) * 10000
			total += section
			section, current = 0, 0
			any = true
		} else {
			return ""
		}
	}
	if !any {
		return ""
	}
	return fmt.Sprint(total + section + current)
}

func biStr(in *Interp, args []Value, line int) (Value, *errs.Error) {
	if argIsOmitted(args[0]) {
		return nil, errs.RuntimeHint(
			"文本() 里没有写要转换的值。", "nothing to convert.",
			"括号里放任意一个值，例如 文本(95)。", line)
	}
	return Str(args[0]), nil
}

func biList(in *Interp, args []Value, line int) (Value, *errs.Error) {
	a, ok := opt(args, 0)
	if !ok {
		return &List{}, nil
	}
	switch x := a.(type) {
	case *List:
		items := make([]Value, len(x.Items))
		copy(items, x.Items)
		return &List{Items: items}, nil
	case *Dict:
		return &List{Items: x.KeysAsValues()}, nil
	}
	return nil, errs.RuntimeHint(
		"列表() 只接受 列表 或 字典，收到的是"+TypeName(a)+"。", "list() accepts a list or a dict, got "+TypeName(a)+".",
		"列表() 得空列表；列表(某列表) 得浅拷贝；列表(某字典) 得键的列表。", line)
}

func biDict(in *Interp, args []Value, line int) (Value, *errs.Error) {
	a, ok := opt(args, 0)
	if !ok {
		return NewDict(), nil
	}
	l, isList := a.(*List)
	if !isList {
		return nil, errs.RuntimeHint(
			"字典() 只接受配对列表，收到的是"+TypeName(a)+"。", "dict() accepts a list of [key, value] pairs, got "+TypeName(a)+".",
			"例如：字典([[\"名字\", \"咪咪\"]])。", line)
	}
	d := NewDict()
	for i, item := range l.Items {
		pair, ok := item.(*List)
		if !ok || len(pair.Items) != 2 {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("配对列表的第 %d 项不是 [键, 值] 的形式。", i+1),
				fmt.Sprintf("pair #%d is not a [key, value] list.", i+1),
				"每一项都要是恰好两个元素的列表。", line)
		}
		enc, ok := KeyEncode(pair.Items[0])
		if !ok {
			return nil, errs.RuntimeHint(
				"字典的键只能是文本或数。", "dict keys must be text or a number.", "", line)
		}
		d.SetNew(enc, pair.Items[1])
	}
	return d, nil
}

func biRangeTo(in *Interp, args []Value, line int) (Value, *errs.Error) {
	a, e := needInt(args, 0, line)
	if e != nil {
		return nil, e
	}
	b, e := needInt(args, 1, line)
	if e != nil {
		return nil, e
	}
	step := 1
	if s, ok := opt(args, 2); ok {
		step, e = intFrom(s, line)
		if e != nil {
			return nil, e
		}
	}
	if step == 0 {
		return nil, errs.RuntimeHint(
			"步长不能是 0。", "step cannot be 0.",
			"步长是每一步跨多少；跨 0 步永远走不动。", line)
	}
	items := []Value{}
	if step > 0 {
		for i := a; i < b; i += step {
			items = append(items, big.NewRat(int64(i), 1))
		}
	} else {
		for i := a; i > b; i += step {
			items = append(items, big.NewRat(int64(i), 1))
		}
	}
	return &List{Items: items}, nil
}

func needInt(args []Value, i int, line int) (int, *errs.Error) {
	a, ok := opt(args, i)
	if !ok {
		return 0, errs.RuntimeHint(
			fmt.Sprintf("从a到b 缺少第 %d 个参数。", i+1),
			fmt.Sprintf("range_to is missing argument #%d.", i+1), "", line)
	}
	return intFrom(a, line)
}

func intFrom(v Value, line int) (int, *errs.Error) {
	r, ok := v.(*big.Rat)
	if !ok || !r.IsInt() {
		return 0, errs.RuntimeHint(
			"这里需要一个整数，收到的是 "+Str(v)+"。", "an integer is required here, got "+Str(v)+".",
			"如果来自文本，先用 数() 转换。", line)
	}
	return AsInt(r), nil
}

func biLen(in *Interp, args []Value, line int) (Value, *errs.Error) {
	switch x := args[0].(type) {
	case *List:
		return big.NewRat(int64(len(x.Items)), 1), nil
	case string:
		return big.NewRat(int64(len([]rune(x))), 1), nil
	case *Dict:
		return big.NewRat(int64(x.Len()), 1), nil
	}
	return nil, errs.RuntimeHint(
		"长度() 只能用在 列表、文本、字典 上，收到的是"+TypeName(args[0])+"。",
		"len() works on lists, strings and dicts, got "+TypeName(args[0])+".",
		"一个数没有长度；想要它的位数，可以先 文本() 再 长度()。", line)
}

func biSlice(in *Interp, args []Value, line int) (Value, *errs.Error) {
	start := 0
	if a, ok := opt(args, 1); ok {
		var e *errs.Error
		if start, e = intFrom(a, line); e != nil {
			return nil, e
		}
	}
	stop, hasStop := 0, false
	if a, ok := opt(args, 2); ok {
		var e *errs.Error
		if stop, e = intFrom(a, line); e != nil {
			return nil, e
		}
		hasStop = true
	}
	step := 1
	if a, ok := opt(args, 3); ok {
		var e *errs.Error
		if step, e = intFrom(a, line); e != nil {
			return nil, e
		}
	}
	if step == 0 {
		return nil, errs.RuntimeHint("步长不能是 0。", "step cannot be 0.", "", line)
	}
	switch col := args[0].(type) {
	case *List:
		idxs := sliceIndexes(len(col.Items), start, stop, hasStop, step)
		out := make([]Value, 0, len(idxs))
		for _, i := range idxs {
			out = append(out, col.Items[i])
		}
		return &List{Items: out}, nil
	case string:
		runes := []rune(col)
		idxs := sliceIndexes(len(runes), start, stop, hasStop, step)
		var sb strings.Builder
		for _, i := range idxs {
			sb.WriteRune(runes[i])
		}
		return sb.String(), nil
	}
	return nil, errs.RuntimeHint(
		"取() 只能用在 列表 或 文本 上，收到的是"+TypeName(args[0])+"。",
		"slice() works on lists and strings, got "+TypeName(args[0])+".", "", line)
}

// sliceIndexes 计算半开区间 [起, 止) 的下标（负数从尾部数）。
func sliceIndexes(n, start, stop int, hasStop bool, step int) []int {
	if start < 0 {
		start += n
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	if hasStop {
		if stop < 0 {
			stop += n
		}
		if stop < 0 {
			stop = 0
		}
		if stop > n {
			stop = n
		}
	} else {
		if step > 0 {
			stop = n
		} else {
			stop = -1
		}
	}
	var out []int
	if step > 0 {
		for i := start; i < stop; i += step {
			out = append(out, i)
		}
	} else {
		for i := start; i > stop; i += step {
			out = append(out, i)
		}
	}
	return out
}

func biThrow(in *Interp, args []Value, line int) (Value, *errs.Error) {
	msg, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"抛出() 的消息必须是文本，收到的是"+TypeName(args[0])+"。",
			"throw() requires a text message, got "+TypeName(args[0])+".",
			"用插值把别的值放进消息：抛出(\"成绩 {分数} 不合法\")。", line)
	}
	return nil, errs.Runtime(msg, msg, line)
}

func biType(in *Interp, args []Value, line int) (Value, *errs.Error) {
	return TypeEnName(args[0]), nil
}

func biContains(in *Interp, args []Value, line int) (Value, *errs.Error) {
	container, needle := args[0], args[1]
	switch col := container.(type) {
	case *List:
		for _, it := range col.Items {
			eq, e := DeepEqual(it, needle, map[[2]interface{}]bool{})
			if e != nil {
				e.Line = line
				return nil, e
			}
			if eq {
				return true, nil
			}
		}
		return false, nil
	case *Dict:
		enc, ok := KeyEncode(needle)
		return ok && col.Has(enc), nil
	case string:
		ns, ok := needle.(string)
		if !ok {
			return nil, errs.RuntimeHint(
				"在文本里查找时，要找的也得是文本，收到的是"+TypeName(needle)+"。",
				"searching in a string requires a string needle, got "+TypeName(needle)+".",
				"先用 文本() 转换。", line)
		}
		return strings.Contains(col, ns), nil
	}
	return nil, errs.RuntimeHint(
		"包含() 的第一个参数要 列表、字典 或 文本，收到的是"+TypeName(container)+"。",
		"contains() needs a list, dict or string, got "+TypeName(container)+".", "", line)
}

func allNumbers(l *List) bool {
	for _, it := range l.Items {
		if !IsNum(it) {
			return false
		}
	}
	return true
}

func allStrings(l *List) bool {
	for _, it := range l.Items {
		if _, ok := it.(string); !ok {
			return false
		}
	}
	return true
}

func biSum(in *Interp, args []Value, line int) (Value, *errs.Error) {
	l, ok := args[0].(*List)
	if !ok {
		return nil, notAList("求和", args[0], line)
	}
	total := new(big.Rat)
	for i, it := range l.Items {
		if !IsNum(it) {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("求和() 的列表里第 %d 项不是数（是%s）。", i+1, TypeName(it)),
				fmt.Sprintf("sum(): item #%d is not a number (got %s).", i+1, TypeName(it)),
				"求和 只能把数相加。", line)
		}
		total.Add(total, it.(*big.Rat))
	}
	return total, nil
}

func notAList(fn string, v Value, line int) *errs.Error {
	return errs.RuntimeHint(
		fn+"() 的参数要是列表，收到的是"+TypeName(v)+"。",
		fn+"() needs a list, got "+TypeName(v)+".", "", line)
}

func biMax(in *Interp, args []Value, line int) (Value, *errs.Error) {
	return maxMin(args[0], 1, line)
}

func biMin(in *Interp, args []Value, line int) (Value, *errs.Error) {
	return maxMin(args[0], -1, line)
}

func maxMin(v Value, sign int, line int) (Value, *errs.Error) {
	l, ok := v.(*List)
	if !ok {
		return nil, notAList("最大值", v, line)
	}
	if len(l.Items) == 0 {
		return nil, errs.RuntimeHint(
			"空列表没有最大值。", "an empty list has no maximum.",
			"先往列表里 添加() 一些元素。", line)
	}
	if !allNumbers(l) && !allStrings(l) {
		return nil, errs.RuntimeHint(
			"列表里的元素类型不一致，没法比大小。", "the list mixes types and cannot be ordered.",
			"要么全是数，要么全是文本。", line)
	}
	best := l.Items[0]
	nums := allNumbers(l)
	for _, it := range l.Items[1:] {
		c, e := orderCmp(it, best, nums, line)
		if e != nil {
			return nil, e
		}
		if c*sign > 0 {
			best = it
		}
	}
	return best, nil
}

func orderCmp(a, b Value, nums bool, line int) (int, *errs.Error) {
	if nums {
		return AsNum(a).Cmp(AsNum(b)), nil
	}
	return strings.Compare(a.(string), b.(string)), nil
}

func biSorted(in *Interp, args []Value, line int) (Value, *errs.Error) {
	l, ok := args[0].(*List)
	if !ok {
		return nil, notAList("排序", args[0], line)
	}
	reverse := false
	if a, ok2 := opt(args, 1); ok2 {
		b, ok3 := a.(bool)
		if !ok3 {
			return nil, errs.RuntimeHint(
				"降序 参数要是 真 或 假。", "the reverse parameter must be 真 or 假.", "", line)
		}
		reverse = b
	}
	if !allNumbers(l) && !allStrings(l) {
		return nil, errs.RuntimeHint(
			"列表里的元素类型不一致，没法排序。", "the list mixes types and cannot be sorted.",
			"要么全是数，要么全是文本。", line)
	}
	out := make([]Value, len(l.Items))
	copy(out, l.Items)
	nums := allNumbers(l)
	sort.SliceStable(out, func(i, j int) bool {
		c, _ := orderCmp(out[i], out[j], nums, 0)
		if reverse {
			return c > 0
		}
		return c < 0
	})
	return &List{Items: out}, nil
}

func biReversed(in *Interp, args []Value, line int) (Value, *errs.Error) {
	switch x := args[0].(type) {
	case *List:
		out := make([]Value, len(x.Items))
		for i, it := range x.Items {
			out[len(x.Items)-1-i] = it
		}
		return &List{Items: out}, nil
	case string:
		runes := []rune(x)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes), nil
	}
	return nil, errs.RuntimeHint(
		"反转() 只能用在 列表 或 文本 上，收到的是"+TypeName(args[0])+"。",
		"reversed() works on lists and strings, got "+TypeName(args[0])+".", "", line)
}

func biJoin(in *Interp, args []Value, line int) (Value, *errs.Error) {
	l, ok := args[0].(*List)
	if !ok {
		return nil, notAList("连接", args[0], line)
	}
	sep := ""
	if a, ok2 := opt(args, 1); ok2 {
		s, ok3 := a.(string)
		if !ok3 {
			return nil, errs.RuntimeHint(
				"分隔 参数要是一段文本。", "the sep parameter must be a string.", "", line)
		}
		sep = s
	}
	parts := make([]string, len(l.Items))
	for i, it := range l.Items {
		parts[i] = Str(it)
	}
	return strings.Join(parts, sep), nil
}

func biSplit(in *Interp, args []Value, line int) (Value, *errs.Error) {
	s, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"分割() 的第一个参数要是文本，收到的是"+TypeName(args[0])+"。",
			"split() needs a string, got "+TypeName(args[0])+".", "", line)
	}
	sep := " "
	if a, ok2 := opt(args, 1); ok2 {
		s2, ok3 := a.(string)
		if !ok3 {
			return nil, errs.RuntimeHint(
				"分隔 参数要是一段文本。", "the sep parameter must be a string.", "", line)
		}
		sep = s2
	}
	if sep == "" {
		return nil, errs.RuntimeHint(
			"分隔符不能是空文本。", "the separator cannot be an empty string.",
			"按空格分割就写 分割(文本)；要按字符拆，用 遍历。", line)
	}
	parts := strings.Split(s, sep)
	items := make([]Value, len(parts))
	for i, p := range parts {
		items[i] = p
	}
	return &List{Items: items}, nil
}

func biReplace(in *Interp, args []Value, line int) (Value, *errs.Error) {
	s, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"替换() 的第一个参数要是文本，收到的是"+TypeName(args[0])+"。",
			"replace() needs a string, got "+TypeName(args[0])+".", "", line)
	}
	old, ok1 := args[1].(string)
	new, ok2 := args[2].(string)
	if !ok1 || !ok2 {
		return nil, errs.RuntimeHint(
			"替换() 的旧文本和新文本都要是文本。",
			"replace() needs text for both the old and the new value.", "", line)
	}
	return strings.ReplaceAll(s, old, new), nil
}

func biTrim(in *Interp, args []Value, line int) (Value, *errs.Error) {
	s, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"修剪() 的参数要是文本，收到的是"+TypeName(args[0])+"。",
			"trim() needs a string, got "+TypeName(args[0])+".", "", line)
	}
	return strings.TrimSpace(s), nil
}

func biUpper(in *Interp, args []Value, line int) (Value, *errs.Error) {
	s, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"转大写() 的参数要是文本，收到的是"+TypeName(args[0])+"。",
			"upper() needs a string, got "+TypeName(args[0])+".", "", line)
	}
	return strings.ToUpper(s), nil
}

func biLower(in *Interp, args []Value, line int) (Value, *errs.Error) {
	s, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"转小写() 的参数要是文本，收到的是"+TypeName(args[0])+"。",
			"lower() needs a string, got "+TypeName(args[0])+".", "", line)
	}
	return strings.ToLower(s), nil
}

func biAbs(in *Interp, args []Value, line int) (Value, *errs.Error) {
	r, ok := args[0].(*big.Rat)
	if !ok {
		return nil, errs.RuntimeHint(
			"绝对值() 的参数要是一个数，收到的是"+TypeName(args[0])+"。",
			"abs() needs a number, got "+TypeName(args[0])+".", "", line)
	}
	return new(big.Rat).Abs(r), nil
}

func biSqrt(in *Interp, args []Value, line int) (Value, *errs.Error) {
	r, ok := args[0].(*big.Rat)
	if !ok {
		return nil, errs.RuntimeHint(
			"平方根() 的参数要是一个数，收到的是"+TypeName(args[0])+"。",
			"sqrt() needs a number, got "+TypeName(args[0])+".", "", line)
	}
	if r.Sign() < 0 {
		return nil, errs.RuntimeHint(
			"负数没有实数平方根。", "a negative number has no real square root.",
			"想要复数就不在 v1 的范围里了；检查数是不是算错了。", line)
	}
	f := new(big.Float).SetPrec(120).SetRat(r)
	f.Sqrt(f)
	rat, _ := f.Rat(nil)
	return rat, nil
}

func biRound(in *Interp, args []Value, line int) (Value, *errs.Error) {
	r, ok := args[0].(*big.Rat)
	if !ok {
		return nil, errs.RuntimeHint(
			"四舍五入() 的第一个参数要是一个数，收到的是"+TypeName(args[0])+"。",
			"round() needs a number, got "+TypeName(args[0])+".", "", line)
	}
	digits := 0
	if a, ok2 := opt(args, 1); ok2 {
		var e *errs.Error
		if digits, e = intFrom(a, line); e != nil {
			return nil, e
		}
	}
	var scale *big.Rat
	if digits >= 0 {
		scale = new(big.Rat).SetInt(pow10(digits))
	} else {
		scale = new(big.Rat).Inv(new(big.Rat).SetInt(pow10(-digits)))
	}
	scaled := new(big.Rat).Mul(r, scale)
	// 四舍五入（半步远离 0）：(|q|*2 + d) / 2d 取整，再补符号
	q := scaled.Num()
	d := scaled.Denom()
	neg := q.Sign() < 0
	absQ := new(big.Int).Abs(q)
	num := new(big.Int).Lsh(absQ, 1)
	num.Add(num, d)
	den := new(big.Int).Lsh(d, 1)
	unit := new(big.Int).Div(num, den)
	if neg {
		unit.Neg(unit)
	}
	out := new(big.Rat).SetInt(unit)
	out.Quo(out, scale)
	return out, nil
}

func pow10(n int) *big.Int {
	r := big.NewInt(1)
	ten := big.NewInt(10)
	for i := 0; i < n; i++ {
		r.Mul(r, ten)
	}
	return r
}

func (in *Interp) resolvePath(args []Value, line int) (string, *errs.Error) {
	p, ok := args[0].(string)
	if !ok {
		return "", errs.RuntimeHint(
			"路径要是一段文本，收到的是"+TypeName(args[0])+"。",
			"the path must be a string, got "+TypeName(args[0])+".", "", line)
	}
	return in.ScriptPath(p), nil
}

func biReadFile(in *Interp, args []Value, line int) (Value, *errs.Error) {
	path, e := in.resolvePath(args, line)
	if e != nil {
		return nil, e
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.RuntimeHint(
			"读不到文件 \""+Str(args[0])+"\"。", "cannot read file \""+Str(args[0])+"\".",
			"检查路径和文件名；相对路径以脚本所在文件夹为基准。", line)
	}
	return string(data), nil
}

func biWriteFile(in *Interp, args []Value, line int) (Value, *errs.Error) {
	path, e := in.resolvePath(args, line)
	if e != nil {
		return nil, e
	}
	content := Str(args[1])
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755) // 父目录不存在就自动建（入门友好）
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, errs.RuntimeHint(
			"写不进文件 \""+Str(args[0])+"\"。", "cannot write file \""+Str(args[0])+"\".",
			"检查文件夹是否存在、有没有写权限。", line)
	}
	return nil, nil
}

// biFind 位置(文本, 子文本)：返回首次出现的字符下标（从 0 开始）；找不到返回 -1。
func biFind(in *Interp, args []Value, line int) (Value, *errs.Error) {
	text, ok := args[0].(string)
	if !ok {
		return nil, errs.RuntimeHint(
			"位置() 的第一个参数要是文本，收到的是"+TypeName(args[0])+"。",
			"find() needs a string, got "+TypeName(args[0])+".", "", line)
	}
	sub, ok2 := args[1].(string)
	if !ok2 {
		return nil, errs.RuntimeHint(
			"位置() 的第二个参数要是文本，收到的是"+TypeName(args[1])+"。",
			"find() needs a string for the substring, got "+TypeName(args[1])+".", "", line)
	}
	idx := strings.Index(text, sub)
	if idx < 0 {
		return big.NewRat(-1, 1), nil
	}
	charPos := len([]rune(text[:idx]))
	return big.NewRat(int64(charPos), 1), nil
}

func biFileExists(in *Interp, args []Value, line int) (Value, *errs.Error) {
	path, e := in.resolvePath(args, line)
	if e != nil {
		return nil, e
	}
	_, err := os.Stat(path)
	return err == nil, nil
}

// ---------- 成员函数（原地修改，规范 6.4）----------

func mAppend(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	l := recv.(*List)
	if len(args) < 1 || argIsOmitted(args[0]) {
		return nil, errs.RuntimeHint(
			"添加() 要告诉它添加什么。", "append() needs a value to add.",
			"例如：名单.添加(\"小明\")。", line)
	}
	l.Items = append(l.Items, args[0])
	return nil, nil
}

func mDedupe(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	l := recv.(*List)
	var out []Value
	for _, it := range l.Items {
		dup := false
		for _, seen := range out {
			eq, e := DeepEqual(it, seen, map[[2]interface{}]bool{})
			if e != nil {
				e.Line = line
				return nil, e
			}
			if eq {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, it)
		}
	}
	l.Items = out
	return nil, nil
}

func mDictRemove(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
	d := recv.(*Dict)
	if len(args) < 1 || argIsOmitted(args[0]) {
		return nil, errs.RuntimeHint(
			"删除() 要告诉它删哪个键。", "remove() needs a key to delete.",
			"例如：小猫.删除(\"饥饿\")。", line)
	}
	enc, ok := KeyEncode(args[0])
	if !ok {
		return nil, errs.RuntimeHint(
			"字典的键只能是文本或数。", "dict keys must be text or a number.", "", line)
	}
	if !d.Delete(enc) {
		cands := displayKeys(d)
		sug := errs.DidYouMean(Str(args[0]), cands)
		zh := "字典里没有键 " + Str(args[0]) + "，删不了。"
		en := "the dict has no key " + Str(args[0]) + " to remove."
		if sug != "" {
			zh += "你是不是想写 \"" + sug + "\"？"
			en += " Did you mean \"" + sug + "\"?"
		}
		return nil, errs.RuntimeHint(zh, en, "现有的键有："+strings.Join(cands, "、")+"。", line)
	}
	return nil, nil
}

// CompletionWords 供 LSP 补全：全部关键字 + 内置函数名 + 模块名（中英双语）。
func CompletionWords() []string {
	seen := map[string]bool{}
	var out []string
	add := func(w string) {
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	for w := range lexer.KeyWords {
		add(w)
	}
	for w := range nameToDef {
		add(w)
	}
	for _, m := range []string{"网络", "net", "页面", "page", "随机", "random", "时间", "time",
		"数学", "math", "编码", "encoding", "系统", "sys", "网页", "html",
		"数据库", "db", "应用", "app", "测试", "assert"} {
		add(m)
	}
	return out
}

// CompletionMembers 内置模块成员表：中英模块名都映射到同一份成员列表（编辑器补全用）。
func CompletionMembers() map[string][]string {
	in := New()
	byDict := map[*Dict][]string{}
	for _, d := range in.builtinMods {
		if _, ok := byDict[d]; ok {
			continue
		}
		var members []string
		for _, k := range d.Keys() {
			if strings.HasPrefix(k, "s:") {
				members = append(members, k[2:])
			}
		}
		byDict[d] = members
	}
	out := make(map[string][]string, len(in.builtinMods))
	for name, d := range in.builtinMods {
		out[name] = byDict[d]
	}
	return out
}

// CompletionStoneMembers 一个 stones 包的顶层名字（函数与顶层变量），编辑器补全用。
// 内嵌包直接读；本地包从当前目录向上找 stones/（与解释器同口径）。
func CompletionStoneMembers(pkg string) []string {
	var src string
	if s, ok := stonesrc.Source(pkg); ok {
		src = s
	} else {
		found := ""
		for _, dir := range dirChain(".") {
			for _, cand := range []string{
				filepath.Join(dir, "stones", pkg+".saho"),
				filepath.Join(dir, "stones", pkg, "main.saho"),
			} {
				if st, err := os.Stat(cand); err == nil && !st.IsDir() {
					found = cand
					break
				}
			}
			if found != "" {
				break
			}
		}
		if found == "" {
			return nil
		}
		data, err := encodesrc.ReadFile(found)
		if err != nil {
			return nil
		}
		src = string(data)
	}
	toks, e := lexer.Tokenize(src)
	if e != nil {
		return nil
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		return nil
	}
	var names []string
	for _, st := range prog.Stmts {
		switch s := st.(type) {
		case *parser.FnStmt:
			names = append(names, s.Name)
		case *parser.LetStmt:
			if id, ok := s.Target.(*parser.Ident); ok {
				names = append(names, id.Name)
			}
		}
	}
	return names
}
