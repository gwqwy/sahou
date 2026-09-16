// v2 标准库模块：随机/random、时间/time、数学/math、编码/encoding、系统/sys。
// 全部预置（不写 用...引入 也能用）；成员不占 29 个内置函数名额（08 文档 D9 名额账本）。
// 数值语义与核心一致：结果按 15 位有效数字圆整（big.Rat 承载）。
package interp

import (
	"encoding/base64"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"sahou/internal/errs"
)

// ---------- 数值转换辅助 ----------

// ratToFloat 精确有理数 → float64（宿主数学函数用）。
func ratToFloat(v Value) (float64, bool) {
	r, ok := v.(*big.Rat)
	if !ok {
		return 0, false
	}
	f, _ := r.Float64()
	return f, true
}

// floatToRat float64 → 15 位有效数字的精确有理数（与显示规则一致）。
func floatToRat(f float64) *big.Rat {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return new(big.Rat)
	}
	s := strconv.FormatFloat(f, 'g', 15, 64)
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return new(big.Rat)
	}
	return r
}

func needNum(args []Value, i int, mod, fn string, line int) (*big.Rat, *errs.Error) {
	if i >= len(args) {
		return nil, errs.RuntimeHint(
			fmt.Sprintf("%s.%s 缺少第 %d 个参数。", mod, fn, i+1),
			fmt.Sprintf("%s.%s is missing argument #%d.", mod, fn, i+1), "", line)
	}
	r, ok := args[i].(*big.Rat)
	if !ok {
		return nil, errs.RuntimeHint(
			fmt.Sprintf("%s.%s 的第 %d 个参数要是一个数，收到的是%s。", mod, fn, i+1, TypeName(args[i])),
			fmt.Sprintf("%s.%s argument #%d must be a number, got %s.", mod, fn, i+1, TypeEnName(args[i])), "", line)
	}
	return r, nil
}

func needTextArg(args []Value, i int, mod, fn string, line int) (string, *errs.Error) {
	if i >= len(args) {
		return "", errs.RuntimeHint(
			fmt.Sprintf("%s.%s 缺少第 %d 个参数。", mod, fn, i+1),
			fmt.Sprintf("%s.%s is missing argument #%d.", mod, fn, i+1), "", line)
	}
	s, ok := args[i].(string)
	if !ok {
		return "", errs.RuntimeHint(
			fmt.Sprintf("%s.%s 的第 %d 个参数要是文本，收到的是%s。", mod, fn, i+1, TypeName(args[i])),
			fmt.Sprintf("%s.%s argument #%d must be a string, got %s.", mod, fn, i+1, TypeEnName(args[i])), "", line)
	}
	return s, nil
}

// ---------- 模块构造辅助 ----------

type modBuilder struct {
	d *Dict
}

func newMod(zh, en string) *modBuilder {
	return &modBuilder{d: NewDict()}
}

func (m *modBuilder) fn(zh, en string, params [][]string, body func(in *Interp, args []Value, line int) (Value, *errs.Error)) {
	b := &Builtin{Zh: zh, En: en}
	b.Call = func(in *Interp, args []Value, named map[string]Value, line int) (Value, *errs.Error) {
		if len(named) > 0 {
			merged, e := mergeModuleNamed(zh, params, args, named, line)
			if e != nil {
				return nil, e
			}
			args = merged
		}
		return body(in, args, line)
	}
	m.d.SetNew("s:"+zh, b)
	m.d.SetNew("s:"+en, b)
}

