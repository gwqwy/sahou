// sahou（卅）VS Code 扩展主代码。
// 1) sahou.run / sahou.build / sahou.repl：在终端运行、转译、打开交互环境；
// 2) 内置极简 LSP 客户端：启动 `sahou lsp`，推送诊断；v0.2 起还转发补全请求，
//    让服务端的上下文补全（`用 "…` 给包名、`名字.` 给成员）真正到达编辑器；
// 3) 静态元数据来自 ./data/language.json（editors/shared/language.json 的同步副本），
//    用于补全兜底与悬停文档。无 npm 依赖，只用 vscode API 与 child_process。
const vscode = require("vscode");
const { spawn } = require("child_process");

// ---------- 共享语言数据（editors/shared/language.json 的副本） ----------

let LANG = null;
try {
  LANG = require("./data/language.json");
} catch (err) {
  LANG = { keywords: [], builtins: [], modules: [], stones: [] };
}

let lspProcess = null;
let lspBuffer = null;
let diagnosticCollection = null;
let lspRequestSeq = 10;
const pendingCompletions = new Map(); // id -> { resolve, timer }

function sahouPath() {
  return vscode.workspace.getConfiguration("sahou").get("path", "sahou");
}

function diagnosticsEnabled() {
  return vscode.workspace.getConfiguration("sahou").get("diagnostics", true);
}

function completionEnabled() {
  return vscode.workspace.getConfiguration("sahou").get("completion", true);
}

// ---------- 运行 / 转译 / REPL ----------

function ensureSahouEditor() {
  const editor = vscode.window.activeTextEditor;
  if (!editor || editor.document.languageId !== "sahou") {
    vscode.window.showWarningMessage("请先打开一个 .saho 文件。");
    return null;
  }
  return editor;
}

function runInTerminal(name, cmd) {
  const terminal = vscode.window.createTerminal({ name: "sahou" });
  terminal.show();
  // 优先用 shell integration API：命令由 VS Code 直接执行，绕开 shell 解析差异
  if (terminal.shellIntegration && terminal.shellIntegration.executeCommand) {
    terminal.shellIntegration.executeCommand(cmd);
  } else {
    terminal.sendText(cmd);
  }
  return terminal;
}

// 组装终端命令：PowerShell 里以引号开头的命令会被当成字符串，
// 所以含空格的路径要加 & 调用符；无空格路径不加引号（三种终端通吃）。
function quoteExe(exe) {
  const needQuote = exe.includes(" ");
  const profile =
    vscode.workspace.getConfiguration("terminal.integrated").get("defaultProfile.windows") || "";
  const isPowerShell = /powershell|pwsh/i.test(profile) || profile === "";
  const part = needQuote ? `"${exe}"` : exe;
  return isPowerShell && needQuote ? "& " + part : part;
}

function runCurrentFile() {
  const editor = ensureSahouEditor();
  if (!editor) return;
  editor.document.save();
  const cmd = `${quoteExe(sahouPath())} run "${editor.document.fileName}"`;
  runInTerminal("sahou", cmd);
}

function buildCurrentFile() {
  const editor = ensureSahouEditor();
  if (!editor) return;
  editor.document.save();
  const file = editor.document.fileName;
  const out = file.replace(/\.saho$/i, "") + ".js";
  const cmd = `${quoteExe(sahouPath())} build "${file}" -o "${out}"`;
  runInTerminal("sahou 转译", cmd);
}

function openRepl() {
  const cmd = quoteExe(sahouPath());
  runInTerminal("sahou", cmd);
}

// ---------- 静态补全与悬停（编辑器端元数据，无网络往返） ----------

function wordAt(doc, pos) {
  const range = doc.getWordRangeAtPosition(pos, /[\p{L}\p{Nd}_]+/u);
  return range ? doc.getText(range) : "";
}

