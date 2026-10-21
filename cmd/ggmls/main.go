// Command ggmls is a language server for GGM.
//
// It speaks LSP over stdio and reports compiler diagnostics. Editors start it
// as a child process:
//
//	ggmls --stdio
//
// The --stdio flag is accepted and ignored; stdio is the only transport. It
// exists because some clients pass it out of habit from other servers.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/lsp"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/version"
)

func main() {
	var (
		stdio   = flag.Bool("stdio", true, "speak LSP over stdin/stdout (the only transport)")
		root    = flag.String("root", "", "workspace root; overridden by the client's initialize request")
		showVer = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()
	_ = stdio
	if *showVer {
		fmt.Printf("ggmls %s (ggm %s)\n", "0.1.0", version.Version)
		return
	}

	dir := *root
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if err := lsp.New(dir).Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ggmls: "+err.Error())
		os.Exit(1)
	}
}
