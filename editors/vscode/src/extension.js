// mzs language support for VS Code.
//
// Plain CommonJS with no dependencies and no build step: drop the folder into
// ~/.vscode/extensions and it runs. Everything the extension knows about the stdlib
// comes from data/api.json, which is generated from the live method registry plus
// SPEC.md §12 — see editors/vscode/README.md.

const vscode = require('vscode');
const cp = require('child_process');
const path = require('path');
const fs = require('fs');

const { Client } = require('./lsp');

const API = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'data', 'api.json'), 'utf8'));

// The keyword list is generated from the lexer's own table (SPEC §3.5), so it cannot
// drift into the Ruby words mzs deliberately leaves out.
const KEYWORDS = API.keywords || [];

// `it` is not a keyword (§3.4): it is the parameter a closure with no parameter list
// binds, and it is worth completing all the same.
const SPECIAL_VARS = ['it'];

// Receiver inference: what a literal immediately left of the dot evaluates to. A `]`
// closes either an array or a dict, so both tables are offered; a `}` closes a closure,
// because in mzs `{ … }` is always one (D2). Anything unrecognised falls back to the
// union of every kind, which is a longer list but never a wrong one.
const RECEIVER_PATTERNS = [
  [/"(?:[^"\\]|\\.)*"\s*$/, ['string']],
  [/'(?:[^'\\]|\\.)*'\s*$/, ['string']],
  [/\]\s*$/, ['array', 'dict']],
  [/\}\s*$/, ['fn']],
  [/\/[imxsu]*\s*$/, ['regex']],
  [/\b\d+\.\d+\s*$/, ['float']],
  [/\b\d+\s*$/, ['int']],
  [/\)\s*$/, null],
];

// What a module needs from the host, as the CLI spells it: `{time: '--time'}`.
const GATES = API.gates || {};

function cfg() {
  return vscode.workspace.getConfiguration('mzs');
}

function binary() {
  return cfg().get('path') || 'mzs';
}

/** Documentation markdown for one API row, or null when the spec has no entry. */
function docFor(name, doc, receiver) {
  const md = new vscode.MarkdownString();
  md.supportHtml = false;
  const sig = doc && doc.sig ? doc.sig : '';
  const head = receiver ? `${receiver}.${name}` : name;
  md.appendCodeblock(sig ? `${head} ${sig}` : head, 'mzs');
  if (doc && doc.sem) md.appendMarkdown('\n' + doc.sem + '\n');
  if (doc && doc.ex) {
    md.appendMarkdown('\n**Example**\n');
    md.appendCodeblock(doc.ex, 'mzs');
  }
  if (!doc || (!doc.sem && !doc.sig)) {
    md.appendMarkdown('\n_The method exists in the implementation, but SPEC.md §12 does not describe it._');
  }
  return md;
}

/** Every kind that registers `name`, so hover works without knowing the receiver. */
function kindsWith(name) {
  const out = [];
  for (const [kind, table] of Object.entries(API.methods)) {
    if (Object.prototype.hasOwnProperty.call(table, name)) out.push(kind);
  }
  return out;
}

function inferReceiver(before) {
  for (const [re, kinds] of RECEIVER_PATTERNS) if (re.test(before)) return kinds;
  return null;
}

// --- modules ----------------------------------------------------------------------

// includesIn reads the `include` statements of a document (§12.8): the name each one
// binds, and the path of a script module. Nothing is ambient any more, so this is what
// decides which module names the file can even use.
function includesIn(text) {
  const out = new Map();
  const re = /^[ \t]*include[ \t]+([A-Za-z_\u0400-\u04FF][\w\u0400-\u04FF]*)(?:[ \t]+from[ \t]+["']([^"']+)["'])?/gm;
  for (const m of text.matchAll(re)) out.set(m[1], m[2] || null);
  return out;
}

