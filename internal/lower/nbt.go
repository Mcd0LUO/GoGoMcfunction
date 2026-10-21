package lower

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// The NBT codec lowers to two shapes of command text.
//
// A decode is a fold over NBT predicates. 1.12.2's
// `scoreboard players add @s <obj> <v> {<nbt>}` means "IF the target's NBT
// matches, add v", so reading a w-bit field is one reset plus w conditional
// adds. There is no way to read NBT in 1.12.2, only to match it.
//
// An encode is a dispatch over values, because NBT on the right-hand side of a
// command is constant text: a score cannot be spliced into it. One command per
// value in the requested range is the only shape there is.

// Path convention: a p"..." path is written from the container's item root,
// exactly as the keys appear in the file. For a weapon that means
// `tag.dat.atk_base`, and for the selected item's stack size just `Count`.
//
// The alternative -- treating the item's tag compound as the root so the
// common case reads `dat.atk_base` -- cannot express `Count`, which sits
// beside `tag` rather than inside it. One rule that covers every case beats a
// shorter spelling that needs an exception.

// pathParts renders a dotted path as NBT braces around the value.
//
// leafCompound is what separates the two encodings:
//
//	bits  `tag.dat.spec` + 0:1b  ->  tag:{dat:{spec:{0:1b}}}
//	enum  `tag.dat.spec` + 1b    ->  tag:{dat:{spec:1b}}
//
// A bit needs a compound level to hold the index; an enumerated value sits
// directly on the key.
func pathParts(path string, leafCompound bool) (open, close string) {
	segs := strings.Split(path, ".")
	var o, c strings.Builder
	for i, seg := range segs {
		if i == len(segs)-1 && !leafCompound {
			o.WriteString(seg + ":")
			continue
		}
		o.WriteString(seg + ":{")
		c.WriteString("}")
	}
	return o.String(), c.String()
}

// nbtMatch renders a complete NBT argument that matches one probe.
func nbtMatch(c syntax.Container, slot int, path, body string, leafCompound bool) string {
	open, close := pathParts(path, leafCompound)
	inner := open + body + close
	switch c.Key {
	case "Inventory":
		// The slot is matched as a pattern (`Slot:0b`), not used as a list
		// index: 1.12.2's Inventory list order need not follow slot numbers.
		return fmt.Sprintf("{Inventory:[{Slot:%db,%s}]}", slot, inner)
	default:
		return fmt.Sprintf("{%s:{%s}}", c.Key, inner)
	}
}

// enumKeyBody renders an enum key as the NBT body it matches.
//
// A numeric key is a byte tag (`spec:1b`). Anything else is a string inside a
// list (`Tags:["残响"]`), which is how an item carries named markers.
func enumKeyBody(key string) string {
	if _, err := strconv.Atoi(key); err == nil {
		return key + "b"
	}
	return "[" + strconv.Quote(key) + "]"
}

// lowerDecode emits one decode statement.
func (l *Lowerer) lowerDecode(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.DecodeStmt) {
	fd, ok := l.fields[s.Field]
	if !ok {
		l.diags.Error(posOf(s.P), "E_FIELD_UNKNOWN", "no nbt field named %q", s.Field)
		return
	}
	if !l.objDeclared(ctx, s.Target) {
		l.diags.Error(posOf(s.P), "E_OBJECTIVE_UNDECLARED",
			"objective %q is never declared; add `obj %s` or bind it with extern, or 1.12.2 discards every value written here",
			s.Target, s.Target)
		return
	}
	dst := l.targetLoc(ctx, s.Target, s.P)

	// One reset for the whole statement, then every slot's rungs. Resetting per
	// slot would leave only the last slot's contribution, which is exactly what
	// the hand-written defence file avoids by resetting once for all four.
	//
	// `decode … into` skips the reset so that SEVERAL statements can add to one
	// objective. The reference data pack needs that: it resets five objectives
	// once in legacy/reset, then eighteen files each add one slot's worth. Three
	// ordinary decodes cannot express it -- each resets, so only the last
	// survives, and the value is silently the third slot's alone.
	if !s.Into {
		fb.Emit(cur, ir.Set{Dst: dst, Value: 0})
	}

	slots := []int{0}
	if s.Container.IsSlot {
		slots = nil
		for v := s.Container.Lo; v <= s.Container.Hi; v++ {
			slots = append(slots, v)
		}
	}

	if fd.Bits > 0 {
		for _, slot := range slots {
			for k := 0; k < fd.Bits; k++ {
				fb.Emit(cur, ir.Raw{
					Text:   fmt.Sprintf("scoreboard players add @s %s %d %s", dst.Obj, 1<<uint(k), nbtMatch(s.Container, slot, fd.Path, fmt.Sprintf("%d:1b", k), true)),
					Writes: []ir.Loc{dst},
					Reads:  []ir.Loc{dst},
				})
			}
		}
	} else {
		for _, ec := range fd.Enum {
			for _, slot := range slots {
				fb.Emit(cur, ir.Raw{
					Text:   fmt.Sprintf("scoreboard players set @s %s %d %s", dst.Obj, ec.Value, nbtMatch(s.Container, slot, fd.Path, enumKeyBody(ec.Key), false)),
					Writes: []ir.Loc{dst},
				})
			}
		}
	}

	// Author-written cases run last so a sentinel can override the field's own
	// value. `set` overwrites and `add` accumulates, exactly as the two
	// scoreboard commands do -- a sentinel must use `set`, or it would be added
	// to the ladder's result instead of replacing it.
	for _, c := range s.Cases {
		// The case value becomes a scoreboard operand, so it must fit a score.
		//
		// The general literal check (driver.checkLiterals) walks the AST for
		// IntLit nodes, and a case value is a plain int on DecodeCase -- not an
		// IntLit -- so a value like 2147483648 reached the command unchanged and
		// 1.12.2 wrapped it silently.
		if !l.tgt.FitsScore(c.Val) {
			l.diags.Error(posOf(c.P), "E_INT_RANGE",
				"decode value %d is outside the %s score range [%d, %d]",
				c.Val, l.tgt.Name, l.tgt.ScoreMin, l.tgt.ScoreMax)
			continue
		}
		fb.Emit(cur, ir.Raw{
			Text:   fmt.Sprintf("scoreboard players %s @s %s %d %s", c.Op, dst.Obj, c.Val, c.NBT),
			Writes: []ir.Loc{dst},
			Reads:  []ir.Loc{dst},
		})
	}
}

