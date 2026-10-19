scoreboard players operation @s t0 = @s a0
scoreboard players operation @s a0 = #1 cmcf.c
scoreboard players operation @s a1 = @s t0
function luo:math/rand
scoreboard players operation @s t0 = @s return
scoreboard players operation @s t1 = @s t0
scoreboard players operation @s t1 -= @s a1
scoreboard players set @s t0 0
execute @s[score_t1=0] ~ ~ ~ scoreboard players set @s t0 1
scoreboard players operation @s return = @s t0
