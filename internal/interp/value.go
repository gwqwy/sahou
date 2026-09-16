// Package interp 是树遍历求值器：值模型、环境、执行与内置函数。
package interp

import (
	"fmt"
	"math/big"
	"strings"

	"sahou/internal/errs"
	"sahou/internal/parser"
)

// ---------- 值模型（规范 5.1：五种类型 + 空值）----------

// Value 任意 sahou 值。具体承载见下方注释。
type Value = interface{}

//
// 数   → *big.Rat（唯一"数"类型的内部承载：精确有理数，整数即分母为 1）
// 文本 → string
// 布尔 → bool
// 列表 → *List
// 字典 → *Dict（保持插入顺序，规范 6.4）
// 空值 → nil
// 函数 → *SahouFn / *Builtin / *BoundMember（一等公民，但不是第五种"类型"）

type List struct {
	Items []Value
}

// Dict 有序字典：keys 保存插入顺序，m 保存键编码 → 值。
type Dict struct {
	keys []string
	m    map[string]Value
}

func NewDict() *Dict {
	return &Dict{m: make(map[string]Value)}
}

// KeyEncode 字典键编码：文本键与数键互不冲突；数键按值的精确形式编码，
// 因此 1 和 1.0 是同一个键（它们是同一个数，规范 5.10 深度相等）。
func KeyEncode(v Value) (string, bool) {
	switch x := v.(type) {
	case string:
		return "s:" + x, true
	case *big.Rat:
		return "n:" + x.RatString(), true
	}
	return "", false
}

func (d *Dict) Len() int { return len(d.keys) }

func (d *Dict) Has(key string) bool {
	_, ok := d.m[key]
	return ok
}

func (d *Dict) Get(key string) (Value, bool) {
	v, ok := d.m[key]
	return v, ok
}

// SetNew 写入键值，报告是否插入了新键。
func (d *Dict) SetNew(key string, v Value) bool {
	isNew := !d.Has(key)
	if isNew {
		d.keys = append(d.keys, key)
	}
	d.m[key] = v
	return isNew
}