// lowerEncode emits one encode statement.
func (l *Lowerer) lowerEncode(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.EncodeStmt) {
	fd, ok := l.fields[s.Field]
	if !ok {
		l.diags.Error(posOf(s.P), "E_FIELD_UNKNOWN", "no nbt field named %q", s.Field)
		return
	}
	if fd.Bits > 0 {
		if max := (1 << uint(fd.Bits)) - 1; s.Hi > max {
			l.diags.Error(posOf(s.P), "E_ENCODE_RANGE",
				"over %d..%d does not fit in %s, which holds 0..%d; the high values would be truncated silently",
				s.Lo, s.Hi, s.Field, max)
			return
		}
	}
	if !l.objDeclared(ctx, s.Source) {
		l.diags.Error(posOf(s.P), "E_OBJECTIVE_UNDECLARED",
			"objective %q is never declared; add `obj %s` or bind it with extern", s.Source, s.Source)
		return
	}
	src := l.targetLoc(ctx, s.Source, s.P)

	for v := s.Lo; v <= s.Hi; v++ {
		// Both bounds on the guard. `score_return=3` alone means `<= 3`, so
		// dropping the lower bound would fire this command for every value at
		// or below it -- the over-match that makes 1.12.2 selectors dangerous.
		//
		// The objective name is spliced in, not baked in: it is a location, and
		// a location written into command text cannot be renamed with the op
		// that reads it. Baking it here meant the objective was invisible to
		// usedObjectives, so it was never declared and 1.12.2 discarded every
		// one of these entitydata commands.
		fb.Emit(cur, ir.Raw{
			Text: fmt.Sprintf("entitydata @s[score_%%o_min=%d,score_%%o=%d] %s",
				v, v, nbtMatch(s.Container, 0, fd.Path, packBits(v, fd.Bits), true)),
			Args:  []ir.Loc{src, src},
			Reads: []ir.Loc{src},
		})
	}
}

// packBits renders v as the flat NBT bit map the decode ladder reads back.
//
// This is the encoder half of the codec and the reason the codec is worth
// having: the same layout is written out here and folded back in by the ladder,
// so the two cannot drift apart the way two hand-written files can.
func packBits(v, bits int) string {
	var parts []string
	for k := 0; k < bits; k++ {
		if v&(1<<uint(k)) != 0 {
			parts = append(parts, fmt.Sprintf("%d:1b", k))
		}
	}
	return strings.Join(parts, ",")
}

// lowerMatch lowers `<container> matches n"..."`.
//
// 1.12.2 has no way to put NBT in a selector, so the pattern cannot be tested
// where the branch needs it. The data pack's own answer, repeated 19 times, is
// `scoreboard players tag @s add <marker> {<nbt>}` -- the tag is added only if
// the pattern matches -- and then a tag filter does the branching. That is what
// this emits, with a compiler-owned marker name.
//
// The marker is removed before every probe. Without that, a test that matched
// once would keep matching after the item changed, because a tag written by an
// earlier evaluation is indistinguishable from one written now.
func (l *Lowerer) lowerMatch(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, mc syntax.MatchCond) (ir.Predicate, error) {
	c, sel := mc.Container, mc.Sel
	probe := fmt.Sprintf("ggm.probe%d", l.probeN)
	l.probeN++

	// The author's literal is a complete NBT value, not a fragment to be
	// wrapped: `n"{Item:{tag:{dat:{qlt:0b}}}}"` already names the entity key.
	// The container in `@s.Item matches` therefore only documents what is
	// being inspected; the pattern itself decides.
	_ = c
	fb.Emit(block, ir.Raw{Text: fmt.Sprintf("scoreboard players tag %s remove %s", sel, probe)})
	fb.Emit(block, ir.Raw{Text: fmt.Sprintf("scoreboard players tag %s add %s %s", sel, probe, mc.NBT)})
	return ir.Predicate{Exist: &ir.ExistTest{Selector: sel + "[tag=" + probe + "]"}}, nil
}

// objDeclared reports whether name is an objective the module knows about.
//
// Writing to an undeclared objective is the silent failure the codec is most
// exposed to: 1.12.2 neither errors nor takes effect, so the score stays 0 and
// every value read back is wrong with nothing to point at.
func (l *Lowerer) objDeclared(ctx *fnCtx, name string) bool {
	if l.objs[name] {
		return true
	}
	if xd, ok := l.externs[name]; ok && (xd.Kind == "var" || xd.Kind == "global") {
		return true
	}
	return ctx.isField(name)
}
