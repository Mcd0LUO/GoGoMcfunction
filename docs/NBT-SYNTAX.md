# NBT 编解码语法设计（深设计稿）

> 2026-10-20。取代 `NBT-CODEC.md` 与 `NBT-CODEC-CASES.md`。
> 本文先立**心智模型**，语法从模型推出来；每条决定都附实测数字。

---

# 一、心智模型（先立这个）

## 1.1 NBT 是谓词，不是数据源

```mcfunction
scoreboard players add @s wp.damage1 1 {Inventory:[{Slot:0b,tag:{dat:{atk_base:{0:1b}}}}]}
```

这条命令的语义**不是**「读取 atk_base」，而是：

> **如果** @s 的 NBT 匹配这个模式，**就**加 1。

「读」是**对 w 个谓词做加权折叠**，权重 2^k。这解释了两件事：
为什么第一条必须是 `set 0`（先清零），以及为什么它天然是 O(w) 条命令。

## 1.2 encode 是值分派，不是构造

```mcfunction
entitydata @s[score_return_min=3,score_return=3] {Item:{tag:{dat:{Atk_base:{0:1b,1:1b}}}}}
```

1.12.2 **无法用分数构造 NBT**。这条命令的 NBT 是**常量文本**，
`score_return=3` 只决定「发射哪一条」。值分派 = 值域里每个值一条命令。

## 1.3 两条方向的成本来源不同 → 语法必须体现

| 方向 | 命令数 | 由谁决定 |
|---|---|---|
| decode | `1 + 宽度` | **字段**（宽度是编码事实） |
| encode | `值域大小` | **使用点**（作者选多大范围） |

**这是整个设计的不对称点。** 把它们藏进同一个 `=`，读者会以为两者同价——
而实测两者相差 **200 倍**。

---

# 二、实测约束

```powershell
$p = 'D:\game\PCL2\.minecraft\versions\1.12.2\saves\末法\data\functions'
```

| 事实 | 数字 | 出处 |
|---|---|---|
| 字段宽度 | 9 / 10 / 11 / 14 位 | `ApLoader/weapon/*`、`Bind/uid/read` |
| **真实 encode 值域** | **11 ~ 12 条** | `identify/**/*.mcfunction` 的 `entitydata` 计数 |
| 整包最大单文件 | **210 条** | `particle/character/柒咕咕/left_obj_*.mcfunction` |
| 嵌套下标取值 | **只有 0 和 1** | `weapon/passive/{N}_{1,2}` |
| 递增值域里没有 | 没有任何文件做过 `2^bits` 展开 | 全包扫描 |

**三条推论：**

1. **encode 的值域永远很小**，不需要 `2^bits` 展开。值域是作者在使用点的选择。
2. **嵌套下标只有 0/1** → **不需要参数化路径**，写两个常量即可。省掉一整块插值语法。
3. 命令数上限 210 → 目前没有 `maxCommandChainLength` 风险，但要防作者写出大值域。

---

# 三、语法全貌

## 3.1 路径指针 `p`

```ggm
const atk_base = p"dat.atk_base" : bits 11
const pen_base = p"dat.pen_base" : bits 11
const cooldown = p"dat.cooldown" : bits 9
const passive0 = p"dat.passive.0" : bits 10
const passive1 = p"dat.passive.1" : bits 10
const spec     = p"dat.spec"     : enum 1..4
const quality  = p"tag.Tags"     : enum { "残响":1, "蚀刻":2, "回响":3, "星烬":4, "窄门":5 }
```

- `p"…"` 是**字面量**，和 `n"…"` 同族：`n` 是 NBT 值，`p` 是 NBT 路径。
- 路径是**完整的、从实体根起算的**（`dat.atk_base` 就是 `{dat:{atk_base:…}}`）。
  不把 `dat` 设成隐式根——显式，且以后有 `dat` 之外的数据也照样能用。
- 冒号后面是**编码方式**，只有三种：

| 写法 | 含义 | decode 生成 |
|---|---|---|
| `bits 11` | 位折叠 | `set 0` + 11 条 `add 2^k` |
| `enum 1..4` | 恒等值分派 | `set 0` + 4 条 `set v` |
| `enum {…}` | 映射值分派（数字或字符串） | `set 0` + N 条 `set v` |

