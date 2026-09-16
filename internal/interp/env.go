package interp

// Env 作用域（规范 5.3：只有函数作用域 + 顶层作用域，词法链）。
// 读取沿 parent 链向外找；赋值只在当前作用域创建或覆盖（遮蔽，不写外层）。
type Env struct {
	vars   map[string]Value
	parent *Env
}

func NewEnv(parent *Env) *Env {
	return &Env{vars: make(map[string]Value), parent: parent}
}

// Get 沿词法链查找；ok=false 表示整个链上都没有。
func (e *Env) Get(name string) (Value, bool) {
	for s := e; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// Set 只作用于当前作用域（规范 5.3 第 5 条：永不修改外层）。
func (e *Env) Set(name string, v Value) {
	e.vars[name] = v
}

// Names 汇总整条链上可见的名字（拼写建议用）。
func (e *Env) Names() []string {
	seen := map[string]bool{}
	var out []string
	for s := e; s != nil; s = s.parent {
		for k := range s.vars {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}
