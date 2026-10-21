package ir

// Dump renders a Program as text: one line per function, one per block, and
// one per op or terminator.
//
// It exists because reading a wrong answer out of generated .mcfunction is
// guesswork. A CFG that merged two blocks looks exactly like a CFG that
// inlined one, and that difference decides whether an optimisation is a bug.
// The dump shows the shape before the back end renders it.
//
// The format is stable enough to diff: blocks print in ID order and every
// field that affects code generation appears.
import (
	"fmt"
	"sort"
	"strings"
)

// DumpString renders the whole program.
func (p *Program) DumpString() string {
	var b strings.Builder
	fmt.Fprintf(&b, "program %s: %d funcs, %d consts, %d objs\n",
		p.Namespace, len(p.Funcs), len(p.Consts), len(p.Objs))
	if len(p.Objs) > 0 {
		names := make([]string, 0, len(p.Objs))
		for _, o := range p.Objs {
			names = append(names, o.Name)
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "  objs: %s\n", strings.Join(names, " "))
	}
	if len(p.Consts) > 0 {
		cs := make([]int, len(p.Consts))
		copy(cs, p.Consts)
		sort.Ints(cs)
		fmt.Fprintf(&b, "  consts: %v\n", cs)
	}
	for _, fn := range p.Funcs {
		fn.dumpTo(&b, "  ")
	}
	return b.String()
}

func (f *Function) dumpTo(b *strings.Builder, indent string) {
	export := ""
	if f.Exported {
		export = " [exported]"
	}
	fmt.Fprintf(b, "%sfn %s%s holder=%s\n", indent, f.Path, export, f.RegHolder)
	if len(f.Params) > 0 {
		fmt.Fprintf(b, "%s  params: %s\n", indent, strings.Join(f.Params, " "))
	}
	for _, blk := range f.Blocks {
		fmt.Fprintf(b, "%s  b%d:\n", indent, blk.ID)
		for _, op := range blk.Ops {
			fmt.Fprintf(b, "%s    %s\n", indent, dumpOp(op))
		}
		fmt.Fprintf(b, "%s    -> %s\n", indent, dumpTerm(blk.Term))
	}
}

func dumpOp(op Op) string {
	switch x := op.(type) {
	case Set:
		return fmt.Sprintf("set %s = %d", dumpLoc(x.Dst), x.Value)
	case Copy:
		return fmt.Sprintf("copy %s = %s", dumpLoc(x.Dst), dumpLoc(x.Src))
	case Arith:
		return fmt.Sprintf("arith %s %s %s", dumpLoc(x.Dst), x.Op, dumpLoc(x.Src))
	case Add:
		return fmt.Sprintf("add %s += %d", dumpLoc(x.Dst), x.Value)
	case Remove:
		return fmt.Sprintf("remove %s -= %d", dumpLoc(x.Dst), x.Value)
	case Guarded:
		return fmt.Sprintf("guarded [%s] %s", dumpPred(x.Cond), dumpOp(x.Inner))
	case Call:
		return "call " + x.Path
	case FanOut:
		return fmt.Sprintf("fanout %s -> %s", x.Selector, x.Path)
	case Raw:
		s := "raw " + x.Text
		if len(x.Args) > 0 {
			s += fmt.Sprintf(" args=%v", dumpLocs(x.Args))
		}
		if len(x.Writes) > 0 {
			s += fmt.Sprintf(" writes=%v", dumpLocs(x.Writes))
		}
		if len(x.Reads) > 0 {
			s += fmt.Sprintf(" reads=%v", dumpLocs(x.Reads))
		}
		return s
	}
	return fmt.Sprintf("%T", op)
}

func dumpTerm(t Terminator) string {
	switch x := t.(type) {
	case Jump:
		return fmt.Sprintf("jump b%d", x.Target)
	case Branch:
		return fmt.Sprintf("branch [%s] then=b%d else=b%d", dumpPred(x.Cond), x.Then, x.Else)
	case TailCall:
		return "tailcall " + x.Path
	case Return:
		return "return"
	}
	return fmt.Sprintf("%T", t)
}

func dumpPred(p Predicate) string {
	var parts []string
	for _, alt := range p.Alts {
		var cs []string
		for _, c := range alt {
			cs = append(cs, dumpCond(c))
		}
		parts = append(parts, strings.Join(cs, " and "))
	}
	s := strings.Join(parts, " or ")
	if p.Exist != nil {
		e := "exists " + p.Exist.Selector
		if p.Exist.Negate {
			e = "not " + e
		}
		if s != "" {
			s += " and "
		}
		s += e
	}
	if p.Block != nil {
		bt := fmt.Sprintf("block %s at %s,%s,%s = %s:%s", p.Block.Selector,
			p.Block.X, p.Block.Y, p.Block.Z, p.Block.Block, p.Block.Data)
		if s != "" {
			s += " and "
		}
		s += bt
	}
	if s == "" {
		return "always"
	}
	return s
}

func dumpCond(c Cond) string {
	switch {
	case c.HasMin && c.HasMax && c.Min == c.Max:
		return fmt.Sprintf("%s == %d", c.Obj, c.Min)
	case c.HasMin && c.HasMax:
		return fmt.Sprintf("%d <= %s <= %d", c.Min, c.Obj, c.Max)
	case c.HasMin:
		return fmt.Sprintf("%s >= %d", c.Obj, c.Min)
	case c.HasMax:
		return fmt.Sprintf("%s <= %d", c.Obj, c.Max)
	}
	return c.Obj
}

func dumpLoc(l Loc) string {
	if !l.Valid() {
		return "?"
	}
	return l.Holder + " " + l.Obj
}

func dumpLocs(ls []Loc) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = dumpLoc(l)
	}
	return out
}