// exportsOf reads the `export`ed names of a script module, so completing `cart.` after
// `include cart from "./cart.mzs"` offers what that file actually exposes. Failures are
// silent: an unreadable path is the diagnostics' business, not the completion's.
const exportCache = new Map();
function exportsOf(documentPath, modulePath) {
  const full = path.resolve(path.dirname(documentPath), modulePath);
  let stamp = 0;
  try {
    stamp = fs.statSync(full).mtimeMs;
  } catch (err) {
    return [];
  }
  const hit = exportCache.get(full);
  if (hit && hit.stamp === stamp) return hit.names;

  let names = [];
  try {
    const src = fs.readFileSync(full, 'utf8');
    const re = /^[ \t]*export[ \t]+(?:fn[ \t]+)?([A-Za-z_\u0400-\u04FF][\w\u0400-\u04FF]*)/gm;
    names = [...src.matchAll(re)].map(m => m[1]);
  } catch (err) {
    names = [];
  }
  exportCache.set(full, { stamp, names });
  return names;
}

// moduleItems completes the members of `name.` when name is a module the file included.
function moduleItems(document, name) {
  const included = includesIn(document.getText());
  if (!included.has(name)) return null;

  const modulePath = included.get(name);
  if (modulePath) {
    const items = exportsOf(document.uri.fsPath || '', modulePath).map(n => {
      const item = new vscode.CompletionItem(n, vscode.CompletionItemKind.Function);
      item.detail = `${name}.${n} — from ${modulePath}`;
      item.sortText = '0' + n;
      return item;
    });
    return items;
  }

  const table = API.modules[name];
  if (!table) return null;
  return Object.entries(table).map(([n, doc]) => {
    const item = new vscode.CompletionItem(n, vscode.CompletionItemKind.Function);
    item.documentation = docFor(n, doc, name);
    if (doc && doc.sig) item.detail = doc.sig;
    item.sortText = '0' + n;
    return item;
  });
}

// includeItems completes the `include` line itself: every built-in module, with the
// host option it needs spelled out.
function includeItems(document) {
  const already = includesIn(document.getText());
  return Object.keys(API.modules)
    .filter(name => !already.has(name))
    .map(name => {
      const item = new vscode.CompletionItem(name, vscode.CompletionItemKind.Module);
      item.detail = GATES[name] ? `needs ${GATES[name]}` : 'no extra options';
      item.documentation = new vscode.MarkdownString(
        `Module \`${name}\`: ` + Object.keys(API.modules[name]).join(', ')
      );
      item.sortText = '0' + name;
      return item;
    });
}

// --- completion -------------------------------------------------------------------

function methodItems(recv) {
  const seen = new Map();
  const own = recv || [];
  const kinds = recv ? [...recv, 'any'] : Object.keys(API.methods);
  for (const k of kinds) {
    const table = API.methods[k];
    if (!table) continue;
    for (const [name, doc] of Object.entries(table)) {
      if (seen.has(name)) continue;
      const item = new vscode.CompletionItem(name, vscode.CompletionItemKind.Method);
      item.documentation = docFor(name, doc, k === 'any' ? null : k);
      if (doc && doc.sig) item.detail = doc.sig;
      // Methods the receiver actually has sort above the catch-all union.
      item.sortText = (own.includes(k) ? '0' : '1') + name;
      seen.set(name, item);
    }
  }
  // UFCS (D18): `x.f` *is* `f(x)`, so every global function is a method spelling too —
  // `x.dup`, `xs.sum`, `flags.band(0xff)`. The registry keeps them as builtins rather
  // than as a row per kind, so completion has to union them in or the dot hides them.
  for (const [name, doc] of Object.entries(API.builtins)) {
    if (seen.has(name)) continue;
    const item = new vscode.CompletionItem(name, vscode.CompletionItemKind.Method);
    item.documentation = docFor(name, doc, null);
    if (doc && doc.sig) item.detail = doc.sig;
    item.sortText = '2' + name;
    seen.set(name, item);
  }
  return [...seen.values()];
}

