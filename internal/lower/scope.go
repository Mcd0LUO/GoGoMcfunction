package lower

import (
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// Scope is the module-wide view of the three declaration tables.
//
// `obj`, `extern` and `nbt` declare things a MODULE uses, not things a FILE
// uses. A module is one namespace: the boot function is one, the constant pool
// is one, the objective declarations are one. The specification says so
// directly -- 'boot 汇总**所有文件**的 [load] 函数和常量池' -- and the three
// declaration tables were the ones that did not follow.
//
// They were per-file, and that made the MEANING of a name depend on which
// file the author happened to be in:
//
//	obj Ap.Amount          // file A: the objective
//	fn f() { Ap.Amount = 1 }   // file B: the fake player Ap's score 'Amount'
//
// Same module, same name, two meanings, and nothing said which one applied.
// The second one compiled cleanly, wrote to a score nobody reads, and 1.12.2
// discarded it without an error -- a silent no-op, the failure this compiler
// exists to make impossible.
//
// The three tables live here so that one module-wide view can be built once
// and handed to every file's lowering. The per-file VALIDATION stays
// per-file: a file is still responsible for its own duplicate declarations,
// because 'declared twice' is a fact about one file's text.
type Scope struct {
	// Objs names the objectives declared with `obj`, across the module.
	Objs map[string]bool
	// Externs binds every extern declaration, by name.
	Externs map[string]syntax.ExternDecl
	// Fields are the `nbt` declarations, by name.
	Fields map[string]syntax.NBTField
	// warned remembers the dotted names already reported, so one ambiguous
	// name is reported ONCE for the whole module rather than once per use.
	//
	// A map is a reference, so copying a Scope shares this set. That is
	// what makes the report module-wide: the same name in two files is one
	// mistake, and saying so twice would just be noise.
	warned map[string]bool
}

// NewScope returns an empty scope, ready to be filled.
func NewScope() Scope {
	return Scope{
		Objs:    map[string]bool{},
		Externs: map[string]syntax.ExternDecl{},
		Fields:  map[string]syntax.NBTField{},
		warned:  map[string]bool{},
	}
}

// CollectScope gathers the declarations of every file in a module.
//
// The first declaration of a name wins. Two files declaring the same
// objective is not an error -- it reads like a header, and the back end
// declares each objective once anyway -- but an `obj` and an `extern` on the
// same name ARE, and that is checked where the two meet: in lowerObjs, which
// sees the merged extern table.
func CollectScope(files []*syntax.File) Scope {
	s := NewScope()
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, od := range f.Objs {
			s.Objs[od.Name] = true
		}
		for _, x := range f.Externs {
			if _, taken := s.Externs[x.Name]; !taken {
				s.Externs[x.Name] = x
			}
		}
		for _, fd := range f.NBTFields {
			if _, taken := s.Fields[fd.Name]; !taken {
				s.Fields[fd.Name] = fd
			}
		}
	}
	return s
}

// holders lists the score holders the module has introduced.
//
// An extern names a holder outright: `extern SEED: int = seed.math` says
// 'seed is a holder and math is the score on it'. Once a module has said that
// about a name, a dotted name starting with it is unambiguously the
// holder.objective shorthand, and the compiler has no business warning about
// it.
//
// The holder of an extern bound to @s is not a holder in this sense: @s is
// the executor, not a fake player, and no dotted name can start with it.
func (s Scope) holders() map[string]bool {
	out := map[string]bool{}
	for _, x := range s.Externs {
		if x.Kind != "var" && x.Kind != "global" {
			continue
		}
		if x.Holder == "" || x.Holder == "@s" {
			continue
		}
		out[x.Holder] = true
	}
	return out
}

