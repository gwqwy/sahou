// v1.8 测试模块：内置模块 测试/assert —— 给用户写 sahou 程序的单元测试用。
//
//	测试.相等(加(1, 2), 3, "一加二")
//	测试.为真(长度([1, 2]) == 2)
//	测试.汇总()   # 打印 通过 X / Y，全部通过返回空值，否则返回失败数
package interp

import (
	"fmt"
	"sync"

	"math/big"

	"sahou/internal/errs"
)

var (
	testMu    sync.Mutex
	testPass  int
	testFail  int
	testNames []string // 失败用例名，供汇总列出
)

func testModule() *Dict {
	m := newMod("测试", "assert")
	m.fn("相等", "equal", [][]string{{"实际!"}, {"期望!"}, {"名"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		eq, e := DeepEqual(args[0], args[1], map[[2]interface{}]bool{})
		if e != nil {
			return nil, e
		}
		recordResult(eq, nameOf(args, 2, line), "期望 "+Str(args[1])+"，实际 "+Str(args[0]))
		return nil, nil
	})
	m.fn("为真", "is_true", [][]string{{"值!"}, {"名"}}, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		recordResult(Truthy(args[0]), nameOf(args, 1, line), "值是假")
		return nil, nil
	})
	m.fn("汇总", "summary", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		testMu.Lock()
		pass, fail := testPass, testFail
		names := append([]string{}, testNames...)
		testMu.Unlock()
		fmt.Printf("测试汇总：通过 %d / %d\n", pass, pass+fail)
		for _, n := range names {
			fmt.Println("  失败：" + n)
		}
		if fail == 0 {
			return nil, nil
		}
		return big.NewRat(int64(fail), 1), nil
	})
	m.fn("清零", "reset", nil, func(in *Interp, args []Value, line int) (Value, *errs.Error) {
		testMu.Lock()
		testPass, testFail, testNames = 0, 0, nil
		testMu.Unlock()
		return nil, nil
	})
	return m.d
}

func nameOf(args []Value, i int, line int) string {
	if i < len(args) {
		if n, isText := args[i].(string); isText && n != "" {
			return n
		}
	}
	return "第 " + fmt.Sprint(line) + " 行"
}

func recordResult(ok bool, name, why string) {
	testMu.Lock()
	defer testMu.Unlock()
	if ok {
		testPass++
		fmt.Println("√ " + name)
	} else {
		testFail++
		testNames = append(testNames, name+"（"+why+"）")
		fmt.Println("× " + name + "（" + why + "）")
	}
}

func registerTestModule(in *Interp) {
	m := testModule()
	in.Globals.Set("测试", m)
	in.Globals.Set("assert", m)
	in.builtinMods["测试"] = m
	in.builtinMods["assert"] = m
}