// mergeModuleNamed 把模块函数的命名实参按参数表归位（与内置函数 bindArgs 同一套规则）：
// 名字匹配参数的中英别名（别名尾部的 ! 必填标记不参与匹配）；
// 既按位置又按名字给、或名字不存在都报错；未填充的槽位补 omitted 哨兵。
func mergeModuleNamed(zh string, params [][]string, pos []Value, named map[string]Value, line int) ([]Value, *errs.Error) {
	out := make([]Value, len(params))
	filled := make([]bool, len(params))
	for i, v := range pos {
		if i >= len(params) {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("%s 只要 %d 个参数，但给了至少 %d 个。", zh, len(params), len(pos)),
				fmt.Sprintf("%s takes %d arguments but got at least %d.", zh, len(params), len(pos)),
				"参数个数要与定义一致。", line)
		}
		out[i] = v
		filled[i] = true
	}
	for alias, v := range named {
		matched := false
		for i, aliases := range params {
			for _, a := range aliases {
				if strings.TrimSuffix(a, "!") == alias {
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
			for _, aliases := range params {
				for _, a := range aliases {
					all = append(all, strings.TrimSuffix(a, "!"))
				}
			}
			sug := errs.DidYouMean(alias, all)
			if sug != "" {
				return nil, errs.RuntimeHint(
					fmt.Sprintf("%s 没有名叫 \"%s\" 的参数。你是不是想写 \"%s\"？", zh, alias, sug),
					fmt.Sprintf("%s has no argument \"%s\". Did you mean \"%s\"?", zh, alias, sug),
					"参数名可以用中文或英文别名。", line)
			}
			return nil, errs.RuntimeHint(
				fmt.Sprintf("%s 没有名叫 \"%s\" 的参数。", zh, alias),
				fmt.Sprintf("%s has no argument \"%s\".", zh, alias),
				"参数名可以用中文或英文别名。", line)
		}
	}
	for i := range out {
		if !filled[i] {
			for _, a := range params[i] {
				if strings.HasSuffix(a, "!") {
					return nil, errs.RuntimeHint(
						fmt.Sprintf("%s 缺少参数 %s。", zh, strings.TrimSuffix(a, "!")),
						fmt.Sprintf("%s is missing argument %s.", zh, strings.TrimSuffix(a, "!")),
						"参数按位置给，或用 名字: 值 按名给。", line)
				}
			}
			out[i] = omitted{}
		}
	}
	return out, nil
}

func (m *modBuilder) val(zh, en string, v Value) {
	m.d.SetNew("s:"+zh, v)
	m.d.SetNew("s:"+en, v)
}

// ---------- 随机/random ----------

var (
	rngMu sync.Mutex
	rng   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func modRandom() *Dict {
	m := newMod("随机", "random")
	m.fn("数", "int", [][]string{{"最小!"}, {"最大!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		a, e := needIntMod(args, 0, "随机", "数", line)
		if e != nil {
			return nil, e
		}
		b, e := needIntMod(args, 1, "随机", "数", line)
		if e != nil {
			return nil, e
		}
		if a > b {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("最小 %d 比最大 %d 还大。", a, b),
				fmt.Sprintf("min %d is greater than max %d.", a, b),
				"范围写反了；想倒着来可以先 生成列表 再 挑。", line)
		}
		rngMu.Lock()
		n := rng.Intn(b-a+1) + a
		rngMu.Unlock()
		return big.NewRat(int64(n), 1), nil
	})
	m.fn("小数", "float", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		rngMu.Lock()
		f := rng.Float64()
		rngMu.Unlock()
		return floatToRat(f), nil
	})
	m.fn("挑", "pick", [][]string{{"列表!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		l, ok := opt(args, 0)
		if !ok {
			return nil, errs.RuntimeHint("挑() 要告诉它从哪个列表挑。", "pick() needs a list.", "", line)
		}
		list, ok2 := l.(*List)
		if !ok2 {
			return nil, errs.RuntimeHint(
				"挑() 的参数要是列表，收到的是"+TypeName(l)+"。", "pick() needs a list.", "", line)
		}
		if len(list.Items) == 0 {
			return nil, errs.RuntimeHint(
				"空列表挑不出东西。", "cannot pick from an empty list.", "", line)
		}
		rngMu.Lock()
		i := rng.Intn(len(list.Items))
		rngMu.Unlock()
		return list.Items[i], nil
	})
	m.fn("洗牌", "shuffle", [][]string{{"列表!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		l, ok := opt(args, 0)
		if !ok {
			return nil, errs.RuntimeHint("洗牌() 要告诉它洗哪个列表。", "shuffle() needs a list.", "", line)
		}
		list, ok2 := l.(*List)
		if !ok2 {
			return nil, errs.RuntimeHint(
				"洗牌() 的参数要是列表，收到的是"+TypeName(l)+"。", "shuffle() needs a list.", "", line)
		}
		out := make([]Value, len(list.Items))
		copy(out, list.Items)
		rngMu.Lock()
		rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		rngMu.Unlock()
		return &List{Items: out}, nil // 返回新列表（全局函数不改原值）
	})
	m.fn("种子", "seed", [][]string{{"n!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		n, e := needIntMod(args, 0, "随机", "种子", line)
		if e != nil {
			return nil, e
		}
		rngMu.Lock()
		rng = rand.New(rand.NewSource(int64(n)))
		rngMu.Unlock()
		return nil, nil
	})
	return m.d
}

func needIntMod(args []Value, i int, mod, fn string, line int) (int, *errs.Error) {
	r, e := needNum(args, i, mod, fn, line)
	if e != nil {
		return 0, e
	}
	if !r.IsInt() {
		return 0, errs.RuntimeHint(
			fmt.Sprintf("%s.%s 的第 %d 个参数必须是整数，收到的是 %s。", mod, fn, i+1, FmtNum(r)),
			fmt.Sprintf("%s.%s argument #%d must be an integer.", mod, fn, i+1), "", line)
	}
	return int(new(big.Int).Quo(r.Num(), r.Denom()).Int64()), nil
}

// ---------- 时间/time ----------

func timeDict(t time.Time) *Dict {
	d := NewDict()
	week := []string{"日", "一", "二", "三", "四", "五", "六"}
	set := func(k string, v int64) { d.SetNew("s:"+k, big.NewRat(v, 1)) }
	set("年", int64(t.Year()))
	set("月", int64(t.Month()))
	set("日", int64(t.Day()))
	set("时", int64(t.Hour()))
	set("分", int64(t.Minute()))
	set("秒", int64(t.Second()))
	d.SetNew("s:星期", week[int(t.Weekday())])
	return d
}

func modTime() *Dict {
	m := newMod("时间", "time")
	m.fn("现在", "now", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		return timeDict(time.Now()), nil
	})
	m.fn("文本", "text", [][]string{{"字典!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		d, ok := opt(args, 0)
		if !ok {
			return nil, errs.RuntimeHint("文本() 要一个时间字典。", "time.text() needs a time dict.", "", line)
		}
		dd, ok2 := d.(*Dict)
		if !ok2 {
			return nil, errs.RuntimeHint(
				"时间.文本() 的参数要是字典，收到的是"+TypeName(d)+"。",
				"time.text() needs a dict.", "", line)
		}
		get := func(k string) int64 {
			if v, ok := dd.Get("s:" + k); ok {
				if r, ok2 := v.(*big.Rat); ok2 && r.IsInt() {
					return new(big.Int).Quo(r.Num(), r.Denom()).Int64()
				}
			}
			return 0
		}
		return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d",
			get("年"), get("月"), get("日"), get("时"), get("分"), get("秒")), nil
	})
	m.fn("解析", "parse", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "时间", "解析", line)
		if e != nil {
			return nil, e
		}
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return timeDict(t), nil
			}
		}
		return nil, errs.RuntimeHint(
			"认不出这个时间文本："+s+"。", "cannot parse this time text: "+s+".",
			"支持的写法：2026-09-15 20:30:00、2026-09-15 20:30 或 2026-09-15。", line)
	})
	m.fn("戳", "unix", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		return big.NewRat(time.Now().Unix(), 1), nil
	})
	m.fn("计时", "tick", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		return big.NewRat(time.Now().UnixNano(), 1), nil
	})
	m.fn("耗时", "elapsed", [][]string{{"起点!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		start, e := needIntMod(args, 0, "时间", "耗时", line)
		if e != nil {
			return nil, e
		}
		elapsed := float64(time.Now().UnixNano()-int64(start)) / 1e9
		return floatToRat(elapsed), nil
	})
	return m.d
}

