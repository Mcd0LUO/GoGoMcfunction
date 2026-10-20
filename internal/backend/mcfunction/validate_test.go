package mcfunction

import "testing"

// Validate is the last gate before a file is written. A selector argument
// the command does not recognise makes the whole file fail to load, so the
// mistake has to be caught here rather than discovered on a server.
func TestValidateRejectsOperatorSelectors(t *testing.T) {
	bad := []string{
		"execute @a[score_x>=1] ~ ~ ~ say hi",
		"execute @s[score_x<=1] ~ ~ ~ function t:f",
		"function t:f if @s[score_x==0]",
		"scoreboard players set @a[nbt={}] obj 1",
	}
	for _, line := range bad {
		if err := Validate(line); err == nil {
			t.Errorf("Validate accepted %q", line)
		}
	}
	good := []string{
		"execute @s[score_x_min=1] ~ ~ ~ function t:f",
		"function t:f if @s[score_x_min=0,score_x=0]",
		"function t:f unless @s[score_l12=0]",
		"execute @e[type=zombie,tag=mine] ~ ~ ~ function t:g",
		"execute @e[tag=a,tag=!b] ~ ~ ~ function t:g",
		"scoreboard players operation @s t0 = @s a0",
		"say hi",
		// An NBT argument is not a selector filter. Nothing here is a selector
		// argument, and treating the '[' of {Inventory:[...]} as one rejected
		// every decode ladder the NBT codec emits.
		"scoreboard players add @s wp.damage1 1 {Inventory:[{Slot:0b,tag:{dat:{atk_base:{0:1b}}}}]}",
		"scoreboard players set @s wp.type1 1 {SelectedItem:{tag:{dat:{spec:1b}}}}",
		"scoreboard players set @s temp3 1 {SelectedItem:{tag:{Tags:[\"残响\"]}}}",
		"entitydata @s[score_return_min=3,score_return=3] {Item:{tag:{dat:{money:{0:1b,1:1b}}}}}",
	}
	for _, line := range good {
		if err := Validate(line); err != nil {
			t.Errorf("Validate rejected %q: %v", line, err)
		}
	}
}
