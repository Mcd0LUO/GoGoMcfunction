scoreboard players operation @s t19 = #2147483647 cmcf.c
scoreboard players operation @s t19 < @a[tag=math.sort] math
scoreboard players operation @s l2 = @s t19
scoreboard players operation @a[tag=math.sort] temp1 = @s l2
scoreboard players operation @s l0 += #1 cmcf.c
scoreboard players remove @s l1 1
execute @s[score_l1_min=1] ~ ~ ~ function luo:math/sort_asc$b1
