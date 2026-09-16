// sahou（卅）VS Code 扩展主代码。
// 1) sahou.run：在终端运行当前 .saho 文件；
// 2) 内置极简 LSP 客户端：启动 `sahou lsp`，把语法诊断映射为编辑器波浪线。
// 无 npm 依赖，直接使用 vscode API 与 child_process。
const vscode = require("vscode");
const { spawn } = require("child_process");

let lspProcess = null;
let lspBuffer = null;
let diagnosticCollection = null;
let pendingDocs = null;

function sahouPath() {
  return vscode.workspace.getConfiguration("sahou").get("path", "sahou");
}

function diagnosticsEnabled() {
  return vscode.workspace.getConfiguration("sahou").get("diagnostics", true);
}

// ---------- 运行当前文件 ----------

function runCurrentFile() {
  const editor = vscode.window.activeTextEditor;
  if (!editor || editor.document.languageId !== "sahou") {
    vscode.window.showWarningMessage("请先打开一个 .saho 文件。");
    return;
  }
  editor.document.save();
  const terminal = vscode.window.createTerminal({ name: "sahou" });
  terminal.show();
  const exe = sahouPath();
  const file = editor.document.fileName;
  const cmd = buildRunCommand(exe, file);
  // 优先用 shell integration API：命令由 VS Code 直接执行，绕开 shell 解析差异
  if (terminal.shellIntegration && terminal.shellIntegration.executeCommand) {
    terminal.shellIntegration.executeCommand(cmd);
  } else {
    terminal.sendText(cmd);
  }
}

// 组装终端命令：PowerShell 里以引号开头的命令会被当成字符串，
// 所以含空格的路径要加 & 调用符；无空格路径不加引号（三种终端通吃）。
function buildRunCommand(exe, file) {
  const profile =
    vscode.workspace.getConfiguration("terminal.integrated").get("defaultProfile.windows") || "";
  const isPowerShell = /powershell|pwsh/i.test(profile) || profile === "";
  const needQuoteExe = exe.includes(" ");
  const exePart = needQuoteExe ? `"${exe}"` : exe;
  const prefix = isPowerShell && needQuoteExe ? "& " : "";
  return `${prefix}${exePart} run "${file}"`;
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
  pendingDocs = pendingDocs || new Map();
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
  }
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
  lspBuffer = lspBuffer.slice(headEnd + 4 + length);
  return body;
}

// ---------- 扩展入口 ----------

