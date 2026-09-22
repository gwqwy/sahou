<div align="center">
  <img src="assets/logo.svg" width="96" alt="卅 语言标志">
  <h1>卅 <sub style="font-size:0.5em">读音 sa</sub></h1>
  <p>比 Python 更简单一点的中文入门编程语言 —— 一种语言写前后端、桌面与手机应用</p>
</div>

**卅**是一门为初学者设计的编程语言：中文/英文双语关键字、
精确有理数（`0.1 + 0.2` 永远精确）、报错说人话还会提示"你是不是想写 XXX"。
同一门语言可以写命令行程序、网站后端、浏览器页面、桌面窗口应用和手机端页面。

- 源文件后缀 **`.saho`**，解释器/工具链用 **Go** 实现，标准库功能零第三方依赖。
- **23 个双语关键字**（`函数/func`、`如果/if`…，中英可混用）、**30 个内置函数**、
  **11 个标准库模块**（网络/页面/随机/时间/数学/编码/系统/网页/数据库/应用/测试）。

## 一分钟上手

```saho
# 打印九九乘法表
函数 乘法表()
    for i 于 从a到b(1, 10)
        for j 于 从a到b(1, i + 1)
            print("{j}×{i}={i * j}", 结尾: "  ")
        完毕
        print("")
    完毕
完毕

乘法表()
```

```bash
go build -o sahou.exe .          # 构建（需 Go 1.23+）
./sahou.exe run 乘法表.saho      # 运行
./sahou.exe                      # 交互环境（REPL）
```

## 它能做什么

| 方向 | 写法 | 一行示例 |
|---|---|---|
| 命令行程序 | 语言核心 + 管道/格子 | `名单 → 取(?, 0, 3) → 排序(?)` |
| 网站后端 | `网络` + `数据库` + `网页` 模块 | `服务.路由("GET", "/文章/:编号", 查看)` |
| 浏览器页面 | `sahou build` 转译 JS，或 sahou.wasm 直接运行 | `页面.取元素("按钮")` |
| 桌面应用 | `应用.窗口` / `应用.原生窗口`（WebView2） | `应用.原生窗口("应用页面", "我的应用")` |
| 手机端 | `应用.手机`（局域网 + 添加到主屏幕） | `应用.手机("应用页面")` |
| 独立分发 | `sahou 打包` 生成单文件 exe | `sahou 打包 应用.saho -o 应用.exe` |

数据库是**纯 Go 内置的嵌入式 SQL**（不装 SQLite）：自增列、聚合函数、原子落盘，
关键词中英双语可混写：

```saho
库 = 数据库.打开("数据.db")
库.执行("建 表 如无 订单 (编号 自增, 客户, 金额)")
库.执行("插入 订单 (客户, 金额) 值 (?, ?)", "小明", 10)
统 = 库.查询("选择 数(*), 平均(金额) 出 订单")
```

## 仓库结构

| 路径 | 内容 |
|---|---|
| [docs/](docs/README.md) | 全套中文文档（设计决策、语言规范、教程、模块指南、用户手册） |
| `internal/` | 解释器：词法 → 语法 → 树遍历求值 + 各内置模块 |
| `cmd/sahouwasm/` | 浏览器直跑版（sahou.wasm） |
| `internal/transpile/` | sahou → JavaScript 转译器 |
| `internal/lsp/` `internal/format/` | 语言服务（诊断+补全）与格式化 |
| `examples/` | 可直接运行的示例（入门、留言板、桌面/手机/原生窗口应用） |
| `stones/` | 纯 sahou 写的 18 个标准库包（算术/文本/游戏/中文数字/…/时长库） |
| `tests/` | bash 回归套件（核心一致性、网络、全栈、应用等）+ Go 单元测试与基准 |

## 构建与测试

```bash
go build -o sahou.exe .        # 构建（唯一第三方依赖：go-webview2，纯 Go，无 CGO，仅 Windows 原生窗口用到）
bash tests/run.sh              # 核心一致性测试（24/24）
bash tests/fullstack.sh        # 全栈回归（数据库/网页/表单/会话）
bash tests/app.sh              # 应用回归（路径参数/桌面/手机端）
go test ./internal/interp      # Go 单元测试 + 基准（-bench .）
GOOS=js GOARCH=wasm go build -o sahou.wasm ./cmd/sahouwasm   # 浏览器直跑版
```

测试脚本为 bash（Windows 下用 Git Bash 运行）。

## 文档

按阅读顺序：[00-设计决策简报](docs/00-设计决策简报.md) →
[02-语言规范](docs/02-语言规范.md) →
[03-入门教程](docs/03-入门教程.md) →
[12-管道编程](docs/12-管道编程.md) / [13-响应式计算模型](docs/13-响应式计算模型.md) →
[14-全栈开发](docs/14-全栈开发.md) / [15-应用开发](docs/15-应用开发.md)。
完整清单见 [docs/README.md](docs/README.md)。

## 版本

当前版本 **v0.3.0**（`sahou version` 查看）；图标在 [assets/](assets/)（印章红「卅」字形，纯 SVG 路径，无字体依赖）。里程碑路线（v1 核心 → 全栈 → 应用 → 工程化）
见 [docs/README.md](docs/README.md) 的版本路线表。

## 许可证

[MIT](LICENSE)。