function globalItems(document) {
  const items = [];
  for (const [name, doc] of Object.entries(API.builtins)) {
    const item = new vscode.CompletionItem(name, vscode.CompletionItemKind.Function);
    item.documentation = docFor(name, doc, null);
    if (doc && doc.sig) item.detail = doc.sig;
    item.sortText = '0' + name;
    items.push(item);
  }
  for (const kw of KEYWORDS) {
    const item = new vscode.CompletionItem(kw, vscode.CompletionItemKind.Keyword);
    item.sortText = '1' + kw;
    items.push(item);
  }
  for (const v of SPECIAL_VARS) {
    const item = new vscode.CompletionItem(v, vscode.CompletionItemKind.Variable);
    item.sortText = '1' + v;
    items.push(item);
  }
  // The modules this file included: without the include the name does not exist, so
  // offering the rest would be offering an error (§12.8).
  for (const [name, modPath] of includesIn(document.getText())) {
    const item = new vscode.CompletionItem(name, vscode.CompletionItemKind.Module);
    item.detail = modPath ? `a module from ${modPath}` : `a built-in module`;
    item.sortText = '0' + name;
    items.push(item);
  }

  // Names the file itself introduces: locals, functions and $globals.
  const text = document.getText();
  const seen = new Set();
  const add = (name, kind) => {
    if (seen.has(name)) return;
    seen.add(name);
    const item = new vscode.CompletionItem(name, kind);
    item.sortText = '2' + name;
    items.push(item);
  };
  for (const m of text.matchAll(/\$[A-Za-z0-9_Ѐ-ӿ]+/g)) {
    add(m[0], vscode.CompletionItemKind.Variable);
  }
  for (const m of text.matchAll(/\b(?:fn|def)\s+([A-Za-z_][A-Za-z0-9_]*[?!]?)/g)) {
    add(m[1], vscode.CompletionItemKind.Function);
  }
  for (const m of text.matchAll(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:=|\+=|-=|\|\|=)(?!=)/gm)) {
    add(m[1], vscode.CompletionItemKind.Variable);
  }
  return items;
}

const completionProvider = {
  provideCompletionItems(document, position) {
    if (!cfg().get('completion.enable')) return [];
    const before = document.getText(
      new vscode.Range(new vscode.Position(position.line, 0), position)
    );

    // `include ` — offer the built-in modules and what each one needs.
    if (/^\s*include\s+[A-Za-z_Ѐ-ӿ]*$/.test(before)) return includeItems(document);

    const dot = before.match(/(\?\.|\.)\s*[A-Za-z_Ѐ-ӿ]*$/);
    if (dot) {
      const recv = before.slice(0, before.length - dot[0].length);
      // A module reached through its include has members, not methods (§8.7).
      const name = recv.match(/([A-Za-z_Ѐ-ӿ][\wЀ-ӿ]*)\s*$/);
      if (name) {
        const members = moduleItems(document, name[1]);
        if (members) return members;
      }
      return methodItems(inferReceiver(recv));
    }
    return globalItems(document);
  },
};

// --- hover ------------------------------------------------------------------------

const hoverProvider = {
  provideHover(document, position) {
    const range = document.getWordRangeAtPosition(position, /\$?[A-Za-z_Ѐ-ӿ][\wЀ-ӿ]*[?!]?/);
    if (!range) return null;
    const word = document.getText(range);
    const before = document.getText(
      new vscode.Range(new vscode.Position(range.start.line, 0), range.start)
    );

    if (/(\?\.|\.)\s*$/.test(before)) {
      const head = before.replace(/(\?\.|\.)\s*$/, '');
      const modName = head.match(/([A-Za-z_Ѐ-ӿ][\wЀ-ӿ]*)\s*$/);
      if (modName && API.modules[modName[1]] && API.modules[modName[1]][word]) {
        const mod = modName[1];
        const md = docFor(word, API.modules[mod][word], mod);
        if (GATES[mod]) md.appendMarkdown(`\n\nModule \`${mod}\` needs \`${GATES[mod]}\` and an \`include ${mod}\` line.`);
        else md.appendMarkdown(`\n\nAn \`include ${mod}\` line is needed.`);
        return new vscode.Hover(md, range);
      }
      const recvKinds = inferReceiver(head);
      const own = (recvKinds || []).find(k => API.methods[k] && API.methods[k][word]);
      const kinds = own ? [own] : kindsWith(word);
      // No kind registers it, but a global function of that name is reachable here all
      // the same — that is UFCS (D18), and `flags.band(0xff)` must hover like `band`.
      if (!kinds.length && API.builtins[word]) {
        return new vscode.Hover(docFor(word, API.builtins[word], null), range);
      }
      if (!kinds.length) return null;
      const doc = API.methods[kinds[0]][word];
      const md = docFor(word, doc, kinds.length === 1 ? kinds[0] : null);
      if (kinds.length > 1) {
        md.appendMarkdown('\n\nReceivers: ' + kinds.map(k => '`' + k + '`').join(', '));
      }
      return new vscode.Hover(md, range);
    }

    if (API.builtins[word]) return new vscode.Hover(docFor(word, API.builtins[word], null), range);
    if (KEYWORDS.includes(word)) {
      const md = new vscode.MarkdownString();
      md.appendCodeblock(word, 'mzs');
      md.appendMarkdown('\nAn mzs keyword.');
      return new vscode.Hover(md, range);
    }
    return null;
  },
};