`enum { "残响":1, … }` 让**标签枚举**也进来了：值分派发射
`{SelectedItem:{tag:{Tags:["残响"]}}}`。这是之前我判定「盖不住」的形状之一，现在盖住了。

## 3.2 容器

`@s` 是选择器；容器用 **1.12.2 自己的 NBT 键名**，零造词：

| 写法 | 生成的 NBT 前缀 | 适用 |
|---|---|---|
| `@s.SelectedItem` | `{SelectedItem:{tag:{…}}}` | 玩家手持 |
| `@s.Inventory(0)` | `{Inventory:[{Slot:0b,tag:{…}}]}` | 玩家背包槽 |
| `@s.Inventory(100..103)` | 四个槽位 | 多槽位（一次清零） |
| `@s.Item` | `{Item:{tag:{…}}}` | 物品实体 |

## 3.3 decode —— NBT 谓词折叠 → 分数

```ggm
on load {
    decode wp.damage1 from @s.Inventory(0)[atk_base]
    decode wp.damage2 from @s.Inventory(1)[atk_base]
    decode wp.damage3 from @s.Inventory(2)[atk_base]

    decode return from @s.SelectedItem[money]      // 手持金钱

    decode Eq.Defence from @s.Inventory(100..103)[defence]   // 一次清零，44 条 add

    decode wp.passiveId1:1 from @s.Inventory(0)[passive0]
    decode wp.type1        from @s.Inventory(0)[spec]
    decode temp3           from @s.SelectedItem[quality]
    decode temp2           from @s.Item[bind]      // 物品实体
}
```

成本 `1 + 宽度`，**不需要在语法里写**——宽度已经在字段里，没有第二个决定。

## 3.4 encode —— 分数 → NBT 值分派

```ggm
on load {
    encode @s.Item[money] = return over 1..10
}
```

**`over` 是必须的**，因为值域是使用点的决定：

- 字段 `money` 是 14 位（decode 要 16384 个值）
- 但这一轮只掷出 1..10 → **只发 10 条** `entitydata`

实测就是这么干的（`idt_generater.py:146`：`for value in range(min_val, max_val + 1)`）。
`over` 让**成本写在脸上**：作者看到 `over 1..500` 就知道那是 500 条命令。

## 3.5 测试 —— 一条命令

```ggm
on load {
    if @s.SelectedItem has n"{tag:{Tags:[\"upable\"]}}" { … }
}
```

NBT 值用 `n"…"` 字面量，成本恒为 1 条。

---

# 四、为什么这样切（对照被否掉的方案）

| 方案 | 否决理由 |
|---|---|
| 一个 `=` 管两个方向 | 把 `1+w` 和 `值域大小` 藏成同价，读者以为对称 |
| `nbt` 新声明关键字 | 根路径被烤进每条声明；且它和 `const` 无区别 |
| `slot(100..103).defence` | 列表属性读法；真实语义是「一次清零 + 逐槽累加」 |
| 参数化路径 `p"…{$i}…"` | **实测下标只有 0/1**，两个常量就够（先数再设计） |
| 构造函数 `nbt(list,[…])` | 要给 GGM 加 5 类 NBT token + 两处语法冲突；且会和 SNBT 分叉 |
| 把 `dat` 设成隐式根 | 省 4 个字符，换来一个看不见的全局状态 |

---

# 五、待定

1. **`: bits 11` 还是 `: 0..2047`？**
   前者是编码视角（作者编码时就想位），后者是取值视角（读者想知道范围）。
   我倾向 `bits 11`：decode 的展开直接由它决定，且 `11` 比 `2047` 不容易抄错。

2. **`@s.Inventory(0)` 里的 `0` 是槽位号，不是列表下标**（NBT 的 `Inventory` 是列表，
   槽位靠 `Slot:0b` 匹配）。这个简写要确认不会误导。

3. **encode 的值域上界要不要能推**？例如 `over 1..10` 里 10 是作者决定的；
   若写成 `over full` 就是 `0..2^bits-1`（14 位 = 16384 条，危险）。
   我倾向**不允许 `full`**，强制写数字。

4. **哨兵位**（`{lv:{max:1b}}` → `-1`）仍然不在模型里。用普通代码：
   ```ggm
   decode temp2 from @s.SelectedItem[lv]
   if @s.SelectedItem has n"{tag:{dat:{lv:{max:1b}}}}" { temp2 = -1 }
   ```
