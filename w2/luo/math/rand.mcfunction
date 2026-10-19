scoreboard players operation @s t0 = @s a1
scoreboard players operation @s t0 -= @s a0
scoreboard players operation @s l0 = @s t0
scoreboard players operation @s l0 += #1 cmcf.c
function luo:math/lcg_next
scoreboard players operation @s t0 = @s return
scoreboard players operation @s l1 = @s t0
scoreboard players operation @s return = @s a0
scoreboard players operation @s t0 = @s l1
scoreboard players operation @s t0 %= @s l0
scoreboard players operation @s t1 = @s t0
scoreboard players operation @s t1 += @s l0
scoreboard players operation @s t1 %= @s l0
scoreboard players operation @s return += @s t1
