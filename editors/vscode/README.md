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

## What works

| Feature | What it shows |
|---|---|
| Diagnostics | The compiler's own messages, as you type |
| Hover | The declaration under the cursor, its signature and where it lives |
| Go to definition | From a use to the declaration, including locals and params |
| Completion | Names in the module, filtered by the fragment under the cursor |
| Outline | The declarations in this file |
| Find references | Every use of the symbol under the cursor |
| Signature help | The call being typed, and which argument you are on |
| Rename | Every use of the symbol, and no same-named stranger |
| Quick fix | Declare an objective the program writes but never declares |
| Workspace search | Names across every module the server has indexed |

Hover and jump both report the KIND of a name, which matters here: an
objective, an extern and a class field look identical at a use site and mean
three different things. An undeclared dotted name is reported as the
holder.objective shorthand rather than as a missing declaration.

## Checking a query without the editor

The server and the compiler share `internal/symbols`, and the compiler exposes
it:

```sh
ggm -query hover    -at a.ggm:9:13  a.ggm b.ggm
ggm -query def      -at a.ggm:9:13  a.ggm b.ggm
ggm -query complete -at a.ggm:9:13  a.ggm b.ggm
```

Positions are 1-based, the way the compiler reports errors. This exists so
that changing a hover message is a command and not an editor session; the same
code path answers the editor, so what the command prints is what ships.

## How the server groups files

A GGM module is one `namespace` spread over several files, and the server
compiles the whole module whenever any of its files changes. Compiling only
the file being edited would report every cross-file call as an unknown callee,
which is a false alarm on correct code and worse than showing nothing.