function activate(context) {
  registerLanguageFeatures(context);
  diagnosticCollection = vscode.languages.createDiagnosticCollection("sahou");
  context.subscriptions.push(diagnosticCollection);

  context.subscriptions.push(
    vscode.commands.registerCommand("sahou.run", runCurrentFile)
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

  startLsp(context);
}

function registerLanguageFeatures(context) {
  registerLanguageFeaturesImpl(context);
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
}

function deactivate() {
  stopLsp();
}

module.exports = { activate, deactivate };

// ---------- 补全与悬停（客户端元数据，无网络往返） ----------
const SAHOU_KEYWORDS = [
  ["如果", "条件：如果 条件 … 完毕"], ["又如", "否则再判断"], ["否则", "其余情况"],
  ["当", "循环：当 条件 … 完毕"], ["遍历", "循环：遍历 x 于 序列"], ["于", "遍历的连接词"],
  ["完毕", "结束一个块"], ["函数", "定义函数（或匿名 函数(x) => 表达式）"],
  ["返回", "从函数返回值"], ["设", "可选的赋值前缀"],
  ["真", "布尔真"], ["假", "布尔假"], ["空值", "没有值"],
  ["并且", "逻辑与（短路）"], ["或者", "逻辑或（短路）"], ["非", "逻辑非"],
  ["尝试", "尝试块开始"], ["接住", "接住错误信息"], ["跳出", "结束最近一层循环（v4.1）"],
  ["继续", "跳过本轮循环（v4.1）"], ["格子", "响应式变量（v4）"],
  ["用", "引入模块：用 \"模块名\" 引入"],
];
const SAHOU_KEYWORDS_EN = [
  ["if", "if condition … end"], ["elif", "else if"], ["else", "else"],
  ["while", "while loop"], ["for", "for loop"], ["in", "loop connector"],
  ["end", "close a block"], ["fn", "define function / anonymous fn"],
  ["return", "return from function"], ["let", "optional assignment prefix"],
  ["true", "true"], ["false", "false"], ["null", "null"],
  ["and", "logical and"], ["or", "logical or"], ["not", "logical not"],
  ["try", "try block"], ["catch", "catch error"], ["break", "break loop (v4.1)"],
  ["continue", "continue loop (v4.1)"], ["cell", "reactive cell (v4)"],
  ["use", "import module"], ["import", "import suffix"],
];
const SAHOU_BUILTINS = [
  ["打印", "print", "打印(值, 结尾: \"\n\")", "把值打印到屏幕，结尾可改"],
  ["输入", "input", "输入(提示: \"\")", "等用户输入一行，返回文本"],
  ["数", "num", "数(值)", "把文本/布尔转成数；失败抛错"],
  ["文本", "str", "文本(值)", "把任意值转成文本"],
  ["列表", "list", "列表(值?)", "空列表/浅拷贝/字典的键列表"],
  ["字典", "dict", "字典(配对列表?)", "空字典或由 [键,值] 对构造"],
  ["从a到b", "range_to", "从a到b(a, b, 步长: 1)", "生成含头不含尾的等差列表"],
  ["长度", "len", "长度(列表|文本|字典)", "元素数/字符数/键数"],
  ["取", "slice", "取(序列, 起: 0, 止, 步长: 1)", "取一段（半开区间），替代切片语法"],
  ["抛出", "throw", "抛出(消息文本)", "主动抛错，可被 接住"],
  ["类型", "type_of", "类型(值)", "返回 \"number\"/\"string\"/…"],
  ["包含", "contains", "包含(容器, 值)", "列表含元素/字典含键/文本含子串"],
  ["求和", "sum", "求和(列表)", "所有数相加"],
  ["最大值", "max", "最大值(列表)", "最大元素"],
  ["最小值", "min", "最小值(列表)", "最小元素"],
  ["排序", "sorted", "排序(列表, 降序: 假)", "返回新排序列表"],
  ["反转", "reversed", "反转(列表|文本)", "返回倒序副本"],
  ["连接", "join", "连接(列表, 分隔: \"\")", "元素转文本后连接"],
  ["分割", "split", "分割(文本, 分隔: \" \")", "按分隔符切成列表"],
  ["替换", "replace", "替换(文本, 旧, 新)", "全部替换，返回新文本"],
  ["修剪", "trim", "修剪(文本)", "去首尾空白"],
  ["转大写", "upper", "转大写(文本)", "字母转大写"],
  ["转小写", "lower", "转小写(文本)", "字母转小写"],
  ["绝对值", "abs", "绝对值(数)", "绝对值"],
  ["平方根", "sqrt", "平方根(数)", "平方根；负数报错"],
  ["四舍五入", "round", "四舍五入(数, 小数位: 0)", "按位四舍五入"],
  ["读取文件", "read_file", "读取文件(路径)", "UTF-8 读整个文件（服务端）"],
  ["写入文件", "write_file", "写入文件(路径, 内容)", "覆盖写入（服务端）"],
  ["文件存在", "file_exists", "文件存在(路径)", "路径是否存在（服务端）"],
  ["位置", "find", "位置(文本, 子文本)", "子文本首次出现的下标；找不到 -1"],
];
const SAHOU_MODULES = [
  ["网络", "net", "后端：服务.路由/监听、请求、自文本/到文本（JSON）"],
  ["页面", "page", "前端：取元素/置文本/点击/绑定/绑输入 等 DOM 能力"],
  ["随机", "random", "数/小数/挑/洗牌/种子"],
  ["时间", "time", "现在/文本/解析/戳/计时/耗时"],
  ["数学", "math", "圆周率/幂/对数/三角/取整/公约数"],
  ["编码", "encoding", "网址/六十四/十六进制 编解码"],
  ["系统", "sys", "参数/环境/平台/退出（仅服务端）"],
  ["数据库", "db", "嵌入式 SQL：打开/执行/查询/表/关闭（仅服务端）"],
  ["网页", "html", "服务端渲染：渲染/渲染文件/页面/转义"],
  ["应用", "app", "窗口/手机/原生窗口/写页面（纯代码图形页面）"],
  ["测试", "assert", "相等/为真/汇总/清零（单元测试）"],
];

function wordAt(doc, pos) {
  const range = doc.getWordRangeAtPosition(pos, /[\p{L}\p{Nd}_]+/u);
  return range ? doc.getText(range) : "";
}

function completionItems(prefix) {
  const items = [];
  const add = (label, kind, detail, doc) => items.push(
    new vscode.CompletionItem(label, kind, detail, doc));
  for (const [zh, en] of SAHOU_KEYWORDS) {
    if (zh.startsWith(prefix)) add(zh, vscode.CompletionItemKind.Keyword, "sahou 关键字（英文 " + en + "）");
    else if (en.startsWith(prefix)) add(en, vscode.CompletionItemKind.Keyword, "sahou keyword（中文 " + zh + "）");
  }
  for (const [zh, en, sig, desc] of SAHOU_BUILTINS) {
    if (zh.startsWith(prefix)) add(zh, vscode.CompletionItemKind.Function, sig + " —— " + desc);
    else if (en.startsWith(prefix)) add(en, vscode.CompletionItemKind.Function, sig + " —— " + desc);
  }
  for (const [zh, en, desc] of SAHOU_MODULES) {
    if (zh.startsWith(prefix)) add(zh, vscode.CompletionItemKind.Module, desc);
    else if (en.startsWith(prefix)) add(en, vscode.CompletionItemKind.Module, desc);
  }
  return items;
}

function hoverInfo(word) {
  for (const [zh, en, sig, desc] of SAHOU_BUILTINS) {
    if (word === zh || word === en) {
      return new vscode.Hover(new vscode.MarkdownString(
        "**sahou 内置函数** `" + sig + "`\n\n" + desc + "\n\n（英文写法：" + en + "）"));
    }
  }
  for (const [zh, en] of SAHOU_MODULES) {
    if (word === zh || word === en) {
      return new vscode.Hover(new vscode.MarkdownString("**sahou 标准库模块** " + desc));
    }
  }
  for (const [zh, en] of SAHOU_KEYWORDS) {
    if (word === zh) return new vscode.Hover(new vscode.MarkdownString("**sahou 关键字**（英文 " + en + "）"));
    if (word === en) return new vscode.Hover(new vscode.MarkdownString("**sahou keyword**（中文 " + zh + "）"));
  }
  return null;
}

function registerLanguageFeaturesImpl(context) {
  const selector = { language: "sahou" };
  context.subscriptions.push(vscode.languages.registerCompletionItemProvider(
    selector, {
      provideCompletionItems(doc, pos) {
        const word = wordAt(doc, pos);
        if (!word) return [];
        return completionItems(word);
      }
    }));
  context.subscriptions.push(vscode.languages.registerHoverProvider(
    selector, {
      provideHover(doc, pos) {
        const word = wordAt(doc, pos);
        return word ? hoverInfo(word) : null;
      }
    }));
}
