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

// fieldPathNBT renders a dotted path as the opening and closing braces of the
// nested compounds it denotes. `dat.money` is `dat:{` `money:{` ... `}` `}`.
func fieldPathNBT(path string) (open, close string) {
	var o, c strings.Builder
	for _, seg := range strings.Split(path, ".") {
		o.WriteString(seg + ":{")
		c.WriteString("}")
	}
	return o.String(), c.String()
}

// nbtMatch renders a complete NBT argument that matches one probe.
//
// body is what sits innermost: `0:1b` for a bit, `1b` for an enumerated
// byte, `["残响"]` for a tag. Every brace is balanced here rather than at the
// call site, because an unbalanced one produces a command the backend rejects
// at the very end of the pipeline (E_BAD_COMMAND) rather than at the point of
// the mistake.
func nbtMatch(c syntax.Container, slot int, path, body string) string {
	open, close := fieldPathNBT(path)
	inner := open + body + close
	switch c.Key {
	case "Inventory":
		return fmt.Sprintf("{Inventory:[{Slot:%db,tag:{%s}}]}", slot, inner)
	case "Item":
		return fmt.Sprintf("{Item:{tag:{%s}}}", inner)
	default:
		return fmt.Sprintf("{SelectedItem:{tag:{%s}}}", inner)
	}
}

// enumKeyBody renders an enum key as the NBT body it matches.
//
// A numeric key is a byte tag (`spec:1b`). Anything else is a string inside a
// list (`Tags:["残响"]`), which is how 1.12.2 stores scoreboard tags.
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
	dst := l.targetLoc(ctx, s.Target)

	// One reset for the whole statement, then every slot's rungs. Resetting per
	// slot would leave only the last slot's contribution, which is exactly what
	// the hand-written defence file avoids by resetting once for all four.
	fb.Emit(cur, ir.Set{Dst: dst, Value: 0})

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
				fb.Emit(cur, ir.Raw{Text: fmt.Sprintf("scoreboard players add @s %s %d %s",
					dst.Obj, 1<<uint(k), nbtMatch(s.Container, slot, fd.Path, fmt.Sprintf("%d:1b", k)))})
			}
		}
	} else {
		for _, ec := range fd.Enum {
			for _, slot := range slots {
				fb.Emit(cur, ir.Raw{Text: fmt.Sprintf("scoreboard players set @s %s %d %s",
					dst.Obj, ec.Value, nbtMatch(s.Container, slot, fd.Path, enumKeyBody(ec.Key)))})
			}
		}
	}

	// Author-written cases run last so a sentinel can override the field's own
	// value. `set` overwrites and `add` accumulates, exactly as the two
	// scoreboard commands do -- a sentinel must use `set`, or it would be added
	// to the ladder's result instead of replacing it.
	for _, c := range s.Cases {
		fb.Emit(cur, ir.Raw{Text: fmt.Sprintf("scoreboard players %s @s %s %d %s",
			c.Op, dst.Obj, c.Val, c.NBT)})
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
	src := l.targetLoc(ctx, s.Source)

	for v := s.Lo; v <= s.Hi; v++ {
		// Both bounds on the guard. `score_return=3` alone means `<= 3`, so
		// dropping the lower bound would fire this command for every value at
		// or below it -- the over-match that makes 1.12.2 selectors dangerous.
		guard := fmt.Sprintf("@s[score_%s_min=%d,score_%s=%d]", src.Obj, v, src.Obj, v)
		fb.Emit(cur, ir.Raw{Text: fmt.Sprintf("entitydata %s %s",
			guard, nbtMatch(s.Container, 0, fd.Path, packBits(v, fd.Bits)))})
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