// --- diagnostics ------------------------------------------------------------------

// `mzs --check -` reads the buffer from stdin and reports `<stdin>:line:col: kind: msg`,
// so unsaved edits are checked without touching disk.
const DIAG_LINE = /^(.*?):(\d+):(\d+):\s*(\w+):\s*(.*)$/;

let missingBinaryWarned = false;

function runCheck(document, collection) {
  if (!cfg().get('diagnostics.enable')) {
    collection.delete(document.uri);
    return;
  }
  const args = ['--check', ...capabilityFlags(), '-'];

  let proc;
  try {
    proc = cp.spawn(binary(), args, { cwd: path.dirname(document.uri.fsPath || '.') });
  } catch (err) {
    return;
  }

  let out = '';
  proc.stdout.on('data', d => (out += d));
  proc.stderr.on('data', d => (out += d));
  proc.on('error', err => {
    if (err.code === 'ENOENT' && !missingBinaryWarned) {
      missingBinaryWarned = true;
      vscode.window.showWarningMessage(
        `mzs: could not find the executable "${binary()}". Build it: go build -o ~/bin/mzs ./cmd/mzs — ` +
        'or name the path in the mzs.path setting.'
      );
    }
  });
  proc.on('close', () => {
    const diags = [];
    for (const line of out.split('\n')) {
      const m = DIAG_LINE.exec(line);
      if (!m) continue;
      const [, , lineNo, col, kind, msg] = m;
      const ln = Math.max(0, parseInt(lineNo, 10) - 1);
      const ch = Math.max(0, parseInt(col, 10) - 1);
      // Underline the whole token starting at the reported column when we can find one.
      const text = document.lineAt(Math.min(ln, document.lineCount - 1)).text;
      const tail = text.slice(ch);
      const width = (tail.match(/^[\wЀ-ӿ$?!]+/) || [''])[0].length || 1;
      const range = new vscode.Range(ln, ch, ln, ch + width);
      const severity = kind === 'warning'
        ? vscode.DiagnosticSeverity.Warning
        : vscode.DiagnosticSeverity.Error;
      const d = new vscode.Diagnostic(range, msg, severity);
      d.source = 'mzs';
      d.code = kind;
      diags.push(d);
    }
    collection.set(document.uri, diags);
  });

  proc.stdin.on('error', () => {});
  proc.stdin.end(document.getText());
}

// --- commands ---------------------------------------------------------------------

let terminal = null;

function mzsTerminal() {
  if (!terminal || terminal.exitStatus !== undefined) {
    terminal = vscode.window.createTerminal('mzs');
  }
  return terminal;
}

