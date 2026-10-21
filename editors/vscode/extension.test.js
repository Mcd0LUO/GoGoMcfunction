// Tests for the one piece of logic in the extension: where to find ggmls.
//
// Everything else is a call into vscode-languageclient. This function decides
// which binary runs, and getting it wrong is the failure a user actually hits.

const assert = require("assert");
const fs = require("fs");
const os = require("os");
const path = require("path");
const Module = require("module");

// Stand in for the two modules that only exist inside the editor.
const fakeVscode = {
  workspace: { getConfiguration: () => ({ get: (_k, dflt) => dflt }), workspaceFolders: [] },
  window: { showErrorMessage: () => {}, createOutputChannel: () => ({}) },
};
const fakeClient = { LanguageClient: class {}, TransportKind: { stdio: 1 } };
const load = Module._load;
Module._load = function (request, parent, isMain) {
  if (request === "vscode") return fakeVscode;
  if (request === "vscode-languageclient/node") return fakeClient;
  return load.call(this, request, parent, isMain);
};

const { resolveServer } = require("./extension.js");
const exe = process.platform === "win32" ? "ggmls.exe" : "ggmls";

// 1. A binary in the workspace root wins, so a contributor uses what they built.
const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ggm-ext-"));
fs.writeFileSync(path.join(dir, exe), "");
fakeVscode.workspace.workspaceFolders = [{ uri: { fsPath: dir } }];
assert.strictEqual(
  resolveServer({ extensionPath: dir }),
  path.join(dir, exe),
  "a binary in the workspace root must be used, not PATH"
);

// 2. With nothing in the workspace, fall back to PATH rather than a missing path.
const empty = fs.mkdtempSync(path.join(os.tmpdir(), "ggm-empty-"));
fakeVscode.workspace.workspaceFolders = [{ uri: { fsPath: empty } }];
assert.strictEqual(
  resolveServer({ extensionPath: empty }),
  exe,
  "with no local binary the bare name must be returned so PATH is consulted"
);

// 3. An explicit setting is used verbatim, even if it does not exist yet.
fakeVscode.workspace.getConfiguration = () => ({ get: () => "/opt/custom/ggmls" });
assert.strictEqual(resolveServer({ extensionPath: empty }), "/opt/custom/ggmls");

console.log("extension: 3 checks passed");
