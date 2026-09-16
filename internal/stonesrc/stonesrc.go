// Package stonesrc 是"exe 自带的标准库包"的唯一存放处：
// 命令行入口（main.go / cmd/sahouwasm）在编译期把 stones/ 目录 embed 进来注入 FS，
// 解释器（loadModule 兜底）、转译器（模块打包兜底）、CLI 子命令（stones/装）都从这里读。
// 没有注入时（比如第三方直接 import interp），所有函数安全返回"没有"。
package stonesrc

import (
	"io/fs"
	"sort"
	"strings"
)

// FS 内嵌的 stones 目录（键如 "统计库/main.saho"）。nil 表示没有内嵌库。
var FS fs.FS

// Has 内嵌库里有没有这个包（单文件 名.saho 或目录包 名/main.saho 都算）。
func Has(name string) bool {
	if FS == nil {
		return false
	}
	for _, cand := range []string{name + ".saho", name + "/main.saho"} {
		if _, err := fs.Stat(FS, cand); err == nil {
			return true
		}
	}
	return false
}

// Read 读内嵌目录包里的一个文件（目前目录包入口固定 main.saho）。
func Read(name, rel string) (string, bool) {
	if FS == nil {
		return "", false
	}
	data, err := fs.ReadFile(FS, name+"/"+rel)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Source 读包入口源码：目录包读 main.saho，单文件包读 名.saho。
func Source(name string) (string, bool) {
	if FS == nil {
		return "", false
	}
	if s, ok := Read(name, "main.saho"); ok {
		return s, true
	}
	if FS == nil {
		return "", false
	}
	data, err := fs.ReadFile(FS, name+".saho")
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Names 列出内嵌 stones 包名（排序后返回）。
func Names() []string {
	var out []string
	if FS == nil {
		return out
	}
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".saho") {
			out = append(out, strings.TrimSuffix(name, ".saho"))
			continue
		}
		if e.IsDir() {
			if _, err := fs.Stat(FS, name+"/main.saho"); err == nil {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}