func (d *Dict) Delete(key string) bool {
	if !d.Has(key) {
		return false
	}
	delete(d.m, key)
	for i, k := range d.keys {
		if k == key {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
	return true
}

// Keys 返回按插入顺序的原始键编码。
func (d *Dict) Keys() []string { return d.keys }

// KeysAsValues 把键编码还原成值（文本键 → string；数键 → *big.Rat）。
func (d *Dict) KeysAsValues() []Value {
	out := make([]Value, len(d.keys))
	for i, k := range d.keys {
		if strings.HasPrefix(k, "s:") {
			out[i] = k[2:]
		} else {
			r, _ := new(big.Rat).SetString(k[2:])
			out[i] = r
		}
	}
	return out
}

type SahouFn struct {
	Name   string // 匿名函数为 ""
	Params []string
	Body   []parser.Stmt
	Env    *Env
}

// Builtin 内置函数：中英双名指向同一个实现（规范 6.1）。
type Builtin struct {
	Zh, En string
	Call   func(in *Interp, args []Value, named map[string]Value, line int) (Value, *errs.Error)
}

func (b *Builtin) DisplayName() string {
	if b.En == "" {
		return b.Zh
	}
	return b.Zh + "/" + b.En
}

// BoundMember 成员函数（列表.添加 之类），持有接收者。
type BoundMember struct {
	Recv Value
	Zh   string
	En   string
	Call func(in *Interp, recv Value, args []Value, named map[string]Value, line int) (Value, *errs.Error)
}

func (b *BoundMember) DisplayName() string { return b.Zh + "/" + b.En }

// ---------- 类型名 ----------

func TypeName(v Value) string {
	switch x := v.(type) {
	case *big.Rat:
		return "数"
	case string:
		return "文本"
	case bool:
		return "布尔"
	case *List:
		return "列表"
	case *Dict:
		return "字典"
	case nil:
		return "空值"
	case *SahouFn:
		return "函数"
	case *Builtin:
		return "函数"
	case *BoundMember:
		return "函数"
	case *Server:
		return "字典" // 网络.服务() 对用户就是一个普通字典（05 文档 2.2）
	case *Database:
		return "字典" // 数据库.打开() 对用户呈现为普通字典
	case interface{ isPageEl() }:
		return "字典" // 元素句柄（规范：类型(元素) 是 dict）
	default:
		_ = x
		return "未知"
	}
}

// TypeEnName 规范 6.2 第 11 条：类型(值) 返回英文名。
func TypeEnName(v Value) string {
	switch v.(type) {
	case *big.Rat:
		return "number"
	case string:
		return "string"
	case bool:
		return "bool"
	case *List:
		return "list"
	case *Dict:
		return "dict"
	case *Server:
		return "dict"
	case *Database:
		return "dict"
	case interface{ isPageEl() }:
		return "dict"
	case nil:
		return "null"
	}
	return "function"
}

// ---------- 真值（规范 5.4：只有 6 个假值）----------

func Truthy(v Value) bool {
	switch x := v.(type) {
	case *big.Rat:
		return x.Sign() != 0
	case string:
		return x != ""
	case bool:
		return x
	case *List:
		return len(x.Items) > 0
	case *Dict:
		return x.Len() > 0
	case nil:
		return false
	default:
		return true // 函数恒为真
	}
}

// ---------- 显示与转换（规范 5.10）----------

// Str 把任意值转成文本：与打印、插值同一套规则。
func Str(v Value) string {
	var sb strings.Builder
	writeValue(&sb, v, map[*List]bool{}, map[*Dict]bool{})
	return sb.String()
}

func writeValue(sb *strings.Builder, v Value, ls map[*List]bool, ds map[*Dict]bool) {
	switch x := v.(type) {
	case *big.Rat:
		sb.WriteString(FmtNum(x))
	case string:
		sb.WriteString(x)
	case bool:
		if x {
			sb.WriteString("真")
		} else {
			sb.WriteString("假")
		}
	case nil:
		sb.WriteString("空值")
	case *List:
		if ls[x] {
			sb.WriteString("<循环引用>")
			return
		}
		ls[x] = true
		sb.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeQuoted(sb, it, ls, ds)
		}
		sb.WriteByte(']')
		delete(ls, x)
	case *Dict:
		if ds[x] {
			sb.WriteString("<循环引用>")
			return
		}
		ds[x] = true
		sb.WriteByte('{')
		keyVals := x.KeysAsValues()
		for i, k := range x.keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeQuoted(sb, keyVals[i], ls, ds)
			sb.WriteString(": ")
			writeQuoted(sb, x.m[k], ls, ds)
		}
		sb.WriteByte('}')
		delete(ds, x)
	case *SahouFn:
		if x.Name == "" {
			sb.WriteString("<匿名函数>")
		} else {
			sb.WriteString("<函数 " + x.Name + ">")
		}
	case *Builtin:
		sb.WriteString("<内置函数 " + x.DisplayName() + ">")
	case *BoundMember:
		sb.WriteString("<成员函数 " + x.DisplayName() + ">")
	case *Server:
		sb.WriteString("<网络服务>")
	case *Database:
		sb.WriteString("<数据库>")
	case interface{ isPageEl() }:
		sb.WriteString("<元素>")
	}
}

// writeQuoted 容器内部的元素按"可粘贴回代码"的形式写：文本带引号并转义。
func writeQuoted(sb *strings.Builder, v Value, ls map[*List]bool, ds map[*Dict]bool) {
	if s, ok := v.(string); ok {
		sb.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"':
				sb.WriteString("\\\"")
			case '\\':
				sb.WriteString("\\\\")
			case '\n':
				sb.WriteString("\\n")
			case '\t':
				sb.WriteString("\\t")
			default:
				sb.WriteRune(r)
			}
		}
		sb.WriteByte('"')
		return
	}
	writeValue(sb, v, ls, ds)
}

// ---------- 深度相等（规范 5.10；自引用容器比较报错）----------

