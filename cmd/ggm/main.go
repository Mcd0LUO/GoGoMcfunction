// Command ggm compiles GGM source to Minecraft 1.12.2 mcfunction files.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ggm: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		outDir   = flag.String("out", "data/functions", "output directory for .mcfunction files")
		check    = flag.Bool("check", false, "check only; produce no files")
		quiet    = flag.Bool("q", false, "suppress per-file listing")
		tgtName  = flag.String("target", target.V1_12_2.Name, "Minecraft version to target")
		levelStr = flag.String("O", "2", "optimisation level: 0, 1, 2 or 3")
		showSkip = flag.Bool("v", false, "explain which optimisations the level omits")
		showVer  = flag.Bool("version", false, "print the version and exit")
		comments = flag.Bool("comments", false, "put a header naming each generated file's declaration")
		showIR   = flag.Bool("ir", false, "print the optimised IR instead of writing files")
		query    = flag.String("query", "", "answer a question about a position: hover, def or complete")
		at       = flag.String("at", "", "position for -query, as <file>:<line>:<col> (1-based)")
	)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ggm [flags] <file.ggm> ...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println(version.String())
		return nil
	}
	if flag.NArg() == 0 {
		flag.Usage()
		return fmt.Errorf("no input files")
	}
	// -query answers a question about the module instead of building it.
	//
	// It shares the index with the language server, so checking a hover
	// message or a jump target is one command rather than an editor session.
	if *query != "" {
		text, err := runQuery(*query, *at, flag.Args())
		if err != nil {
			return err
		}
		fmt.Println(text)
		return nil
	}
	level, ok := optimize.ParseLevel(*levelStr)
	if !ok {
		return fmt.Errorf("invalid optimisation level %q", *levelStr)
	}
	if *tgtName != target.V1_12_2.Name {
		return fmt.Errorf("unknown target %q (only %s is implemented)", *tgtName, target.V1_12_2.Name)
	}

	// Every file on the command line is one module, so a library passed
	// alongside its caller is linked rather than compiled twice. Compiling
	// them separately would leave the caller's cross-namespace calls
	// unresolved, which 1.12.2 treats as a call that silently never runs.
	var srcs []driver.Source
	for _, in := range flag.Args() {
		src, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		srcs = append(srcs, driver.Source{Name: in, Text: string(src)})
	}

	failed := false
	out, diags := driver.CompileFiles(srcs, driver.Options{
		Target:   target.V1_12_2,
		Level:    level,
		Comments: *comments,
	})
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "%s\n", d.String())
		if d.Severity == diag.Error {
			failed = true
		}
	}
	if *showSkip && out != nil {
		for _, sk := range out.Skipped {
			fmt.Printf("skipped at -O%d: %s\n", int(level), sk)
		}
	}
	if failed {
		return fmt.Errorf("compilation failed")
	}
	if *check || out == nil {
		return nil
	}
	// The IR, not the commands. Printed instead of writing files, because the
	// question it answers is "what shape did the optimiser produce", and
	// rendering commands on top of that only obscures it.
	if *showIR {
		fmt.Print(out.IR.DumpString())
		return nil
	}
	if err := write(out, *outDir); err != nil {
		return err
	}
	if !*quiet {
		paths := make([]string, 0, len(out.Files))
		for p := range out.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fmt.Printf("%s  (%d lines)\n", p, strings.Count(out.Files[p], "\n"))
		}
		fmt.Printf("total: %d files, %d lines\n", len(out.Files), out.CountLines())
		// 1.12.2 has no load event and no automatic tick hook, so somebody has
		// to wire the two entries by hand. Saying which ones is the difference
		// between a working data pack and one where the constant pool never
		// ran and nothing says so.
		fmt.Printf("\nwire these:\n")
		fmt.Printf("  %s   on world load, from a command block\n", out.NeedsConstInit)
		if out.HasCycle {
			fmt.Printf("  %s   gamerule gameLoopFunction\n", out.NeedsCycle)
		}
	}
	return nil
}

// write lays files out as <out>/<ns>/<path>.mcfunction, matching the 1.12.2
// save layout where functions live in the world save rather than a data pack.
func write(out *driver.Output, outDir string) error {
	paths := make([]string, 0, len(out.Files))
	for p := range out.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if strings.Count(p, ":") != 1 {
			return fmt.Errorf("internal: malformed function path %q", p)
		}
		ns, rel := splitPath(p)
		dst := filepath.Join(outDir, ns, filepath.FromSlash(rel)+".mcfunction")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(out.Files[p]), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func splitPath(p string) (string, string) {
	if i := strings.Index(p, ":"); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "minecraft", p
}
