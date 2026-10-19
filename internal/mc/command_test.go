package mc

import "testing"

func TestCommandLines(t *testing.T) {
	exact0, _ := Exact(0)
	lt5, _ := LessThan(5)

	cases := []struct {
		cmd  Command
		want string
	}{
		{
			Execute{Selector: SelfSelector().Arg("score_hp=-1"), Cmd: Function{Name: "ns:then"}},
			"execute @s[score_hp=-1] ~ ~ ~ function ns:then",
		},
		{
			Function{Name: "ns:loop", Cond: SelfSelector().Score("n", exact0), Negate: true},
			"function ns:loop unless @s[score_n_min=0,score_n=0]",
		},
		{
			ScoreboardOp{Target: "@s", Objective: "hp", Op: "-=", Source: "@s", SrcObj: "dmg"},
			"scoreboard players operation @s hp -= @s dmg",
		},
		{
			ScoreboardOp{Target: "@s", Objective: "x", Op: "<", Source: "@s", SrcObj: "y"},
			"scoreboard players operation @s x < @s y",
		},
		{
			ScoreboardSet{Target: "#math.4", Objective: "math", Value: 4},
			"scoreboard players set #math.4 math 4",
		},
		{
			ScoreboardSet{Target: "#math.MIN", Objective: "math", Value: -2147483647},
			"scoreboard players set #math.MIN math -2147483647",
		},
		{
			ScoreboardReset{Target: "@s", Objective: "temp1"},
			"scoreboard players reset @s temp1",
		},
		{
			ScoreboardReset{Target: "@a"},
			"scoreboard players reset @a",
		},
		{
			ObjectiveAdd{Name: "math", Display: "常量"},
			"scoreboard objectives add math dummy 常量",
		},
		{
			Tag{Selector: SelfSelector(), Action: "remove", Name: "math.sort"},
			"tag @s remove math.sort",
		},
		{
			Kill{Selector: SelfSelector()},
			"kill @s",
		},
	}

	for _, c := range cases {
		if got := c.cmd.Line(); got != c.want {
			t.Errorf("\n got: %s\nwant: %s", got, c.want)
		}
	}

	_ = lt5
}

func TestItoa(t *testing.T) {
	cases := map[int]string{
		0:            "0",
		7:            "7",
		-7:           "-7",
		1000:         "1000",
		-2147483647:  "-2147483647",
		2147483647:   "2147483647",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}