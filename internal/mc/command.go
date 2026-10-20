package mc

import "strings"

// Command is a printable 1.12.2 command. We model commands structurally rather
// than concatenating strings so that the emitter can validate and so golden
// output stays byte-deterministic.
type Command interface {
	Line() string
}

// Raw is an escape hatch for commands we do not model yet.
type Raw struct{ Text string }

func (r Raw) Line() string { return r.Text }

// ScoreboardOp is `scoreboard players operation <target> <obj> <op> <source> <srcObj>`.
type ScoreboardOp struct {
	Target    string
	Objective string
	Op        string // += -= *= /= %= = < > <=
	Source    string
	SrcObj    string
}

func (c ScoreboardOp) Line() string {
	return "scoreboard players operation " + c.Target + " " + c.Objective +
		" " + c.Op + " " + c.Source + " " + c.SrcObj
}

// ScoreboardSet is `scoreboard players set <target> <obj> <value>`.
type ScoreboardSet struct {
	Target    string
	Objective string
	Value     int
}

func (c ScoreboardSet) Line() string {
	return "scoreboard players set " + c.Target + " " + c.Objective + " " + itoa(c.Value)
}

// ScoreboardAdd is `scoreboard players add <target> <obj> <value>`.
type ScoreboardAdd struct {
	Target    string
	Objective string
	Value     int
}

func (c ScoreboardAdd) Line() string {
	return "scoreboard players add " + c.Target + " " + c.Objective + " " + itoa(c.Value)
}

// ScoreboardRemove is `scoreboard players remove <target> <obj> <value>`.
type ScoreboardRemove struct {
	Target    string
	Objective string
	Value     int
}

func (c ScoreboardRemove) Line() string {
	return "scoreboard players remove " + c.Target + " " + c.Objective + " " + itoa(c.Value)
}

// ScoreboardReset is `scoreboard players reset <target> [obj]`.
type ScoreboardReset struct {
	Target    string
	Objective string // empty means all objectives
}

func (c ScoreboardReset) Line() string {
	if c.Objective == "" {
		return "scoreboard players reset " + c.Target
	}
	return "scoreboard players reset " + c.Target + " " + c.Objective
}

// ObjectiveAdd is `scoreboard objectives add <name> <criteria> [display]`.
//
// Criteria is empty for the common case, which is `dummy`. It is not always
// dummy: `stat.damageDealt` and friends track a statistic instead of being
// written by hand, and 1.12.2 rejects an unknown criteria by refusing the
// whole command.
type ObjectiveAdd struct {
	Name     string
	Criteria string
	Display  string
}

func (c ObjectiveAdd) Line() string {
	crit := c.Criteria
	if crit == "" {
		crit = "dummy"
	}
	s := "scoreboard objectives add " + c.Name + " " + crit
	if c.Display != "" {
		s += " " + c.Display
	}
	return s
}

// Execute is `execute <selector> <x> <y> <z> <command>`.
//
// 1.12.2 has NO `execute if/unless ... run`. The only conditional execution is
// "filter targets with a selector, then run on each of them".
type Execute struct {
	Selector *Selector
	X, Y, Z  string // typically "~" "~" "~"
	Cmd      Command
}

func (c Execute) Line() string {
	x, y, z := c.X, c.Y, c.Z
	if x == "" {
		x, y, z = "~", "~", "~"
	}
	return "execute " + c.Selector.String() + " " + x + " " + y + " " + z + " " + c.Cmd.Line()
}

// Function is `function <name> [if <selector> | unless <selector>]`.
//
// Semantics note: `function F if @e[tag=x]` is an EXISTENCE test; F runs once.
// `execute @e[tag=x] ~ ~ ~ function F` runs F once PER matching entity.
// They are not interchangeable.
type Function struct {
	Name   string // "namespace:path"
	Cond   *Selector
	Negate bool // emit `unless` instead of `if`
}

func (c Function) Line() string {
	s := "function " + c.Name
	if c.Cond != nil {
		if c.Negate {
			s += " unless "
		} else {
			s += " if "
		}
		s += c.Cond.String()
	}
	return s
}

// Kill is `kill <selector>`.
type Kill struct{ Selector *Selector }

func (c Kill) Line() string { return "kill " + c.Selector.String() }

// Tag is `tag <selector> add|remove <name>`.
type Tag struct {
	Selector *Selector
	Action   string // add | remove
	Name     string
}

func (c Tag) Line() string {
	return "tag " + c.Selector.String() + " " + c.Action + " " + c.Name
}

// Say is `say <text>`.
type Say struct{ Text string }

func (c Say) Line() string { return "say " + c.Text }

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// File is a rendered .mcfunction: a path plus its command lines.
type File struct {
	Path string
	Cmds []Command
}

func (f *File) Render() string {
	var sb strings.Builder
	for _, c := range f.Cmds {
		sb.WriteString(c.Line())
		sb.WriteByte('\n')
	}
	return sb.String()
}
