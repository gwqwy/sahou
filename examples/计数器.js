// 由 sahou build 生成 —— 请勿手改
// saho_rt.js — sahou（卅）build 产物的运行时前导库。
// 由 `sahou build` 内联到生成的 JS 顶部；请勿手改生成文件。
// 语义对齐 v1 解释器（规范 5）：真值 6 假值、深度相等、余数符号、15 位有效数字。
"use strict";
function __saho_num(x) {
  if (typeof x !== "number" || !isFinite(x)) {
    throw { __saho: true, message: "这里需要一个数。a number is required here." };
  }
  if (Number.isInteger(x)) return x;
  return Number(x.toPrecision(15));
}
function __saho_truthy(v) {
  if (v === null || v === false || v === "") return false;
  if (typeof v === "number") return v !== 0;
  if (__saho_isList(v)) return v.length > 0;
  if (__saho_isDict(v)) return v.size > 0;
  return true;
}
function __saho_isList(v) { return Array.isArray(v); }
function __saho_isDict(v) { return v instanceof Map; }
function __saho_isFn(v) { return typeof v === "function"; }
function __saho_isNum(v) { return typeof v === "number"; }
function __saho_isElement(v) {
  return v !== null && typeof v === "object" && v.__saho_element === true;
}
function __saho_type(v) {
  if (v === null) return "null";
  if (typeof v === "boolean") return "bool";
  if (typeof v === "number") return "number";
  if (typeof v === "string") return "string";
  if (__saho_isList(v)) return "list";
  if (__saho_isDict(v) || __saho_isElement(v)) return "dict";
  return "function";
}
function __saho_shortest(n) {
  // 最短十进制：与解释器的“最短且能唯一还原”一致
  if (Number.isInteger(n) && Math.abs(n) < 1e21) return String(n);
  return String(Number(n.toPrecision(15)));
}
function __saho_str(v) { return __saho_strInner(v, [], []); }
function __saho_strInner(v, ls, ds) {
  if (v === null) return "空值";
  if (typeof v === "boolean") return v ? "真" : "假";
  if (typeof v === "number") return __saho_shortest(v);
  if (typeof v === "string") return v;
  if (__saho_isList(v)) {
    if (ls.indexOf(v) >= 0) return "<循环引用>";
    ls.push(v);
    var parts = v.map(function (it) { return __saho_strQuoted(it, ls, ds); });
    ls.pop();
    return "[" + parts.join(", ") + "]";
  }
  if (__saho_isDict(v)) {
    if (ds.indexOf(v) >= 0) return "<循环引用>";
    ds.push(v);
    var parts2 = [];
    v.forEach(function (val, key) {
      parts2.push(__saho_strQuoted(key, ls, ds) + ": " + __saho_strQuoted(val, ls, ds));
    });
    ds.pop();
    return "{" + parts2.join(", ") + "}";
  }
  if (__saho_isElement(v)) return "<元素>";
  if (typeof v === "function") {
    return v.__saho_name ? "<函数 " + v.__saho_name + ">" : "<匿名函数>";
  }
  return "<未知>";
}
function __saho_strQuoted(v, ls, ds) {
  if (typeof v === "string") {
    var s = v.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n").replace(/\t/g, "\\t");
    return '"' + s + '"';
  }
  return __saho_strInner(v, ls, ds);
}
function __saho_eq(a, b) {
  if (typeof a === "number" && typeof b === "number") return a === b;
  if (a === null || b === null) return a === b;
  if (__saho_isList(a) && __saho_isList(b)) {
    if (a === b) return true;
    if (a.length !== b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!__saho_eq(a[i], b[i])) return false;
    }
    return true;
  }
  if (__saho_isDict(a) && __saho_isDict(b)) {
    if (a === b) return true;
    if (a.size !== b.size) return false;
    var ok = true;
    a.forEach(function (av, ak) {
      if (!ok || !b.has(ak) || !__saho_eq(av, b.get(ak))) ok = false;
    });
    return ok;
  }
  return a === b;
}
function __saho_cmp(a, b) {
  if (typeof a === "number" && typeof b === "number") return a < b ? -1 : a > b ? 1 : 0;
  if (typeof a === "string" && typeof b === "string") return a < b ? -1 : a > b ? 1 : 0;
  throw {
    __saho: true,
    message: TypeName2(a) + " 和 " + TypeName2(b) + " 没有大小之分，不能比较。" +
      TypeName2(a) + " and " + TypeName2(b) + " are not order-comparable.",
    hint: "< <= > >= 只能比较两个数或两个文本；判断是否相等用 ==。"
  };
}
function TypeName2(v) {
  if (v === null) return "空值";
  if (typeof v === "boolean") return "布尔";
  if (typeof v === "number") return "数";
  if (typeof v === "string") return "文本";
  if (__saho_isList(v)) return "列表";
  if (__saho_isDict(v)) return "字典";
  return "函数";
}
function __saho_lt(a, b) { return __saho_cmp(a, b) < 0; }
function __saho_le(a, b) { return __saho_cmp(a, b) <= 0; }
function __saho_gt(a, b) { return __saho_cmp(a, b) > 0; }
function __saho_ge(a, b) { return __saho_cmp(a, b) >= 0; }
function __saho_add(a, b) {
  if (typeof a === "number" && typeof b === "number") return __saho_num(a + b);
  if (typeof a === "string" && typeof b === "string") return a + b;
  if (__saho_isList(a) && __saho_isList(b)) return a.concat(b);
  var zh = "不能把 " + TypeName2(a) + " 和 " + TypeName2(b) + " 相加。";
  var hint = __saho_isNum(a) || __saho_isNum(b)
    ? "你可能是想把数字转换成文本，试试 文本(…)。"
    : "相加的两边要么都是数，要么都是文本，要么都是列表。";
  throw { __saho: true, message: zh + "cannot add " + TypeName2(a) + " and " + TypeName2(b) + ".", hint: hint };
}
function __saho_sub(a, b) {
  __saho_needNums(a, b, "-");
  return __saho_num(a - b);
}
function __saho_mul(a, b) {
  if (typeof a === "number" && typeof b === "number") return __saho_num(a * b);
  if (typeof a === "string" && typeof b === "number") return __saho_repeatStr(a, b);
  if (typeof a === "number" && typeof b === "string") return __saho_repeatStr(b, a);
  if (__saho_isList(a) && typeof b === "number") return __saho_repeatList(a, b);
  if (typeof a === "number" && __saho_isList(b)) return __saho_repeatList(b, a);
  throw { __saho: true, message: "这两个类型之间不能做 * 运算。cannot apply * to these types." };
}
function __saho_repeatStr(s, n) {
  if (n < 0) throw { __saho: true, message: "重复次数不能是负数。repeat count cannot be negative.", hint: "负数相当于重复 0 次，直接写 0 或 \"\" 即可。" };
  return s.repeat(n);
}
function __saho_repeatList(items, n) {
  if (n < 0) throw { __saho: true, message: "重复次数不能是负数。repeat count cannot be negative.", hint: "负数相当于重复 0 次，直接写 [] 即可。" };
  var out = [];
  for (var i = 0; i < n; i++) out = out.concat(items);
  return out;
}
function __saho_div(a, b) {
  __saho_needNums(a, b, "/");
  if (b === 0) throw { __saho: true, message: "不能除以 0。cannot divide by zero." };
  return __saho_num(a / b);
}
function __saho_mod(a, b) {
  __saho_needNums(a, b, "%");
  if (b === 0) throw { __saho: true, message: "不能对 0 取余。cannot take remainder by zero." };
  // 余数符号跟随被除数（-7 % 3 得 2）：((a % b) + b) % b
  return __saho_num(((a % b) + b) % b);
}
function __saho_needNums(a, b, op) {
  if (typeof a !== "number" || typeof b !== "number") {
    throw { __saho: true, message: "这两个类型之间不能做 " + op + " 运算。cannot apply " + op + " to these types.", hint: "检查两边的类型；需要转换时用 数(...)。" };
  }
}
function __saho_neg(x) {
  if (typeof x !== "number") throw { __saho: true, message: "- 号后面要跟一个数。- needs a number." };
  return -x;
}
function __saho_throw(msg) {
  if (typeof msg !== "string") {
    throw { __saho: true, message: "抛出() 的消息必须是文本。throw() requires a text message." };
  }
  throw { __saho: true, message: msg };
}
function __saho_error_text(e) {
  if (e && e.__saho) {
    var m = e.message || "";
    if (e.hint) m += " " + e.hint;
    if (e.line) m = "[sahou 第 " + e.line + " 行] " + m;
    return m;
  }
  if (typeof console !== "undefined") console.error(e);
  return "程序内部出错，请把控制台里的内容报给作者。internal error.";
}
function __saho_iter(seq) {
  if (__saho_isList(seq)) return seq;
  if (typeof seq === "string") return seq.split("");
  if (__saho_isDict(seq)) return Array.from(seq.keys());
  throw { __saho: true, message: TypeName2(seq) + " 不是可以遍历的值。not iterable.", hint: "能遍历的有：列表、文本（按字符）、字典（按键）。" };
}
function __saho_intlike(n) {
  return typeof n === "number" && isFinite(n) && Math.floor(n) === n;
}
function __saho_index(x, i) {
  if (__saho_isList(x) || typeof x === "string") {
    if (!__saho_intlike(i)) throw { __saho: true, message: "下标必须是整数，收到的是 " + __saho_shortest(i) + "。index must be an integer." };
    if (i < 0) throw { __saho: true, message: "下标不能是负数。index cannot be negative.", hint: "想取最后一个元素，写 名单[长度(名单) - 1]。" };
    var n = __saho_isList(x) ? x.length : x.length;
    if (i >= n) throw { __saho: true, message: (typeof x === "string" ? "这段文本" : "列表") + "只有 " + n + " 个" + (typeof x === "string" ? "字符" : "元素") + "，取不到第 " + i + " 个（下标从 0 开始，合法范围是 0 到 " + (n - 1) + "）。", hint: "取一段请用 取(值, 起, 止)。" };
    return x[i];
  }
  if (__saho_isDict(x)) return __saho_dict_get(x, i);
  throw { __saho: true, message: TypeName2(x) + " 不能按下标取值。not indexable." };
}
function __saho_set(x, k, v) {
  if (__saho_isDict(x)) { __saho_dict_set(x, k, v); return null; }
  if (__saho_isList(x)) {
    if (!__saho_intlike(k)) throw { __saho: true, message: "列表下标必须是整数。list index must be an integer." };
    if (k < 0) throw { __saho: true, message: "列表下标不能是负数。list index cannot be negative.", hint: "想取最后一个元素，写 名单[长度(名单) - 1]。" };
    if (k >= x.length) throw { __saho: true, message: "列表只有 " + x.length + " 个元素，第 " + k + " 个位置放不进去（下标从 0 开始，合法范围是 0 到 " + (x.length - 1) + "）。", hint: "想在末尾加一个元素，写 名单.添加(值)。" };
    x[k] = v;
    return null;
  }
  if (typeof x === "string") throw { __saho: true, message: "文本不能按下标修改。strings are immutable.", hint: "用 替换、分割、连接 组合出新文本。" };
  throw { __saho: true, message: TypeName2(x) + " 不能按下标赋值。not index-assignable." };
}
function __saho_key(k) {
  if (typeof k === "string" || typeof k === "number") return k;
  throw { __saho: true, message: "字典的键只能是文本或数。dict keys must be text or a number." };
}
function __saho_dict_get(d, k) {
  if (d.has(k)) return d.get(k);
  var cands = [];
  d.forEach(function (_v, key) { cands.push(__saho_str(key)); });
  var want = __saho_str(k);
  var sug = __saho_suggest(want, cands);
  var msg = "字典里没有键 " + want + "。" + (sug ? "你是不是想写 \"" + sug + "\"？" : "");
  throw { __saho: true, message: msg, hint: "现有的键有：" + cands.join("、") + "。" };
}
function __saho_dict_set(d, k, v) {
  d.set(__saho_key(k), v);
  return null;
}
function __saho_dict_remove(d, k) {
  if (!d.has(k)) {
    var cands = [];
    d.forEach(function (_v, key) { cands.push(__saho_str(key)); });
    var want = __saho_str(k);
    var sug = __saho_suggest(want, cands);
    throw { __saho: true, message: "字典里没有键 " + want + "，删不了。" + (sug ? "你是不是想写 \"" + sug + "\"？" : ""), hint: "现有的键有：" + cands.join("、") + "。" };
  }
  d.delete(k);
  return null;
}
function __saho_suggest(name, cands) {
  function dist(a, b) {
    var m = [];
    for (var i = 0; i <= a.length; i++) { m[i] = [i]; }
    for (var j = 0; j <= b.length; j++) { m[0][j] = j; }
    for (var i2 = 1; i2 <= a.length; i2++) {
      for (var j2 = 1; j2 <= b.length; j2++) {
        m[i2][j2] = Math.min(m[i2 - 1][j2] + 1, m[i2][j2 - 1] + 1, m[i2 - 1][j2 - 1] + (a[i2 - 1] === b[j2 - 1] ? 0 : 1));
      }
    }
    return m[a.length][b.length];
  }
  var best = "", bestD = 99;
  for (var i = 0; i < cands.length; i++) {
    var dd = dist(name, cands[i]);
    if (dd < bestD) { best = cands[i]; bestD = dd; }
  }
  var limit = name.length <= 4 ? 2 : 3;
  return bestD <= limit ? best : "";
}
function __saho_get(x, name) {
  if (__saho_isDict(x)) {
    if (x.has(name)) return x.get(name);
    var cands = [];
    x.forEach(function (_v, key) { cands.push(__saho_str(key)); });
    var sug = __saho_suggest(name, cands);
    throw { __saho: true, message: "字典里没有字段 " + name + "。" + (sug ? "你是不是想写 \"" + sug + "\"？" : ""), hint: "现有的字段有：" + cands.join("、") + "；也可以直接赋一个新字段。" };
  }
  if (__saho_isList(x)) {
    if (name === "添加" || name === "append") {
      return function (v) { x.push(v); return null; };
    }
    if (name === "去重" || name === "dedupe") {
      return function () {
        var out = [];
        for (var i = 0; i < x.length; i++) {
          var dup = false;
          for (var j = 0; j < out.length; j++) { if (__saho_eq(x[i], out[j])) { dup = true; break; } }
          if (!dup) out.push(x[i]);
        }
        x.length = 0;
        for (var k = 0; k < out.length; k++) x.push(out[k]);
        return null;
      };
    }
    throw { __saho: true, message: "列表没有成员 " + name + "。lists have no member " + name + ".", hint: "列表的成员只有 添加/append 和 去重/dedupe；其余操作用全局函数，比如 排序(...)。" };
  }
  if (typeof x === "string") {
    throw { __saho: true, message: "文本没有成员 " + name + "。strings have no members.", hint: "用全局函数，比如 转大写(...)、替换(...)。" };
  }
  throw { __saho: true, message: TypeName2(x) + " 没有成员。has no members." };
}
function __saho_defn(fn, params, name) {
  fn.__saho_params = params;
  fn.__saho_name = name;
  return fn;
}
function __saho_call(fn, pos, named) {
  if (fn && fn.__saho_params) {
    var ps = fn.__saho_params;
    if (pos.length > ps.length) {
      throw { __saho: true, message: "函数只要 " + ps.length + " 个参数，但给了至少 " + pos.length + " 个。too many arguments." };
    }
    var args = pos.slice();
    for (var i = pos.length; i < ps.length; i++) {
      var p = ps[i];
      if (named && Object.prototype.hasOwnProperty.call(named, p)) { args.push(named[p]); continue; }
      var sug = named ? __saho_suggest(p, Object.keys(named)) : "";
      throw { __saho: true, message: "函数缺少参数 " + p + "。" + (sug ? "你是不是想写 " + sug + "？" : "missing argument " + p + ".") };
    }
    if (named) {
      for (var key in named) {
        if (Object.prototype.hasOwnProperty.call(named, key) && ps.indexOf(key) < 0) {
          var sug2 = __saho_suggest(key, ps);
          throw { __saho: true, message: "函数没有名叫 " + key + " 的参数。" + (sug2 ? "你是不是想写 " + sug2 + "？" : "no parameter named " + key + ".") };
        }
      }
    }
    return fn.apply(null, args);
  }
  return fn.apply(null, pos);
}
// ---------- 内置函数（29 个；文件/输入类在浏览器不可用） ----------
function __saho_opt(opts, zh, en, dflt) {
  if (opts && typeof opts === "object") {
    if (Object.prototype.hasOwnProperty.call(opts, zh)) return opts[zh];
    if (Object.prototype.hasOwnProperty.call(opts, en)) return opts[en];
  }
  return dflt;
}
function __saho_print(v, opts) {
  var end = __saho_opt(opts, "结尾", "end", "\n");
  if (typeof console !== "undefined" && console.log) {
    // console.log 自带换行：把自定义结尾转成先内容后结尾两次输出
    console.log(__saho_str(v) + (end === "\n" ? "" : end));
  } else if (typeof process !== "undefined") {
    process.stdout.write(__saho_str(v) + end);
  }
  return null;
}
function __saho_tonum(v) {
  if (typeof v === "number") return v;
  if (typeof v === "boolean") return v ? 1 : 0;
  if (typeof v === "string") {
    var t = v.trim();
    if (t !== "" && !isNaN(Number(t))) return __saho_num(Number(t));
    var sug = __saho_zhNum(t);
    throw { __saho: true, message: "文本 \"" + v + "\" 不是数字。" + (sug ? "你是不是想写 " + sug + "？" : ""), hint: "文本里只能有数字和小数点，例如 \"95\" 或 \"3.5\"。" };
  }
  throw { __saho: true, message: "数() 只能转换 文本 或 布尔，收到的是" + TypeName2(v) + "。num() converts text or bool." };
}
function __saho_zhNum(s) {
  var digits = { "零": 0, "一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9 };
  var units = { "十": 10, "百": 100, "千": 1000 };
  var total = 0, section = 0, current = 0, any = false;
  for (var i = 0; i < s.length; i++) {
    var ch = s[i];
    if (ch in digits) { if (current < 10) current = current * 10 + digits[ch]; else return ""; any = true; }
    else if (ch in units) { if (current === 0) current = 1; section += current * units[ch]; current = 0; any = true; }
    else if (ch === "万") { section = (section + current) * 10000; total += section; section = 0; current = 0; any = true; }
    else return "";
  }
  if (!any) return "";
  return String(total + section + current);
}
function __saho_tolist(v) {
  if (v === null || v === undefined) return [];
  if (__saho_isList(v)) return v.slice();
  if (__saho_isDict(v)) return Array.from(v.keys());
  throw { __saho: true, message: "列表() 只接受 列表 或 字典，收到的是" + TypeName2(v) + "。" };
}
function __saho_todict(pairs) {
  var d = new Map();
  if (pairs === null || pairs === undefined) return d;
  if (!__saho_isList(pairs)) throw { __saho: true, message: "字典() 只接受配对列表，收到的是" + TypeName2(pairs) + "。" };
  for (var i = 0; i < pairs.length; i++) {
    var p = pairs[i];
    if (!__saho_isList(p) || p.length !== 2) throw { __saho: true, message: "配对列表的第 " + (i + 1) + " 项不是 [键, 值] 的形式。" };
    d.set(__saho_key(p[0]), p[1]);
  }
  return d;
}
function __saho_range_to(a, b, opts) {
  var step = __saho_opt(opts, "步长", "step", 1);
  if (step === 0) throw { __saho: true, message: "步长不能是 0。step cannot be 0." };
  var out = [];
  if (step > 0) { for (var i = a; i < b; i += step) out.push(i); }
  else { for (var j = a; j > b; j += step) out.push(j); }
  return out;
}
function __saho_len(v) {
  if (__saho_isList(v)) return v.length;
  if (typeof v === "string") return v.length;
  if (__saho_isDict(v)) return v.size;
  throw { __saho: true, message: "长度() 只能用在 列表、文本、字典 上，收到的是" + TypeName2(v) + "。" };
}
function __saho_slice(seq, opts) {
  if (!__saho_isList(seq) && typeof seq !== "string") {
    throw { __saho: true, message: "取() 只能用在 列表 或 文本 上，收到的是" + TypeName2(seq) + "。" };
  }
  var n = seq.length;
  var start = __saho_opt(opts, "起", "start", 0);
  var stopOmitted = !(opts && (("止" in opts) || ("stop" in opts)));
  var stop = __saho_opt(opts, "止", "stop", 0);
  var step = __saho_opt(opts, "步长", "step", 1);
  if (step === 0) throw { __saho: true, message: "步长不能是 0。step cannot be 0." };
  function intArg(v, zh, en) {
    if (!__saho_intlike(v)) throw { __saho: true, message: "取() 的参数 " + zh + " 必须是整数。" };
    return v;
  }
  start = intArg(start, "起", "start");
  if (start < 0) start += n;
  if (step > 0) {
    start = Math.max(0, Math.min(start, n));
    var e = stopOmitted ? n : intArg(stop, "止", "stop");
    if (e < 0) e += n;
    e = Math.max(0, Math.min(e, n));
    var idxs = [];
    for (var i = start; i < e; i += step) idxs.push(i);
  } else {
    start = Math.min(n - 1, start);
    var e2 = stopOmitted ? -1 : intArg(stop, "止", "stop");
    if (e2 < 0) e2 += n;
    e2 = Math.max(-1, e2);
    var idxs2 = [];
    for (var j = start; j > e2; j += step) idxs2.push(j);
    var idxs = idxs2;
  }
  var res = idxs.map(function (i3) { return seq[i3]; });
  return typeof seq === "string" ? res.join("") : res;
}
function __saho_type_of(v) { return __saho_type(v); }
function __saho_contains(container, v) {
  if (__saho_isList(container)) {
    for (var i = 0; i < container.length; i++) { if (__saho_eq(container[i], v)) return true; }
    return false;
  }
  if (__saho_isDict(container)) return container.has(__saho_key(v));
  if (typeof container === "string") {
    if (typeof v !== "string") throw { __saho: true, message: "在文本里查找时，要找的也得是文本。" };
    return container.indexOf(v) >= 0;
  }
  throw { __saho: true, message: "包含() 的第一个参数要 列表、字典 或 文本。" };
}
function __saho_sum(items) {
  if (!__saho_isList(items)) throw { __saho: true, message: "求和() 的参数要是列表。" };
  var total = 0;
  for (var i = 0; i < items.length; i++) {
    if (typeof items[i] !== "number") throw { __saho: true, message: "求和() 的列表里第 " + (i + 1) + " 项不是数（是" + TypeName2(items[i]) + "）。" };
    total = __saho_num(total + items[i]);
  }
  return total;
}
function __saho_maxmin(items, sign, fnName) {
  if (!__saho_isList(items)) throw { __saho: true, message: fnName + "() 的参数要是列表。" };
  if (items.length === 0) throw { __saho: true, message: "空列表没有最大值。an empty list has no maximum." };
  var allNum = true, allStr = true;
  for (var i = 0; i < items.length; i++) {
    if (typeof items[i] !== "number") allNum = false;
    if (typeof items[i] !== "string") allStr = false;
  }
  if (!allNum && !allStr) throw { __saho: true, message: "列表里的元素类型不一致，没法比大小。" };
  var best = items[0];
  for (var j = 1; j < items.length; j++) {
    var c = allNum ? (items[j] < best ? -1 : items[j] > best ? 1 : 0) : (items[j] < best ? -1 : items[j] > best ? 1 : 0);
    if (c * sign > 0) best = items[j];
  }
  return best;
}
function __saho_sorted(items, opts) {
  var rev = __saho_opt(opts, "降序", "reverse", false);
  if (!__saho_isList(items)) throw { __saho: true, message: "排序() 的参数要是列表。" };
  var allNum = true, allStr = true;
  for (var i = 0; i < items.length; i++) {
    if (typeof items[i] !== "number") allNum = false;
    if (typeof items[i] !== "string") allStr = false;
  }
  if (!allNum && !allStr) throw { __saho: true, message: "列表里的元素类型不一致，没法排序。" };
  var out = items.slice();
  out.sort(function (a, b) {
    if (a < b) return rev ? 1 : -1;
    if (a > b) return rev ? -1 : 1;
    return 0;
  });
  return out;
}
function __saho_reversed(v) {
  if (__saho_isList(v)) return v.slice().reverse();
  if (typeof v === "string") return v.split("").reverse().join("");
  throw { __saho: true, message: "反转() 只能用在 列表 或 文本 上。" };
}
function __saho_join(items, opts) {
  if (!__saho_isList(items)) throw { __saho: true, message: "连接() 的参数要是列表。" };
  var sep = __saho_opt(opts, "分隔", "sep", "");
  return items.map(function (it) { return __saho_str(it); }).join(sep);
}
function __saho_split(text, opts) {
  if (typeof text !== "string") throw { __saho: true, message: "分割() 的第一个参数要是文本。" };
  var sep = __saho_opt(opts, "分隔", "sep", " ");
  if (sep === "") throw { __saho: true, message: "分隔符不能是空文本。the separator cannot be an empty string." };
  return text.split(sep);
}
function __saho_replace(text, oldS, newS) {
  if (typeof text !== "string" || typeof oldS !== "string" || typeof newS !== "string") {
    throw { __saho: true, message: "替换() 的参数都要是文本。" };
  }
  return text.split(oldS).join(newS);
}
function __saho_trim(text) {
  if (typeof text !== "string") throw { __saho: true, message: "修剪() 的参数要是文本。" };
  return text.trim();
}
function __saho_upper(text) {
  if (typeof text !== "string") throw { __saho: true, message: "转大写() 的参数要是文本。" };
  return text.toUpperCase();
}
function __saho_lower(text) {
  if (typeof text !== "string") throw { __saho: true, message: "转小写() 的参数要是文本。" };
  return text.toLowerCase();
}
function __saho_abs(x) {
  if (typeof x !== "number") throw { __saho: true, message: "绝对值() 的参数要是一个数。" };
  return Math.abs(x);
}
function __saho_sqrt(x) {
  if (typeof x !== "number") throw { __saho: true, message: "平方根() 的参数要是一个数。" };
  if (x < 0) throw { __saho: true, message: "负数没有实数平方根。a negative number has no real square root." };
  return __saho_num(Math.sqrt(x));
}
function __saho_round(x, opts) {
  if (typeof x !== "number") throw { __saho: true, message: "四舍五入() 的第一个参数要是一个数。" };
  var digits = __saho_opt(opts, "小数位", "digits", 0);
  var scale = Math.pow(10, digits);
  var scaled = x * scale;
  var unit = Math.sign(scaled) * Math.floor(Math.abs(scaled) + 0.5);
  return __saho_num(unit / scale);
}
function __saho_unavailable(name) {
  throw {
    __saho: true,
    message: "浏览器里没有文件系统，" + name + " 用不了。",
    hint: "试试让服务端把内容发过来。this built-in is unavailable in the browser."
  };
}
function __saho_json_to(v) {
  return JSON.stringify(__saho_toJson(v), null, "");
}
function __saho_toJson(v, ls, ds) {
  ls = ls || []; ds = ds || [];
  if (v === null) return null;
  if (typeof v === "boolean") return v;
  if (typeof v === "number") return __saho_num(v);
  if (typeof v === "string") return v;
  if (__saho_isList(v)) {
    if (ls.indexOf(v) >= 0) throw { __saho: true, message: "值里有自己套自己的结构，转不成 JSON。" };
    ls.push(v);
    var out = v.map(function (it) { return __saho_toJson(it, ls, ds); });
    ls.pop();
    return out;
  }
  if (__saho_isDict(v)) {
    if (ds.indexOf(v) >= 0) throw { __saho: true, message: "值里有自己套自己的结构，转不成 JSON。" };
    ds.push(v);
    var obj = {};
    v.forEach(function (val, key) {
      obj[typeof key === "number" ? __saho_shortest(key) : key] = __saho_toJson(val, ls, ds);
    });
    ds.pop();
    return obj;
  }
  throw { __saho: true, message: (typeof v === "function" ? "函数" : "这个值") + "转不成 JSON。" };
}
function __saho_json_from(text) {
  if (typeof text !== "string") throw { __saho: true, message: "自文本() 的参数要是一段 JSON 文本。" };
  var v;
  try { v = JSON.parse(text); }
  catch (e) {
    throw { __saho: true, message: "这段 JSON 解析不了。this JSON cannot be parsed.", hint: "检查逗号、引号和花括号是否配对。" };
  }
  return __saho_fromJson(v);
}
function __saho_fromJson(v) {
  if (v === null) return null;
  if (Array.isArray(v)) return v.map(__saho_fromJson);
  if (typeof v === "object") {
    var d = new Map();
    for (var k in v) {
      if (Object.prototype.hasOwnProperty.call(v, k)) d.set(k, __saho_fromJson(v[k]));
    }
    return d;
  }
  return v; // number / string / boolean
}
// ---------- 页面/page 模块（只在浏览器宿主存在） ----------
function __saho_needDom() {
  if (typeof document === "undefined") {
    throw { __saho: true, message: "页面 模块只能在浏览器里用；服务端程序请用 网络 模块。" };
  }
}
function __saho_wrapEl(el) {
  if (el === null) return null;
  return el; // 直接持有原生元素；用标记区分
}
function __saho_markEl(el) {
  if (el !== null && typeof el === "object") {
    try { el.__saho_element = true; } catch (e) { /* 某些宿主对象不可扩展 */ }
  }
  return el;
}
var page = {
  "取元素": function (sel) { __saho_needDom(); return __saho_markEl(document.querySelector(sel)); },
  "取全部": function (sel) {
    __saho_needDom();
    var out = [];
    document.querySelectorAll(sel).forEach(function (el) { out.push(__saho_markEl(el)); });
    return out;
  },
  "置文本": function (el, text) { __saho_needDom(); el.textContent = __saho_str(text); return null; },
  "取文本": function (el) { __saho_needDom(); return el.textContent; },
  "值": function (el) { __saho_needDom(); return el.value !== undefined ? el.value : ""; },
  "置值": function (el, text) { __saho_needDom(); el.value = __saho_str(text); return null; },
  "置样式": function (el, name, value) { __saho_needDom(); el.style[name] = __saho_str(value); return null; },
  "置属性": function (el, name, value) { __saho_needDom(); el.setAttribute(name, __saho_str(value)); return null; },
  "点击": function (el, fn) {
    __saho_needDom();
    el.addEventListener("click", function (ev) {
      ev.preventDefault();
      var d = new Map(); d.set("类型", "点击"); d.set("目标", el);
      try { __saho_call(fn, [d], null); }
      catch (e) { if (typeof console !== "undefined") console.error(__saho_error_text(e)); }
    });
    return null;
  },
  "输入": function (el, fn) {
    __saho_needDom();
    el.addEventListener("input", function () {
      var d = new Map(); d.set("类型", "输入"); d.set("目标", el);
      try { __saho_call(fn, [d], null); }
      catch (e) { if (typeof console !== "undefined") console.error(__saho_error_text(e)); }
    });
    return null;
  },
  "提交": function (el, fn) {
    __saho_needDom();
    el.addEventListener("submit", function (ev) {
      ev.preventDefault();
      var d = new Map(); d.set("类型", "提交"); d.set("目标", el);
      try { __saho_call(fn, [d], null); }
      catch (e) { if (typeof console !== "undefined") console.error(__saho_error_text(e)); }
    });
    return null;
  },
  "创建": function (tag) { __saho_needDom(); return __saho_markEl(document.createElement(tag)); },
  "加入": function (parent, child) { __saho_needDom(); parent.appendChild(child); return null; },
  "移除": function (el) { __saho_needDom(); if (el.parentNode) el.parentNode.removeChild(el); return null; }
};
page["el"] = page["取元素"]; page["el_all"] = page["取全部"];
page["set_text"] = page["置文本"]; page["get_text"] = page["取文本"];
page["value"] = page["值"]; page["set_value"] = page["置值"];
page["set_style"] = page["置样式"]; page["set_attr"] = page["置属性"];
page["on_click"] = page["点击"]; page["on_input"] = page["输入"]; page["on_submit"] = page["提交"];
page["create"] = page["创建"]; page["append"] = page["加入"]; page["remove"] = page["移除"];
// ---------- 网络/net 模块（浏览器端：JSON 可用；服务与同步请求不可用） ----------
function __saho_no_server() {
  throw { __saho: true, message: "浏览器里开不了服务；服务端程序请用 网络 模块跑在 Node 或操作系统上。" };
}
function __saho_no_sync_request() {
  throw { __saho: true, message: "浏览器里不能同步发网络请求（页面会卡死）。试试表单提交，或等 v2 的异步能力。" };
}
var net = {
  "自文本": __saho_json_from, "json_from": __saho_json_from,
  "到文本": __saho_json_to, "json_to": __saho_json_to,
  "服务": __saho_no_server, "server": __saho_no_server,
  "请求": __saho_no_sync_request, "request": __saho_no_sync_request
};

function __saho_maxmin_p(items, opts) { return __saho_maxmin(items, 1, "最大值"); }
function __saho_min_p(items, opts) { return __saho_maxmin(items, -1, "最小值"); }
function __saho_input() { return __saho_unavailable("输入"); }
function __saho_unavailable_read_file() { return __saho_unavailable("读取文件"); }
function __saho_unavailable_write_file() { return __saho_unavailable("写入文件"); }
function __saho_unavailable_file_exists() { return __saho_unavailable("文件存在"); }

(function () {
  "use strict";
  let 数字;
  let 显示;
  let 按钮;
  let 刷新;
  let 点一下;
  数字 = new Map([[__saho_key(`值`), 0]]);
  显示 = page["取元素"](`#显示`);
  按钮 = page["取元素"](`#按钮`);
  刷新 = __saho_defn(function 刷新() {
    page["置文本"](显示, `你点了 ${__saho_str(__saho_index(数字, `值`))} 次`);
  }, [], "刷新");
  点一下 = __saho_defn(function 点一下(事件) {
    __saho_set(数字, `值`, __saho_add(__saho_index(数字, `值`), 1));
    刷新();
  }, ["事件"], "点一下");
  page["点击"](按钮, 点一下);
  刷新();
})();
