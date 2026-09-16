// 数据库模块单元测试：自增列、聚合、原子落盘重开。
package interp

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func runDB(t *testing.T, src string) string {
	t.Helper()
	return src
}

func TestDBAutoIncAggPersist(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	dbv, e := openDatabase(dbPath, 0)
	if e != nil {
		t.Fatal(e.Zh)
	}
	db := dbv.(*Database)
	exec := func(sql string, params ...Value) Value {
		v, e := dbExec(nil, db, append([]Value{sql}, params...), nil, 0)
		if e != nil {
			t.Fatalf("%s: %s", sql, e.Zh)
		}
		return v
	}
	exec("建 表 如无 订单 (编号 自增, 客户, 金额)")
	exec("插入 订单 (客户, 金额) 值 (?, ?)", "甲", big.NewRat(10, 1))
	exec("插入 订单 (客户, 金额) 值 (?, ?)", "乙", big.NewRat(20, 1))
	// 自增：1、2
	q := func(sql string, params ...Value) [][]Value {
		st, e := parseSQL(sql, 0)
		if e != nil {
			t.Fatalf("%s: %s", sql, e.Zh)
		}
		rows, e := st.selectRows(db, &paramBinder{params: params})
		if e != nil {
			t.Fatalf("%s: %s", sql, e.Zh)
		}
		return rows
	}
	rows := q("选择 * 出 订单 哪里 客户 = ?", "乙")
	if got := rows[0][0].(interface{}); got == nil {
		t.Fatal("自增列没填号")
	}
	// 聚合
	agg := q("选择 数(*), 和(金额), 平均(金额) 出 订单")
	if Str(agg[0][0]) != "2" || Str(agg[0][1]) != "30" || Str(agg[0][2]) != "15" {
		t.Fatalf("聚合结果不对: %v", agg)
	}
	// 空值匹配
	exec("插入 订单 (客户) 值 (?)", "丙")
	agg = q("选择 数(*) 出 订单 哪里 金额 = ?", nil)
	if Str(agg[0][0]) != "1" {
		t.Fatalf("空值等值匹配失败: %v", agg)
	}
	// 原子落盘重开：自增信息保留
	dbv2, _ := openDatabase(dbPath, 0)
	db2 := dbv2.(*Database)
	exec2 := func(sql string, params ...Value) {
		_, e := dbExec(nil, db2, append([]Value{sql}, params...), nil, 0)
		if e != nil {
			t.Fatalf("%s: %s", sql, e.Zh)
		}
	}
	exec2("插入 订单 (客户, 金额) 值 (?, ?)", "丁", big.NewRat(1, 1))
	rows = func() [][]Value {
		st, _ := parseSQL("选择 * 出 订单 哪里 客户 = ?", 0)
		r, e := st.selectRows(db2, &paramBinder{params: []Value{"丁"}})
		if e != nil {
			t.Fatal(e.Zh)
		}
		return r
	}()
	if Str(rows[0][0]) != "4" {
		t.Fatalf("重开后自增没接上: %v", rows)
	}
	_ = os.Remove(dbPath)
}