// ambiguous reports whether a dotted name is one the author may have meant
// as a DECLARED objective rather than as the holder.objective shorthand.
//
// The two shapes are the same text, so the compiler cannot tell them
// apart -- but it CAN tell which readings are still open, and that is
// enough to say something useful.
//
// The rule, in one line: a dotted name is worth reporting when the module
// has never said what the holder part means. Three facts settle it.
//
//  1. If the WHOLE name is a declared objective, there is no ambiguity:
//     it is that objective.
//
//  2. If the HOLDER part is a name the module has introduced -- an extern
//     bound to a named holder, like `extern SEED: int = seed.math` -- then
//     the author has already said what that holder is, and the dotted
//     name is unambiguously the shorthand. Nothing to report.
//
//  3. Otherwise the holder part is a name nobody has introduced. It is
//     still perfectly good shorthand for a fake player the module creates
//     some other way, so this is NOT an error. But it is also exactly what
//     a mistyped objective declaration looks like: the author wrote
//     `Ap.Defence`, meant the objective of that name, and forgot the
//     `obj Ap.Defence` line. The compiler then declares an objective
//     called `Defence` and writes the score of a fake player called `Ap` --
//     a different thing, silently.
//
// So it is a WARNING, and the message names both readings. The author
// decides; the compiler just refuses to be quiet about it.
//
// The check is deliberately CONSERVATIVE. It only reports a name whose
// holder part has no other meaning in the module, so a module that uses
// the shorthand properly -- which the specification's own examples do --
// stays quiet.
func (s Scope) ambiguous(name string) bool {
	i := strings.Index(name, ".")
	if i <= 0 {
		return false
	}
	if s.Objs[name] {
		return false
	}
	if s.holders()[name[:i]] {
		return false
	}
	return true
}

// warnAmbiguous reports a dotted name whose reading the module has not
// settled.
//
// The two readings of a dotted name are: an objective whose name contains a
// dot, and the holder.objective shorthand for a fake player's score. The
// language keeps both, because the reference data pack needs both -- it
// declares dozens of dotted objectives (Ap.Amount, Time.day, wp.damage1) and
// uses the shorthand for its fake players.
//
// What the compiler CAN do is notice when the author probably meant the
// first and forgot to declare it. The mistake produces a completely
// different program:
//
//	obj X
//	fn f() { Ap.Defence = 1 }
//
// reads as a score of a fake player named Ap, and the compiler declares an
// objective called Defence -- so the declaration in the source is not the
// declaration in the world, and the write lands on a score nobody reads.
//
// It is a WARNING, not an error, because the shorthand is legitimate and
// the compiler cannot know which one the author meant. What it can do is
// name both readings and the two ways out.
//
// The report is once per name for the whole module: a name used in five
// places is one decision, not five.
func (l *Lowerer) warnAmbiguous(name string, p syntax.Pos) {
	if !l.scope.ambiguous(name) {
		return
	}
	if l.scope.warned[name] {
		return
	}
	l.scope.warned[name] = true
	i := strings.Index(name, ".")
	l.diags.Warn(posOf(p), "W_DOTTED_UNDECLARED",
		"%s is not a declared objective, so it reads as the fake player %s's score %q; "+
			"if you meant an objective named %s, declare it with `obj %s` -- "+
			"1.12.2 writes to a score that does not exist without saying so",
		name, name[:i], name[i+1:], name, name)
}

// fanoutReturn reports a `return` inside a fan-out body.
//
// A fan-out body is not a block of the enclosing function: it is a
// FUNCTION of its own, entered by
//
//	execute <sel> ~ ~ ~ function <body>
//
// So a `return` inside it returns from the BODY -- one entity's pass --
// and the function that wrote the loop carries on. That is not what
// `return` means anywhere else in the language, and it is not what the
// author meant.
//
// The two behaviours were measured on the 1.12.2 interpreter, and they
// are different:
//
//	for e in entities<@e[tag=m1]> { return 5 }   // body ends; caller continues
//	flag = 99                                   // <- still runs
//
//	while 1 == 1, budget 3 { return 7 }          // whole function ends
//	flag = 98                                   // <- does NOT run
//
// The same word, the same function, two meanings, decided by which
// statement the body sits in. The author who wrote `return` to leave a
// loop early gets a program that keeps going -- silently, because
// nothing about the generated commands is wrong.
//
// A `while` body stays inside the enclosing function, so its `return`
// really does leave the function. That one is correct, and this
// diagnostic does not touch it.
//
// The message says what the author can do instead, because there is a
// way out: put the early exit in a helper function, or restructure the
// loop. What there is NOT is a way to make `return` mean "leave the
// enclosing function" from inside a fan-out body -- 1.12.2's `execute`
// has no such edge, and inventing one would be a lie.
func (l *Lowerer) fanoutReturn(s syntax.ReturnStmt) {
	l.diags.Error(posOf(s.P), "E_FANOUT_RETURN",
		"a return inside a fan-out body leaves the BODY, not the function: `for e in entities<...>` compiles to `execute <sel> ~ ~ ~ function <body>`, so the body is a function of its own and the function that wrote the loop carries on afterwards. 1.12.2 has no way to leave that outer function from here",
	)
}
