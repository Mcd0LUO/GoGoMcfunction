scoreboard players operation @s a0 = seed math
function luo:math/lcg_step
scoreboard players operation @s t0 = @s return
scoreboard players operation @s l0 = @s t0
scoreboard players operation @s t0 = seed math
scoreboard players operation @s t0 += #1234 cmcf.c
scoreboard players operation seed math = @s t0
scoreboard players operation @s return = @s l0