function staticCompletionItems(prefix) {
  const items = [];
  const add = (label, kind, detail, doc) => {
    const item = new vscode.CompletionItem(label, kind);
    item.detail = detail;
    if (doc) item.documentation = doc;
    items.push(item);
  };
  const md = (text) => new vscode.MarkdownString(text);
  for (const k of LANG.keywords || []) {
    if (k.zh.startsWith(prefix)) {
      add(k.zh, vscode.CompletionItemKind.Keyword, "sahou 关键字（英文 " + k.en + "）", md(k.brief));
    } else if (k.en.startsWith(prefix)) {
      add(k.en, vscode.CompletionItemKind.Keyword, "sahou keyword（中文 " + k.zh + "）", md(k.brief));
    }
  }
  for (const b of LANG.builtins || []) {
    if (b.zh.startsWith(prefix)) {
      add(b.zh, vscode.CompletionItemKind.Function, b.signature, md("**" + b.signature + "**\n\n" + b.brief + "\n\n（英文 " + b.en + "，" + b.side + "）"));
    } else if (b.en.startsWith(prefix)) {
      add(b.en, vscode.CompletionItemKind.Function, b.signature, md("**" + b.signature + "**\n\n" + b.brief + "\n\n（中文 " + b.zh + "，" + b.side + "）"));
    }
  }
  for (const m of LANG.modules || []) {
    if (m.zh.startsWith(prefix)) {
      add(m.zh, vscode.CompletionItemKind.Module, m.brief);
    } else if (m.en.startsWith(prefix)) {
      add(m.en, vscode.CompletionItemKind.Module, m.brief);
    }
  }
  for (const s of LANG.stones || []) {
    if (s.name.startsWith(prefix)) {
      add(s.name, vscode.CompletionItemKind.Module, "stones 包：" + s.brief);
    }
  }
  // 管道占位符：值 -> 步骤 里用 它 指代流经的值
  if ("它".startsWith(prefix)) {
    add("它", vscode.CompletionItemKind.Variable, "管道占位符：值 -> 它 * 2 -> 打印");
  }
  return items;
}

function hoverInfo(word) {
  const md = (text) => new vscode.Hover(new vscode.MarkdownString(text));
  for (const b of LANG.builtins || []) {
    if (word === b.zh || word === b.en) {
      return md("**sahou 内置函数** `" + b.signature + "`\n\n" + b.brief + "\n\n（英文写法：" + b.en + "；" + b.side + "可用）");
    }
  }
  for (const m of LANG.modules || []) {
    if (word === m.zh || word === m.en) {
      const members = (m.members || []).join("、") || "成员在输入 . 后自动补全";
      return md("**sahou 标准库模块** " + m.brief + "\n\n常见成员：" + members);
    }
    if ((m.members || []).includes(word)) {
      return md("**" + m.zh + "/" + m.en + "** 的成员：" + word);
    }
  }
  for (const s of LANG.stones || []) {
    if (word === s.name) {
      return md("**sahou stones 标准库包** " + s.brief + "\n\n用 `用 \"" + s.name + "\" 引入` 后以 " + s.name + ".成员 使用。\n\n顶层成员：" + (s.members || []).join("、"));
    }
    if ((s.members || []).includes(word)) {
      return md("**stones 包 " + s.name + "** 的成员：" + word + "\n\n" + s.brief);
    }
  }
  for (const k of LANG.keywords || []) {
    if (word === k.zh) return md("**sahou 关键字**（英文 " + k.en + "）\n\n" + k.brief);
    if (word === k.en) return md("**sahou keyword**（中文 " + k.zh + "）\n\n" + k.brief);
  }
  if (word === "它") {
    return md("**管道占位符** `它`\n\n在 `值 -> 步骤 -> 步骤` 里指代流经当前步骤的值，例如 `成绩 -> 它 >= 60 -> 打印`。全角 `→` 与 `->` 等价。");
  }
  return null;
}

// ---------- 极简 LSP 客户端 ----------

function frame(body) {
  const data = Buffer.from(body, "utf8");
  return Buffer.concat([
    Buffer.from(`Content-Length: ${data.length}\r\n\r\n`, "ascii"),
    data,
  ]);
}

function send(method, params) {
  if (!lspProcess) return;
  lspProcess.stdin.write(frame(JSON.stringify({ jsonrpc: "2.0", method, params })));
}