// ---------- 数学/math ----------

func modMath() *Dict {
	m := newMod("数学", "math")
	m.val("圆周率", "pi", floatToRat(math.Pi))
	unary := func(zh, en string, f func(float64) float64, domainErr string) {
		m.fn(zh, en, [][]string{{"x!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
			x, e := needNum(args, 0, "数学", zh, line)
			if e != nil {
				return nil, e
			}
			xf, _ := x.Float64()
			r := f(xf)
			if math.IsNaN(r) || math.IsInf(r, 0) {
				msg := domainErr
				if msg == "" {
					msg = "结果不是实数。"
				}
				return nil, errs.RuntimeHint(msg, msg, "检查输入是否在定义域内。", line)
			}
			return floatToRat(r), nil
		})
	}
	unary("对数", "log", math.Log, "0 和负数没有对数。")
	unary("常用对数", "log10", math.Log10, "0 和负数没有对数。")
	unary("正弦", "sin", math.Sin, "")
	unary("余弦", "cos", math.Cos, "")
	unary("正切", "tan", math.Tan, "")
	unary("向上取整", "ceil", math.Ceil, "")
	unary("向下取整", "floor", math.Floor, "")
	m.fn("幂", "pow", [][]string{{"a!"}, {"b!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		a, e := needNum(args, 0, "数学", "幂", line)
		if e != nil {
			return nil, e
		}
		b, e := needNum(args, 1, "数学", "幂", line)
		if e != nil {
			return nil, e
		}
		af, _ := a.Float64()
		bf, _ := b.Float64()
		r := math.Pow(af, bf)
		if math.IsNaN(r) {
			return nil, errs.RuntimeHint(
				fmt.Sprintf("(%s) 的 (%s) 次幂不是实数。", FmtNum(a), FmtNum(b)),
				"the power is not a real number.",
				"负数只有整数次幂；0 的 0 次幂没有定义。", line)
		}
		return floatToRat(r), nil
	})
	intBin := func(zh, en string, f func(a, b *big.Int) *big.Int) {
		m.fn(zh, en, [][]string{{"a!"}, {"b!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
			a, e := needIntMod(args, 0, "数学", zh, line)
			if e != nil {
				return nil, e
			}
			b, e := needIntMod(args, 1, "数学", zh, line)
			if e != nil {
				return nil, e
			}
			ba := big.NewInt(int64(a))
			bb := big.NewInt(int64(b))
			return new(big.Rat).SetInt(f(ba, bb)), nil
		})
	}
	intBin("最大公约数", "gcd", func(a, b *big.Int) *big.Int {
		return new(big.Int).GCD(nil, nil, new(big.Int).Abs(a), new(big.Int).Abs(b))
	})
	intBin("最小公倍数", "lcm", func(a, b *big.Int) *big.Int {
		if a.Sign() == 0 || b.Sign() == 0 {
			return big.NewInt(0)
		}
		g := new(big.Int).GCD(nil, nil, new(big.Int).Abs(a), new(big.Int).Abs(b))
		return new(big.Int).Div(new(big.Int).Abs(new(big.Int).Mul(a, b)), g)
	})
	return m.d
}

// ---------- 编码/encoding ----------

func modEncoding() *Dict {
	m := newMod("编码", "encoding")
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	percentEncode := func(s string) string {
		var sb strings.Builder
		for _, r := range []byte(s) {
			if strings.IndexByte(unreserved, r) >= 0 {
				sb.WriteByte(r)
			} else {
				sb.WriteString(fmt.Sprintf("%%%02X", r))
			}
		}
		return sb.String()
	}
	percentDecode := func(s string, line int) (string, *errs.Error) {
		var out []byte
		for i := 0; i < len(s); i++ {
			if s[i] == '%' {
				if i+2 >= len(s) {
					return "", errs.RuntimeHint(
						"网址解码在末尾发现了不完整的 % 序列。",
						"URL decoding found a truncated % escape at the end.",
						"% 后面要跟两位十六进制数字。", line)
				}
				hi, ok1 := hexVal(s[i+1])
				lo, ok2 := hexVal(s[i+2])
				if !ok1 || !ok2 {
					return "", errs.RuntimeHint(
						fmt.Sprintf("网址解码在 %%%c%c 处遇到非十六进制字符。", s[i+1], s[i+2]),
						"URL decoding found non-hex digits after %.",
						"% 后面要跟两位十六进制数字（0-9、A-F）。", line)
				}
				out = append(out, byte(hi*16+lo))
				i += 2
			} else {
				out = append(out, s[i])
			}
		}
		return string(out), nil
	}
	m.fn("网址", "url_encode", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "编码", "网址", line)
		if e != nil {
			return nil, e
		}
		_ = url.QueryEscape // 保持 import
		return percentEncode(s), nil
	})
	m.fn("网址解码", "url_decode", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "编码", "网址解码", line)
		if e != nil {
			return nil, e
		}
		return percentDecode(s, line)
	})
	m.fn("六十四", "base64", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "编码", "六十四", line)
		if e != nil {
			return nil, e
		}
		return base64Encode(s), nil
	})
	m.fn("六十四解码", "base64_decode", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "编码", "六十四解码", line)
		if e != nil {
			return nil, e
		}
		return base64Decode(s, line)
	})
	m.fn("十六进制", "hex", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "编码", "十六进制", line)
		if e != nil {
			return nil, e
		}
		return fmt.Sprintf("%x", []byte(s)), nil
	})
	m.fn("十六进制解码", "hex_decode", [][]string{{"文本!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		s, e := needTextArg(args, 0, "编码", "十六进制解码", line)
		if e != nil {
			return nil, e
		}
		if len(s)%2 != 0 {
			return nil, errs.RuntimeHint(
				"十六进制文本的长度必须是偶数。", "hex text must have an even length.", "", line)
		}
		out := make([]byte, 0, len(s)/2)
		for i := 0; i < len(s); i += 2 {
			hi, ok1 := hexVal(s[i])
			lo, ok2 := hexVal(s[i+1])
			if !ok1 || !ok2 {
				return nil, errs.RuntimeHint(
					fmt.Sprintf("十六进制文本在 %c%c 处有非法字符。", s[i], s[i+1]),
					"invalid hex digits.",
					"十六进制只能有 0-9 和 A-F（或 a-f）。", line)
			}
			out = append(out, byte(hi*16+lo))
		}
		return string(out), nil
	})
	return m.d
}

