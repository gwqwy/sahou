// 解释器基准测试：go test ./internal/interp -bench .
// 覆盖三条热路径：循环算术、字符串拼接、字典读写。
package interp

import (
	"testing"

	"sahou/internal/lexer"
	"sahou/internal/parser"
)

func runBench(b *testing.B, src string) {
	b.Helper()
	b.StopTimer()
	toks, e := lexer.Tokenize(src)
	if e != nil {
		b.Fatal(e.Zh)
	}
	prog, e := parser.Parse(toks)
	if e != nil {
		b.Fatal(e.Zh)
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		in := New()
		if e := in.Run(prog); e != nil {
			b.Fatal(e.Zh)
		}
	}
}

func BenchmarkLoopArith(b *testing.B) {
	runBench(b, `
s = 0
for i 于 从a到b(1, 2000)
    s = s + i * 2
完毕
print(s)`)
}

func BenchmarkStringBuild(b *testing.B) {
	runBench(b, `
s = ""
for i 于 从a到b(1, 500)
    s = s + "x"
完毕
print(长度(s))`)
}

func BenchmarkDictIO(b *testing.B) {
	runBench(b, `
d = {}
for i 于 从a到b(1, 1000)
    d[文本(i)] = i
完毕
print(长度(d))`)
}