function sendRequest(id, method, params) {
  if (!lspProcess) return;
  lspProcess.stdin.write(frame(JSON.stringify({ jsonrpc: "2.0", id, method, params })));
}

function startLsp(context) {
  if (!diagnosticsEnabled()) return;
  const exe = sahouPath();
  try {
    lspProcess = spawn(exe, ["lsp"], { shell: false });
  } catch (err) {
    return; // sahou 不可用时静默降级：只有高亮没有诊断
  }
  lspBuffer = Buffer.alloc(0);

  lspProcess.stdout.on("data", (chunk) => {
    lspBuffer = Buffer.concat([lspBuffer, chunk]);
    let msg;
    while ((msg = readFrame())) {
      handleLspMessage(msg);
    }
  });

  lspProcess.on("error", () => {
    lspProcess = null; // 无法启动（比如路径没配好）：静默降级
  });

  sendRequest(1, "initialize", {
    processId: process.pid,
    rootUri: vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders[0]
      ? vscode.workspace.workspaceFolders[0].uri.toString()
      : null,
    capabilities: {},
  });
  send("initialized", {});

  // 把已打开的 sahou 文件推给服务
  for (const doc of vscode.workspace.textDocuments) {
    if (doc.languageId === "sahou") {
      sendOpen(doc);
    }
  }
}

function sendOpen(doc) {
  send("textDocument/didOpen", {
    textDocument: {
      uri: doc.uri.toString(),
      languageId: "sahou",
      version: doc.version,
      text: doc.getText(),
    },
  });
}

function handleLspMessage(raw) {
  let msg;
  try {
    msg = JSON.parse(raw.toString("utf8"));
  } catch (err) {
    return;
  }
  if (msg.method === "textDocument/publishDiagnostics") {
    const uri = vscode.Uri.parse(msg.params.uri);
    const diags = (msg.params.diagnostics || []).map((d) => {
      const start = new vscode.Position(
        Math.max(0, d.range.start.line),
        Math.max(0, d.range.start.character)
      );
      const end = new vscode.Position(
        Math.max(0, d.range.end.line),
        Math.max(0, Math.min(d.range.end.character, 10000))
      );
      const diag = new vscode.Diagnostic(
        new vscode.Range(start, end),
        d.message,
        d.severity === 1 ? vscode.DiagnosticSeverity.Error : vscode.DiagnosticSeverity.Warning
      );
      diag.source = "sahou";
      return diag;
    });
    diagnosticCollection.set(uri, diags);
    return;
  }
  // 补全响应：按 id 交给等待中的 provider
  if (msg.id !== undefined && pendingCompletions.has(Number(msg.id))) {
    const pending = pendingCompletions.get(Number(msg.id));
    clearTimeout(pending.timer);
    pendingCompletions.delete(Number(msg.id));
    pending.resolve(msg.result && Array.isArray(msg.result.items) ? msg.result.items : []);
  }
}

// 向服务端要上下文补全；500ms 内没回话（或服务没起来）就退回静态数据。
function serverCompletion(doc, pos) {
  return new Promise((resolve) => {
    if (!lspProcess) {
      resolve(null);
      return;
    }
    const id = lspRequestSeq++;
    const timer = setTimeout(() => {
      pendingCompletions.delete(id);
      resolve(null);
    }, 500);
    pendingCompletions.set(id, { resolve, timer });
    sendRequest(id, "textDocument/completion", {
      textDocument: { uri: doc.uri.toString() },
      position: { line: pos.line, character: pos.character },
    });
  });
}

const LSP_KIND_MAP = {
  3: vscode.CompletionItemKind.Function,
  6: vscode.CompletionItemKind.Variable,
  9: vscode.CompletionItemKind.Module,
  14: vscode.CompletionItemKind.Keyword,
  21: vscode.CompletionItemKind.Field,
};

function toCompletionItems(serverItems) {
  return serverItems.map((s) => {
    const item = new vscode.CompletionItem(String(s.label || s.insertText || ""), LSP_KIND_MAP[s.kind] || vscode.CompletionItemKind.Text);
    if (s.sortText) item.sortText = s.sortText;
    if (s.detail) item.detail = s.detail;
    return item;
  });
}