func DeepEqual(a, b Value, seen map[[2]interface{}]bool) (bool, *errs.Error) {
	ra, aok := a.(*big.Rat)
	rb, bok := b.(*big.Rat)
	if aok && bok {
		return ra.Cmp(rb) == 0, nil
	}
	if aok != bok {
		return false, nil
	}
	la, aok := a.(*List)
	lb, bok := b.(*List)
	if aok || bok {
		if !aok || !bok {
			return false, nil
		}
		key := [2]interface{}{la, lb}
		if seen[key] {
			return false, errs.Runtime("数据里出现了自己套自己的结构，没法比较。", "cannot compare self-referencing structures.", 0)
		}
		seen[key] = true
		defer delete(seen, key)
		if len(la.Items) != len(lb.Items) {
			return false, nil
		}
		for i := range la.Items {
			eq, e := DeepEqual(la.Items[i], lb.Items[i], seen)
			if e != nil {
				return false, e
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	}
	da, aok := a.(*Dict)
	db, bok := b.(*Dict)
	if aok || bok {
		if !aok || !bok {
			return false, nil
		}
		key := [2]interface{}{da, db}
		if seen[key] {
			return false, errs.Runtime("数据里出现了自己套自己的结构，没法比较。", "cannot compare self-referencing structures.", 0)
		}
		seen[key] = true
		defer delete(seen, key)
		if da.Len() != db.Len() {
			return false, nil
		}
		for _, k := range da.keys {
			bv, ok := db.m[k]
			if !ok {
				return false, nil
			}
			eq, e := DeepEqual(da.m[k], bv, seen)
			if e != nil {
				return false, e
			}
			if !eq {
				return false, nil
			}
		}
		return true, nil
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv, nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv, nil
	case nil:
		return b == nil, nil
	}
	// 函数按引用比较
	return a == b, nil
}

// ---------- 数的辅助（规范 5.2）----------

func IsNum(v Value) bool {
	_, ok := v.(*big.Rat)
	return ok
}

func AsNum(v Value) *big.Rat { return v.(*big.Rat) }

// IsIntVal 数值是否为整数形式（索引、步长、重复次数用）。
func IsIntVal(v Value) bool {
	r, ok := v.(*big.Rat)
	return ok && r.IsInt()
}

// IntOf 数值 → int64；超出范围报"数太大"（可接住）。
func IntOf(v Value, line int) (int, *errs.Error) {
	r := v.(*big.Rat)
	if !r.IsInt() {
		return 0, errs.Runtime("这里需要一个整数，但给的是 "+FmtNum(r)+"。", "an integer is required here, got "+FmtNum(r)+".", line)
	}
	i := new(big.Int).Quo(r.Num(), r.Denom())
	if !i.IsInt64() {
		return 0, errs.Runtime("这个数太大了："+FmtNum(r)+"。", "this number is too large: "+FmtNum(r)+".", line)
	}
	return int(i.Int64()), nil
}

// FmtNum 数的打印：最短且能唯一还原的十进制写法，永不出现 10.0（规范 5.10）。
func FmtNum(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String() // 分母为 1，直接输出整数
	}
	// 非整数：15 位有效数字（规范 5.2），再展开成普通十进制、去尾零
	f := new(big.Float).SetPrec(200).SetRat(r)
	s := f.Text('g', 15)
	return expandSci(s)
}

// expandSci 把 big.Float 的 g 格式（可能带 e±xx）展开成普通十进制并去尾零。
func expandSci(s string) string {
	mant := s
	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant = s[:i]
		fmt.Sscanf(s[i+1:], "%d", &exp)
	}
	neg := strings.HasPrefix(mant, "-")
	if neg {
		mant = mant[1:]
	}
	mant = strings.TrimSuffix(mant, ".")
	digits := strings.Replace(mant, ".", "", 1)
	dot := strings.Index(mant, ".")
	if dot < 0 {
		dot = len(mant)
	}
	// 小数点在 digits 中的位置
	point := dot + exp
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	var out string
	switch {
	case point <= 0:
		out = "0." + strings.Repeat("0", -point) + digits
	case point >= len(digits):
		out = digits + strings.Repeat("0", point-len(digits))
	default:
		out = digits[:point] + "." + digits[point:]
	}
	if neg {
		out = "-" + out
	}
	return out
}
