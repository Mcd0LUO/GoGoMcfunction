scoreboard players operation @s t0 = @s a0
scoreboard players operation @s t0 -= #4 cmcf.c
execute @s[score_t0=-1] ~ ~ ~ scoreboard players set @s return 1
execute @s[score_t0_min=0] ~ ~ ~ function luo:math/sqrt$b2
