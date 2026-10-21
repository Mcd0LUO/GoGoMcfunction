// VS Code client for ggmls.
//
// The client is thin on purpose: every decision about what a diagnostic means
// belongs to the compiler, which is the thing that has to be right. This file
// starts the process and gets out of the way.

const vscode = require("vscode");
const { LanguageClient, TransportKind } = require("vscode-languageclient/node");
const fs = require("fs");
const path = require("path");

let client;

// resolveServer finds the ggmls binary.
//
// Order: the explicit setting, then the workspace root, then PATH. The
// workspace root is tried because this repository builds ggmls.exe next to
// ggm.exe, and a contributor working on the compiler should not have to
// configure anything to use what they just built.
function resolveServer(context) {
  const configured = vscode.workspace.getConfiguration("ggm").get("serverPath", "");
  if (configured) {
    return configured;
  }
  const exe = process.platform === "win32" ? "ggmls.exe" : "ggmls";
  for (const folder of vscode.workspace.workspaceFolders ?? []) {
    const candidate = path.join(folder.uri.fsPath, exe);
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }
  const bundled = path.join(context.extensionPath, "bin", exe);
  if (fs.existsSync(bundled)) {
    return bundled;
  }
  return exe; // fall back to PATH
}

function activate(context) {
  const command = resolveServer(context);
  const args = [];

  const serverOptions = {
    command,
    args,
    transport: TransportKind.stdio,
  };

  const clientOptions = {
    documentSelector: [{ scheme: "file", language: "ggm" }],
    synchronize: {
      // Watch for files appearing or being renamed outside the editor, so a
      // cross-file call does not stay "unknown" because a new file was added.
      fileEvents: vscode.workspace.createFileSystemWatcher("**/*.ggm"),
    },
    traceOutputChannel: vscode.window.createOutputChannel("GGM Language Server"),
  };

  client = new LanguageClient("ggm", "GGM Language Server", serverOptions, clientOptions);
  client.start().catch((err) => {
    vscode.window.showErrorMessage(
      'GGM language server failed to start from "' + command + '": ' +
        err.message +
        '. Set ggm.serverPath to the ggmls binary.'
    );
  });
  context.subscriptions.push({ dispose: () => client && client.stop() });
}

function deactivate() {
  return client ? client.stop() : undefined;
}

module.exports = { activate, deactivate, resolveServer };