func hexVal(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	}
	return 0, false
}

// ---------- 系统/sys（仅服务端）----------

func modSystem() *Dict {
	m := newMod("系统", "sys")
	m.fn("参数", "args", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		src := in.ProgramArgs
		if src == nil {
			src = os.Args[1:]
		}
		items := make([]Value, len(src))
		for i, a := range src {
			items[i] = a
		}
		return &List{Items: items}, nil
	})
	m.fn("环境", "env", [][]string{{"名字!"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		name, e := needTextArg(args, 0, "系统", "环境", line)
		if e != nil {
			return nil, e
		}
		if v, ok := os.LookupEnv(name); ok {
			return v, nil
		}
		return nil, nil
	})
	m.fn("平台", "platform", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		return runtime.GOOS, nil
	})
	m.fn("退出", "exit", [][]string{{"码"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		code := 0
		if r, ok := opt(args, 0); ok {
			_ = r
			n, e := needIntMod(args, 0, "系统", "退出", line)
			if e != nil {
				return nil, e
			}
			code = n
		}
		return nil, &errs.Error{Kind: errs.KindRuntime, Zh: "程序已退出。", En: "program exited.", Exit: code}
	})
	return m.d
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func base64Decode(s string, line int) (string, *errs.Error) {
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", errs.RuntimeHint(
			"Base64 解码失败。", "Base64 decoding failed.",
			"Base64 只能有字母、数字、+ / 和补位 =。", line)
	}
	return string(out), nil
}

// moduleNamePair 内置模块的规范中英名。
func moduleNamePair(name string) (string, string) {
	switch name {
	case "net":
		return "网络", "net"
	case "page":
		return "页面", "page"
	case "random":
		return "随机", "random"
	case "time":
		return "时间", "time"
	case "math":
		return "数学", "math"
	case "encoding":
		return "编码", "encoding"
	case "sys":
		return "系统", "sys"
	}
	return name, ""
}

// registerStdModules 在 New() 里调用：预置全部标准库模块（双语名都指向同一字典）。
func registerStdModules(in *Interp) {
	register := func(m *Dict, zh, en string) {
		in.builtinMods[zh] = m
		in.builtinMods[en] = m
		in.Globals.Set(zh, m)
		in.Globals.Set(en, m)
	}
	register(modRandom(), "随机", "random")
	register(modTime(), "时间", "time")
	register(modMath(), "数学", "math")
	register(modEncoding(), "编码", "encoding")
	register(modSystem(), "系统", "sys")
}
