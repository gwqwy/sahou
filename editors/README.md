# editors —— 卅语言编辑器生态

> v0.2 起，编辑器支持按"**共享数据 + 各编辑器薄壳**"组织：
> 语言知识（关键字、内置函数、模块、stones 包）只有一份（`shared/language.json`），
> 各编辑器集成只做"壳"——把数据接成自己的补全/悬停/高亮，并调用 `sahou lsp` 拿实时诊断。
> 想给新编辑器做插件，不需要再从头整理语言资料。

## 目录

| 路径 | 内容 |
|---|---|
| `shared/language.json` | **编辑器元数据唯一事实来源**：语言基本信息、23 关键字、30 内置函数（带签名/说明/可用端）、11 模块、14 stones 包（带成员清单）。与 docs/00 简报、02 规范同源 |
| `sync-shared.js` | 把共享数据同步到各编辑器目录（`node editors/sync-shared.js`，零依赖）。改了 shared 以后必须跑一次并提交 |
| `vscode/sahou/` | VS Code 扩展（v0.2.0）：语法高亮、文件图标、补全、悬停、实时诊断、一键运行/转译/REPL。见 [VS_CODE接入指南.md](./VS_CODE接入指南.md) |
| `tools/genicon.py` | 市场图标生成器：把 `vscode/sahou/icons/saho.svg` 的流水线设计栅格化成 PNG（纯标准库）。改了图标后运行 `python editors/tools/genicon.py` |

## 已支持 / 路线图

| 编辑器 | 状态 | 说明 |
|---|---|---|
| VS Code | ✅ v0.2.0 | `editors/vscode/sahou`，vsix 随仓库发布 |
| 在线体验（浏览器） | ✅ | `examples/在线体验/` + 卅.wasm，任何现代浏览器可用 |
| JetBrains（IDEA 等） | 计划 | 语言插件可直接消费 `shared/language.json`；诊断走 `sahou lsp`（LSP 插件模板即可起步） |
| Sublime Text | 计划 | 语法定义可由 `shared/language.json` 生成 `.sublime-syntax`；补全走 `sahou lsp` |
| Vim / Neovim | 计划 | tree-sitter 或语法文件 + 内置 LSP 客户端（`sahou lsp` 已是标准 stdio LSP） |
| 其他（Zed、Helix…） | 欢迎贡献 | `sahou lsp` 是编辑器无关的标准 LSP 服务，接上即可 |

## 给新编辑器做集成的配方

1. **拿数据**：读 `shared/language.json`（或复制进你的插件目录，保持与 sync 脚本一致）。
   - `keywords`：关键字双语表（zh/en/kind/brief）
   - `builtins`：30 个内置函数（zh/en/signature/brief/side——`side` 标注服务端专属）
   - `modules`：标准库模块与常见成员
   - `stones`：14 个内嵌 stones 包与全部顶层成员
2. **接 LSP**：启动 `sahou lsp`（stdio、Content-Length 帧），它提供：
   - 全文诊断（`textDocument/publishDiagnostics`）
   - 上下文补全（`textDocument/completion`）：`用 "…` 给包名、`名字.` 给成员、其余给关键字+内置函数
3. **补壳**：语法高亮可直接参考 `vscode/sahou/syntaxes/sahou.tmLanguage.json`（词法规则见 docs/02 规范第 2、3 节）。
4. **登记**：在上面的路线图表加一行，并在 `sync-shared.js` 的 `targets` 里加上你的数据副本路径。

## 与语言本体的边界

- 编辑器**不自带**语言知识：一切以 `sahou` 工具链（`sahou lsp`）与 `shared/language.json` 为准，避免两边漂移。
- `sahou` exe 本身内嵌全部 stones 包与 卅.wasm；编辑器只需要能找到 `sahou` 可执行文件（VS Code 扩展里是 `sahou.path` 设置）。
