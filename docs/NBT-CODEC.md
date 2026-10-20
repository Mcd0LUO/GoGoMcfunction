# NBT 位域编解码（取代数据表方案）

> 状态：**设计稿，未实现。** 2026-10-20 实测 + 作者 `py/` 生成器为参考实现。
> 本文**整个取代** `TABLE-DESIGN-v0.8.md`（数据表方案已撤）。

---

## 0. 一句话

**一份字段布局声明，编译器同时产出「读」（NBT → 分数）和「写」（值 → NBT）两个方向，并保证两半永远一致。**

---

## 0.1 为什么会有这个东西（作者原话）

> **「1.12.2 没办法动态修改玩家数据，所以我们想出了一个方法：将物品的数值，比如攻击力为 10，
> 硬编码为扁平的二进制展开，读取的时候就会很方便且很通用。且，也可方便修改——
> 将物品丢在地上变成物品实体，我们就可以修改它。」**

拆成三条硬事实，它们共同决定了设计形状：

| 事实 | 推论 |
|---|---|
| 1.12.2 **无法动态修改玩家背包里的物品数据** | 值不能在原地改，只能**重新给一件**（`give` / `entitydata`） |
| 数值必须**预先编码进物品 NBT** | 值在**作者期**就定死 → 编码必须发生在编译期，不能是运行期函数 |
| 物品丢地上 = 物品实体，`entitydata` 可改 | **写方向有了运行时载体**：`@s` 是那个物品实体 |

第三条是关键：正因为物品实体可写，才需要「值 → NBT」这一半；
而玩家背包不可写，所以写方向的语法**必须显式指明目标是物品实体**（`to @s`），
不能默认 `@s` 是玩家。**这条约束要写进语法，不能靠作者自觉。**

---

## 1. 先看例子（再看理由）

作者写：

```ggm
namespace rpg:api

// 一份布局：位宽 + 共享的 NBT 尾段
#[bits(9)]
field money = "dat.money"

on load {
    read money from "SelectedItem" -> return      // 玩家手持：SelectedItem.tag.dat.money
    read money from "Inventory.0"  -> wp.damage1  // 背包 0 号槽
}

on load {
    write money to @s range 0..511                // 写到物品实体：Item.tag.dat.money
}
```

编译器产出：

```mcfunction
// ── 读：9 条梯子 ──────────────────────────────
scoreboard players set @s return 0
scoreboard players add @s return 1 {SelectedItem:{tag:{dat:{money:{0:1b}}}}}
scoreboard players add @s return 2 {SelectedItem:{tag:{dat:{money:{1:1b}}}}}
… 直到 256

// ── 写：512 条值分派 ──────────────────────────
entitydata @s[score_return_min=0,score_return=0] {Item:{tag:{dat:{money:{}}}}}
entitydata @s[score_return_min=1,score_return=1] {Item:{tag:{dat:{money:{0:1b}}}}}
entitydata @s[score_return_min=2,score_return=2] {Item:{tag:{dat:{money:{1:1b}}}}}
entitydata @s[score_return_min=3,score_return=3] {Item:{tag:{dat:{money:{0:1b,1:1b}}}}}
…
```

注意写方向的守卫是 **`score_return_min=v,score_return=v`（两个界都给）**。
只写 `score_return=v` 意为 `return <= v`，会**静默多匹配**——这正是本项目的存在理由。
作者自己的 `py` 写对了这一点（`idt_generater.py:149`），手写的人不会。

---

## 2. 为什么是它（实测）

```powershell
$root = 'D:\game\PCL2\.minecraft\versions\1.12.2\saves\末法\data\functions\rpg'
```

| 项 | 实测 |
|---|---|
| `rpg/` 下 `.mcfunction` | 668 |
| **NBT → 分数** 的行 | **452**（`add` 扁平梯子 304 + `add` 两级嵌套 60 + `set` 枚举 88） |
| 涉及文件 | **48** |
| **值 → NBT** 的手写位图（`give … {N:1b,…}`） | **59** 条（`give` 共 85 条） |
| source 前缀种类 | `SelectedItem` / `Item` / `Inventory:[{Slot:nb,…}]` / `+Tags:[…]` |
| holder 不是 `@s` 的行 | **0** |

同一份布局**手写在两个地方**：读的梯子、写的位图。
证据（`item/cl/1c/残照花魂的记忆.mcfunction`）：

```mcfunction
give @s end_crystal 1 0 {…Lore:[…"§7500"…],…,dat:{money:{2:1b,4:1b,5:1b,6:1b,7:1b,8:1b}}}
```

`4+16+32+64+128+256 = 500`，Lore 里手写着 `500`。**同一个数写了两遍，写错没有任何报错。**

---

## 3. 为什么不是「标准库」

三样东西必须**编译期**定死：NBT 路径文本、位下标 `k`、命令条数 `N`（= 位宽）。而：

