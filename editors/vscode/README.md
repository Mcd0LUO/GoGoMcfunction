# GGM for VS Code

Shows the compiler's diagnostics in the editor. That is all it does.

## Run it from this repository

```sh
cd editors/vscode
npm install          # once
```

Build the server, then open `editors/vscode` in VS Code and press **F5**:

```sh
go build -o ggmls.exe ./cmd/ggmls     # from the repository root
```

A second VS Code window opens with the extension loaded. Open any `.ggm` file
in it and the squiggles appear as you type.

## Where it looks for the server

1. `ggm.serverPath`, if set;
2. `ggmls.exe` in the workspace root -- so a contributor sees the compiler
   they just built without configuring anything;
3. `bin/ggmls.exe` inside the extension folder;
4. `ggmls` on `PATH`.

## What works, and what does not

Diagnostics, published by the compiler itself, updated as you type.

Hover, go-to-definition and completion are not implemented. All three need a
symbol table, and the compiler produces IR rather than symbols; adding them in
the client would mean re-deriving declarations from the AST and keeping that
second implementation in sync with the language.

## How the server groups files

A GGM module is one `namespace` spread over several files, and the server
compiles the whole module whenever any of its files changes. Compiling only
the file being edited would report every cross-file call as an unknown callee,
which is a false alarm on correct code and worse than showing nothing.
