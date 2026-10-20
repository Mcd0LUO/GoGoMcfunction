# NBT 编解码 —— 真用例集（供语法 review）

> 每个用例都来自 `末法` 的真实文件，并核对了生成行数。
> 语法：`nbt <名字> = <值域> @ "<NBT 路径>"`；读写都是赋值，方向由 NBT 在哪一侧决定。

---

## 用例 1 — 武器攻击力（背包槽位，位梯子）

源：`rpg/api/ApLoader/weapon/damage/1.mcfunction`（12 行）

```ggm
namespace rpg:api

nbt atk_base = 0..2047 @ "tag.dat.atk_base"

on load {
    wp.damage1 = @s.slot(0).atk_base
}
```

生成 12 行：`set 0` + 11 条 `add`，与手写文件逐字一致。

---

## 用例 2 — 三把武器，一个声明

源：`damage/{1,2,3}.mcfunction`

```ggm
on load {
    wp.damage1 = @s.slot(0).atk_base
    wp.damage2 = @s.slot(1).atk_base
    wp.damage3 = @s.slot(2).atk_base
}
```

---

## 用例 3 — 护甲：4 个槽位累加到同一 objective

源：`api/ApLoader/equip/defence.mcfunction`（45 行 = 1 条 set + 44 条 add，**只 set 一次**）

```ggm
nbt defence = 0..2047 @ "tag.dat.defence"

on load {
    Eq.Defence = @s.slot(100..103).defence     // 槽位区间
}
```

**不能用 4 条赋值**——每条读都会先 `set 0`，后一条会抹掉前一条。
槽位区间让编译器只发一条 `set 0`、再发 44 条 `add`，与手写形状一致。

---

## 用例 4 — 武器类型（枚举，不是位梯子）

源：`weapon/type/1.mcfunction`（5 行）

```ggm
nbt spec = 1..4 @ "tag.dat.spec"

on load {
    wp.type1 = @s.slot(0).spec
}
```

值域是 `1..4`（不是 `0..2^n-1`），编译器自动选**值分派**：`set 0` + 4 条 `set v`。

---

## 用例 5 — 手持物品金钱（14 位）

源：`api/get/GetMaterialMoney.mcfunction`（18 行，其中 4 行被注释掉 = 预留到 18 位）

```ggm
nbt money = 0..16383 @ "tag.dat.money"

on load {
    return = @s.held.money
}
```

---

## 用例 6 — 手持物品数量（路径没有 `tag`/`dat` 两层）

源：`api/get/GetSelectItemCount.mcfunction`（65 行 = 1 + 64）

```ggm
nbt count = 1..64 @ "Count"        // 路径就是 Count，不套 tag/dat

on load {
    return = @s.held.count
}
```

生成 `{SelectedItem:{Count:Nb}}`，N = 1..64。

---

## 用例 7 — 物品实体上的 UID（14 位）

源：`api/Bind/uid/read.mcfunction`（15 行）

```ggm
nbt bind = 0..16383 @ "tag.dat.bind"

on load {
    temp2 = @s.item.bind           // @s 是物品实体，不是玩家
}
```

---

## 用例 8 — 升级触发：源上还带一个标签谓词

源：`talk/器灵祭坛神授/武器升级/trigger.mcfunction`

```ggm
nbt id      = 0..63 @ "tag.dat.id"
nbt up_need = 0..15 @ "tag.dat.up_need"

on load {
    temp1 = @s.held.id        where Tags has "upable"
    arg1  = @s.held.up_need   where Tags has "upable"
}
```

生成 `{SelectedItem:{tag:{Tags:["upable"],dat:{id:{k:1b}}}}}`。
`where` 是我造的新词，见文末「未覆盖」。

---

## 用例 9 — 写方向：鉴定属性回写物品实体

源：`identify/legacy/identify/残响/Atk_base.mcfunction`，由 `py/read/idt_generater.py:145` 生成

```ggm
nbt Atk_base = 0..2047 @ "tag.dat.Atk_base"

on load {
    @s.item.Atk_base = return over 1..10
}
```

**注意 `over 1..10`**：字段能存 11 位（读要 2048 个值），但这一轮只掷出 1..10，
所以只发 10 条 `entitydata`。**写值域必须独立于读值域**——这点我上一版说「值域不用写」是错的。

---

## 用例 10 — 作者期：把值编成 `give` 的位图

源：`item/cl/1c/残照花魂的记忆.mcfunction`（`money` 位图 = 500，Lore 也写 500）

```ggm
const MONEY = pack(money, 500)      // 编译期 = "2:1b,4:1b,5:1b,6:1b,7:1b,8:1b"

comptime {
    emit "give @s end_crystal 1 0 {display:{Lore:[\"§7…§7500…\"]},dat:{money:{" + MONEY + "}}}"
}
```

---

# 未覆盖的形状（诚实列出）

| # | 形状 | 真实出处 | 现状 |
|---|---|---|---|
| 1 | **哨兵标志**：`{lv:{max:1b}}` → 分数 `-1` | `trigger.mcfunction:15` | `lv` 值域 `0..7` 表达不了「另有一个位把结果钉成 -1」 |
| 2 | **字符串标签枚举**：`{Tags:["残响"]}` → 1..5 | `trigger.mcfunction:18-22` | 来源是**标签名**不是数字路径，`nbt` 抽象盖不住 |
| 3 | **源上的谓词**：`Tags:["upable"]` | 用例 8 | 我用 `where` 硬造了一个词 |
| 4 | **写值域窄于字段** | 用例 9 | 已用 `over 1..10` 表达，但这词也是我造的 |
| 5 | **多槽位累加** | 用例 3 | 已用 `slot(100..103)` 解决 |

1 和 2 我**没有**好设计。它们不是同一个抽象能顺手带上的：
哨兵标志是「一个布尔位改变整个字段的语义」，标签枚举是「来源根本不是数字」。