- 1.12.2 **没有动态 NBT 路径**——路径就是命令字面量
- 1.12.2 **没有动态函数名**
- GGM 的 `fn` 是**固定代码**，运行期函数发不出「条数可变、文本各异」的命令

所以只能是**编译期展开**，即语言构造。stdlib 在当前语言能力下表达不了。

---

## 4. 参考实现：作者的 `py/`（已实测）

| 文件 | 作用 | 关键代码 |
|---|---|---|
| `py/utils/nbt.py:1` | **值 → 位图** | `format_num2nbt(num)`：`bin(num)[2:][::-1]`，第 i 位为 1 就追加 `f"{i}:1b"` |
| `py/utils/nbt.py:27` | 位权列表 | `generate_bin_list(length)` → `[1,2,4,…,2^length]` |
| `py/read/ApLoader_common.py` | **读梯子生成** | `set 0` + 对每个位权发一条 `add` |
| `py/read/idt_generater.py:145` | **写值分派** | `entitydata @s[score_return_min=v,score_return=v] {Item:{tag:{dat:{F:{bits}}}}}` |
| `py/equip/generate.py:138` | **作者期编码** | 从 JSON 生成 `give`，位图全由 `format_num2nbt` 算出 |

`format_num2nbt` 的两个行为要照抄并保留语义：

- **负数返回空串**（`nbt.py:9-10`）——不是报错，是空位图
- `full=True` 时零位也写 `f"{i}:0b"`（补全）；读梯子用的是 `False`

> **注意**：`py/` 是**思想的出处，不是逐字规格**。已经看到它落后于产物：
> `ApLoader_common.py:39` 把 `Slot:0b` **硬编码**，而 `Ap.slot` 字段声明了却没用；
> 该文件只定义了 `cooldown` 一个字段，实际的 `damage/pen_base/passive` 产物比它新。

---

## 5. 参数

`field` 声明：

| 参数 | 例 | 说明 |
|---|---|---|
| `bits` | `9` | 位宽。`bits(9)` 覆盖 `0..2^9-1` |
| 尾段 | `"dat.money"` | 读写**共享**的那一段路径 |

`read`：`from`（前缀）+ `->`（目标 objective）。
前缀实测四类：`SelectedItem`、`Item`、`Inventory.<n>`、以及附加谓词（`Tags:["upable"]`）。

`write`：`to`（目标，通常是丢在地上的物品实体 `@s`）+ `range`（值域，决定发射多少条）。

---

## 6. 诊断码

| 码 | 检查 |
|---|---|
| `E_FIELD_BITS` | `bits` 非 1..30（`bits=31` 时最高位 `2^30` 仍在 int32 内，`32` 越界） |
| `E_FIELD_PATH` | 尾段为空、或含非法字符 |
| `E_FIELD_UNKNOWN` | `read`/`write` 引用了未声明的 `field` |
| `E_WRITE_RANGE` | `range` 上界 ≥ `2^bits`（该值在这个位宽里放不下，会**静默截断**） |
| `E_FIELD_COLLISION` | 两个 `field` 落在同一个 objective（复用既有检查） |
| `W_FIELD_UNUSED` | 声明了但既没 `read` 也没 `write` |

`E_WRITE_RANGE` 是最有价值的一条：`format_num2nbt(300)` 会产生 9 位，
但字段只有 8 位时**读回来是另一个数**，`py` 那边不会报错。

---

## 7. 验收

| # | 标准 | 判定 |
|---|---|---|
| 1 | 读：`ApLoader` 19 个文件的解码行为正确 | 喂 `gomc.exe`，比对 objective 与 Go 里独立算的值 |
| 2 | 写：`idt_generater` 的 `entitydata` 行为正确 | 同上，反方向 |
| 3 | 位宽边界 | 全 0 / 全 1 / 每个 k 单独置位，共 `bits+2` 用例 |
| 4 | **往返一致** | `write(v)` 之后再 `read` 必须还原出 `v`，对 `v ∈ [0, 2^bits)` 全覆盖 |
| 5 | 命令数不劣于手写 | 读 `bits+1` 条；写 `2^bits` 条（手写就是这么多） |

第 4 条是这个特性**唯一独有的**保证：手写时两半分别维护，没有任何机制阻止它们分叉。

---

## 8. 未定 / 未验证

**要你拍板**：

1. `field` 是顶层声明，还是可以放在 `class` 里（物品类的字段）？
2. `write` 的 `range` 上界要不要能写成 `2^bits-1` 自动推？（`range 0..511` 里的 511 是冗余信息）
3. 前缀 `Inventory.0` 的槽位是**数字**还是**字符串**？实测里槽位跨越 0..103（`defence` 用 100..103）。

**未验证**：

- 我只数了 `rpg/`。`data/` 下 1963 个文件里还有多少同类模式**没数**。
- 写方向我只验了 3 个位图样本（500 / 125 / 3000 待核）。
- `read` 的附加谓词（`Tags:["upable"]`）只在 1 个字段上见过，是否值得进语法**未定**。