function quote(s) {
  return "'" + String(s).replace(/'/g, `'\\''`) + "'";
}

// capabilityFlags are the host options the CLI exposes (§15). They are on by default so
// that a file which includes `time` is checked as it will be run; turn one off to see the
// file the way a sandboxed embedder would. `http` needs no flag — it is always installed.
function capabilityFlags() {
  const flags = [];
  if (cfg().get('time') !== false) flags.push('--time');
  return flags;
}

function runFile() {
  const ed = vscode.window.activeTextEditor;
  if (!ed || ed.document.languageId !== 'mzs') return;
  ed.document.save().then(() => {
    const t = mzsTerminal();
    const flags = capabilityFlags().join(' ');
    t.show(true);
    t.sendText(`${binary()}${flags ? ' ' + flags : ''} ${quote(ed.document.uri.fsPath)}`);
  });
}

// runTests is `mzs test` in a terminal: the report is many lines long and belongs where
// the output of a test run belongs.
function runTests() {
  const folder = vscode.workspace.workspaceFolders && vscode.workspace.workspaceFolders[0];
  const target = folder ? folder.uri.fsPath : '.';
  // `mzs test` reads the files on disk, so an unsaved edit would be tested as it was the
  // last time it was written. `runFile` saves for the same reason.
  vscode.workspace.saveAll(false).then(() => {
    const t = mzsTerminal();
    const flags = capabilityFlags().join(' ');
    t.show(true);
    t.sendText(`${binary()} test${flags ? ' ' + flags : ''} ${quote(target)}`);
  });
}

function evalSelection() {
  const ed = vscode.window.activeTextEditor;
  if (!ed) return;
  const src = ed.document.getText(ed.selection).trim();
  if (!src) return;
  const args = [...capabilityFlags(), '-e', src];
  cp.execFile(binary(), args, (err, stdout, stderr) => {
    const out = (stdout || '').trim() || (stderr || '').trim() || '(no output)';
    if (err && !stdout) {
      vscode.window.showErrorMessage('mzs: ' + out.split('\n')[0]);
    } else {
      vscode.window.setStatusBarMessage('mzs → ' + out.split('\n')[0], 8000);
      vscode.window.showInformationMessage('mzs → ' + out);
    }
  });
}

function dump(flag, title) {
  return () => {
    const ed = vscode.window.activeTextEditor;
    if (!ed || ed.document.languageId !== 'mzs') return;
    const proc = cp.spawn(binary(), [flag, ...capabilityFlags(), '-'], {});
    let out = '';
    proc.stdout.on('data', d => (out += d));
    proc.stderr.on('data', d => (out += d));
    proc.on('error', () => vscode.window.showErrorMessage(`mzs: could not find "${binary()}"`));
    proc.on('close', () => {
      vscode.workspace.openTextDocument({ content: out, language: 'plaintext' })
        .then(doc => vscode.window.showTextDocument(doc, vscode.ViewColumn.Beside))
        .then(() => vscode.window.setStatusBarMessage(title, 3000));
    });
    proc.stdin.on('error', () => {});
    proc.stdin.end(ed.document.getText());
  };
}

// --- formatting -------------------------------------------------------------------

// `mzs fmt` is a filter: source on stdin, source on stdout, and nothing to configure
// (SPEC §15.1). That makes the provider a pipe, and it is registered whether or not the
// language server is running — formatting is the one answer that needs no analysis.
const formattingProvider = {
  provideDocumentFormattingEdits(document) {
    return new Promise(resolve => {
      const proc = cp.spawn(binary(), ['fmt'], {});
      let out = '';
      let err = '';
      proc.stdout.on('data', d => (out += d));
      proc.stderr.on('data', d => (err += d));
      proc.on('error', () => {
        vscode.window.showErrorMessage(`mzs: could not find "${binary()}"`);
        resolve([]);
      });
      proc.on('close', code => {
        // A file that does not parse has no formatted form, and the diagnostics already
        // say why: leaving the buffer alone beats an error on every save.
        if (code !== 0 || !out) {
          if (err) vscode.window.setStatusBarMessage(err.trim().split('\n')[0], 5000);
          resolve([]);
          return;
        }
        const whole = new vscode.Range(
          document.positionAt(0),
          document.positionAt(document.getText().length)
        );
        resolve(out === document.getText() ? [] : [vscode.TextEdit.replace(whole, out)]);
      });
      proc.stdin.on('error', () => {});
      proc.stdin.end(document.getText());
    });
  },
};

// --- the language server ------------------------------------------------------------

// `mzs lsp` answers the same questions this file answers from data/api.json, plus the two
// it cannot: where a name was bound, and what a file declares. It is opt-in
// (`mzs.lsp.enable`) because the built-in providers work with no process to manage, and
// this is the first version of the client.

const LSP_COMPLETION_KIND = {
  2: vscode.CompletionItemKind.Method,
  3: vscode.CompletionItemKind.Function,
  6: vscode.CompletionItemKind.Variable,
  9: vscode.CompletionItemKind.Module,
  14: vscode.CompletionItemKind.Keyword,
};

const LSP_SYMBOL_KIND = {
  2: vscode.SymbolKind.Module,
  12: vscode.SymbolKind.Function,
  13: vscode.SymbolKind.Variable,
  23: vscode.SymbolKind.Struct,
};

function toRange(r) {
  if (!r) return new vscode.Range(0, 0, 0, 0);
  return new vscode.Range(r.start.line, r.start.character, r.end.line, r.end.character);
}

function toMarkdown(contents) {
  const md = new vscode.MarkdownString(contents && contents.value ? contents.value : '');
  md.supportHtml = false;
  return md;
}

function documentParams(document) {
  return { textDocument: { uri: document.uri.toString() } };
}

function positionParams(document, position) {
  return {
    textDocument: { uri: document.uri.toString() },
    position: { line: position.line, character: position.character },
  };
}

/** startServer wires `mzs lsp` to the editor and returns its disposables, or null. */
function startServer(context, collection, onFail) {
  const client = new Client(binary(), ['lsp'], (method, params) => {
    if (method !== 'textDocument/publishDiagnostics' || !params) return;
    const uri = vscode.Uri.parse(params.uri);
    collection.set(uri, (params.diagnostics || []).map(d => {
      const diag = new vscode.Diagnostic(
        toRange(d.range),
        d.message,
        d.severity === 2 ? vscode.DiagnosticSeverity.Warning : vscode.DiagnosticSeverity.Error
      );
      diag.source = d.source || 'mzs';
      if (d.code) diag.code = d.code;
      return diag;
    }));
  }, message => {
    collection.clear();
    onFail(message);
  });

  client.start();
  // Everything the editor does before the handshake answers is queued by the client, so
  // the documents already open are sent once, here, and the listeners below cannot race
  // them.
  client.initialize({
    processId: process.pid,
    rootUri: null,
    capabilities: {},
  }).then(() => {
    vscode.workspace.textDocuments.forEach(doc => openOnServer(client, doc));
  }).catch(err => onFail(err.message));

  const selector = { language: 'mzs', scheme: 'file' };
  return [
    { dispose: () => client.stop() },
    vscode.languages.registerHoverProvider(selector, {
      provideHover(document, position) {
        return client.request('textDocument/hover', positionParams(document, position))
          .then(res => (res ? new vscode.Hover(toMarkdown(res.contents), toRange(res.range)) : null))
          .catch(() => null);
      },
    }),
    vscode.languages.registerCompletionItemProvider(selector, {
      provideCompletionItems(document, position) {
        return client.request('textDocument/completion', positionParams(document, position))
          .then(res => (res || []).map(it => {
            const item = new vscode.CompletionItem(it.label, LSP_COMPLETION_KIND[it.kind]);
            if (it.detail) item.detail = it.detail;
            if (it.documentation) item.documentation = toMarkdown(it.documentation);
            if (it.sortText) item.sortText = it.sortText;
            return item;
          }))
          .catch(() => []);
      },
    }, '.'),
    vscode.languages.registerDefinitionProvider(selector, {
      provideDefinition(document, position) {
        return client.request('textDocument/definition', positionParams(document, position))
          .then(res => (res ? new vscode.Location(vscode.Uri.parse(res.uri), toRange(res.range)) : null))
          .catch(() => null);
      },
    }),
    vscode.languages.registerDocumentSymbolProvider(selector, {
      provideDocumentSymbols(document) {
        return client.request('textDocument/documentSymbol', documentParams(document))
          .then(res => (res || []).map(sym => new vscode.DocumentSymbol(
            sym.name,
            sym.detail || '',
            LSP_SYMBOL_KIND[sym.kind] || vscode.SymbolKind.Variable,
            toRange(sym.range),
            toRange(sym.selectionRange)
          )))
          .catch(() => []);
      },
    }),
    vscode.workspace.onDidOpenTextDocument(doc => openOnServer(client, doc)),
    vscode.workspace.onDidChangeTextDocument(e => {
      if (e.document.languageId !== 'mzs') return;
      client.notify('textDocument/didChange', {
        textDocument: { uri: e.document.uri.toString(), version: e.document.version },
        contentChanges: [{ text: e.document.getText() }],
      });
    }),
    vscode.workspace.onDidSaveTextDocument(doc => {
      if (doc.languageId !== 'mzs') return;
      client.notify('textDocument/didSave', {
        textDocument: { uri: doc.uri.toString() },
        text: doc.getText(),
      });
    }),
    vscode.workspace.onDidCloseTextDocument(doc => {
      if (doc.languageId !== 'mzs') return;
      client.notify('textDocument/didClose', { textDocument: { uri: doc.uri.toString() } });
    }),
  ];
}

function openOnServer(client, doc) {
  if (doc.languageId !== 'mzs') return;
  client.notify('textDocument/didOpen', {
    textDocument: {
      uri: doc.uri.toString(),
      languageId: 'mzs',
      version: doc.version,
      text: doc.getText(),
    },
  });
}

// --- activation -------------------------------------------------------------------

function activate(context) {
  const selector = { language: 'mzs', scheme: 'file' };
  const collection = vscode.languages.createDiagnosticCollection('mzs');

  context.subscriptions.push(
    collection,
    vscode.languages.registerDocumentFormattingEditProvider(selector, formattingProvider),
    vscode.commands.registerCommand('mzs.runFile', runFile),
    vscode.commands.registerCommand('mzs.runTests', runTests),
    vscode.commands.registerCommand('mzs.evalSelection', evalSelection),
    vscode.commands.registerCommand('mzs.showTokens', dump('--tokens', 'the token stream')),
    vscode.commands.registerCommand('mzs.showAst', dump('--ast', 'AST'))
  );

  if (cfg().get('lsp.enable')) {
    let fallen = false;
    const fallBack = message => {
      if (fallen) return;
      fallen = true;
      vscode.window.showWarningMessage(
        `mzs: ${message}. Falling back to the built-in analysis; turn off mzs.lsp.enable to silence this.`
      );
      registerBuiltins(context, selector, collection);
    };
    context.subscriptions.push(...startServer(context, collection, fallBack));
    return;
  }

  registerBuiltins(context, selector, collection);
}

// registerBuiltins is the extension's own analysis: hover and completion out of
// data/api.json, diagnostics out of `mzs --check`. It is what runs when the language
// server is switched off, and what it falls back to when the server cannot be started.
function registerBuiltins(context, selector, collection) {
  context.subscriptions.push(
    vscode.languages.registerCompletionItemProvider(selector, completionProvider, '.'),
    vscode.languages.registerHoverProvider(selector, hoverProvider)
  );

  let timer = null;
  const schedule = doc => {
    if (doc.languageId !== 'mzs') return;
    clearTimeout(timer);
    timer = setTimeout(() => runCheck(doc, collection), cfg().get('diagnostics.delay') || 300);
  };

  context.subscriptions.push(
    vscode.workspace.onDidChangeTextDocument(e => schedule(e.document)),
    vscode.workspace.onDidOpenTextDocument(doc => schedule(doc)),
    vscode.workspace.onDidSaveTextDocument(doc => schedule(doc)),
    vscode.workspace.onDidCloseTextDocument(doc => collection.delete(doc.uri))
  );
  vscode.workspace.textDocuments.forEach(schedule);
}

function deactivate() {}

module.exports = { activate, deactivate };
