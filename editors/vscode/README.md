# GGM for VS Code

Shows the compiler's diagnostics in the editor. That is all it does.

## Run it from this repository (F5)

```sh
cd editors/vscode
npm install                # once
cd ../.. && go build -o ggmls.exe ./cmd/ggmls
```

Open **`editors/vscode`** in VS Code and press **F5**.

The second window (the Extension Development Host) opens the REPOSITORY ROOT,
not the extension folder. That is deliberate: the extension activates on
`onLanguage:ggm`, and the extension folder contains no `.ggm` file, so a host
opened there would show nothing at all and look broken. The repository root has
`ggmls.exe` (found by rule 2 below, no configuration) and the examples.

In that window open `examples/luo-math.ggm` or `stdlib/math.ggm`.

## Install it for real (no F5)

Copy the folder into the extensions directory, named `<publisher>.<name>-<version>`:

```sh
# Windows
xcopy /E /I editors\vscode "%USERPROFILE%\.vscode\extensions\mcd0luo.ggm-0.1.0"
```

Then put the server where it can be found. A real project will not have
`ggmls.exe` sitting in it, so either set `ggm.serverPath`, or copy the binary
into `<extension>/bin/`, or put it on `PATH`.

Restart VS Code. `.ggm` files open with the language set to GGM and the server
starts with them.

## What to open as a test workspace

Any folder with `.ggm` files. The most useful are:

| Folder | Why |
|---|---|
| `examples/` | Committed, small, exercises comptime and classes |
| `stdlib/` | Committed, one namespace over several files -- shows cross-file jumps |
| a real ported pack | Hundreds of lines; the place where diagnostics and performance actually matter |

## Known limitation

There is no syntax highlighting yet: no TextMate grammar is contributed, so a
`.ggm` file renders as plain text with brackets and comments handled only by
`language-configuration.json`. Diagnostics, hover, jumps and the rest are
unaffected -- they come from the server, not from colouring -- but the file will
look monochrome until a grammar is added.
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