// Content-Length 帧解析
function readFrame() {
  const headEnd = lspBuffer.indexOf("\r\n\r\n", 0, "ascii");
  if (headEnd < 0) return null;
  const header = lspBuffer.slice(0, headEnd).toString("ascii");
  const match = /content-length:\s*(\d+)/i.exec(header);
  if (!match) return null;
  const length = Number(match[1]);
  if (lspBuffer.length < headEnd + 4 + length) return null;
  const body = lspBuffer.slice(headEnd + 4, headEnd + 4 + length);
  lspBuffer = lspBuffer.slice(headEnd + 4, headEnd + 4 + length);
  return body;
}

// ---------- 扩展入口 ----------

function activate(context) {
  diagnosticCollection = vscode.languages.createDiagnosticCollection("sahou");
  context.subscriptions.push(diagnosticCollection);

  context.subscriptions.push(
    vscode.commands.registerCommand("sahou.run", runCurrentFile)
  );
  context.subscriptions.push(
    vscode.commands.registerCommand("sahou.build", buildCurrentFile)
  );
  context.subscriptions.push(
    vscode.commands.registerCommand("sahou.repl", openRepl)
  );
  context.subscriptions.push(
    vscode.commands.registerCommand("sahou.restartLsp", () => {
      stopLsp();
      startLsp(context);
      vscode.window.showInformationMessage("sahou 语言服务已重启。");
    })
  );

  // 打开/修改/保存 .saho → 推送全文给语言服务
  const onChange = (doc) => {
    if (doc.languageId !== "sahou" || !lspProcess) return;
    send("textDocument/didChange", {
      textDocument: {
        uri: doc.uri.toString(),
        version: doc.version,
      },
      contentChanges: [{ text: doc.getText() }],
    });
  };
  context.subscriptions.push(
    vscode.workspace.onDidOpenTextDocument(sendOpen)
  );
  context.subscriptions.push(
    vscode.workspace.onDidChangeTextDocument((event) => onChange(event.document))
  );
  context.subscriptions.push(
    vscode.workspace.onDidCloseTextDocument((doc) => {
      if (doc.languageId !== "sahou") return;
      if (lspProcess) {
        send("textDocument/didClose", {
          textDocument: { uri: doc.uri.toString() },
        });
      }
      diagnosticCollection.delete(doc.uri);
    })
  );

  // 配置变更 → 重启语言服务
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration("sahou")) {
        stopLsp();
        startLsp(context);
      }
    })
  );

  registerLanguageFeatures(context);
  startLsp(context);
}

function registerLanguageFeatures(context) {
  if (!completionEnabled()) return;
  const selector = { language: "sahou" };
  context.subscriptions.push(
    vscode.languages.registerCompletionItemProvider(
      selector,
      {
        async provideCompletionItems(doc, pos) {
          // 服务端补全（上下文感知：包名/成员）优先，静态数据兜底
          const serverItems = await serverCompletion(doc, pos);
          if (serverItems && serverItems.length) {
            return toCompletionItems(serverItems);
          }
          return staticCompletionItems(wordAt(doc, pos));
        }
      },
      ".", "\"", " " // 触发符：成员点号、用 " 引导、空格（命名实参提示）
    )
  );
  context.subscriptions.push(
    vscode.languages.registerHoverProvider(selector, {
      provideHover(doc, pos) {
        const word = wordAt(doc, pos);
        return word ? hoverInfo(word) : null;
      }
    })
  );
}

function stopLsp() {
  if (lspProcess) {
    try {
      send("exit", null);
      lspProcess.kill();
    } catch (err) {
      // 已退出则忽略
    }
    lspProcess = null;
  }
  for (const [, pending] of pendingCompletions) {
    clearTimeout(pending.timer);
    pending.resolve(null);
  }
  pendingCompletions.clear();
}

function deactivate() {
  stopLsp();
}

module.exports = { activate, deactivate };
