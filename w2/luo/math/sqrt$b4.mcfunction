scoreboard players operation @s l1 = @s l0
scoreboard players operation @s t1 = @s a0
scoreboard players operation @s t1 /= @s l0
scoreboard players operation @s t2 = @s l0
scoreboard players operation @s t2 += @s t1
scoreboard players operation @s l0 = @s t2
scoreboard players operation @s l0 /= #2 cmcf.c
scoreboard players remove @s l2 1
execute @s[score_l2_min=1] ~ ~ ~ function luo:math/sqrt$b3
execute @s[score_l2=0] ~ ~ ~ function luo:math/sqrt$b5
