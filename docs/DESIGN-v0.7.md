# GGM 设计稿：数据表 · 影子实体 · 值域分析

> 状态：**设计稿，未实现。** 三块彼此独立，可分别落地。
> 每条设计都标了证据来源：要么来自 `末法` 的真实代码，要么来自 1.12.2 的字节码与语言文件。
> 没有证据的地方写成「待验证」，不写成结论。

---

# 一、数据表

## 1.1 先纠正一个判断：这里的「表」不是 N 行 × M 列

我上一轮数了「结构相同的文件」，结论是只有 2 处。那个统计**数错了对象**。

真正在跑的表有 6 张（`ApLoader/weapon/` 下 5 张 + `ApLoader/equip/defence`），
它们全部是同**一个模板**的 6 次实例化，而不是 6 组不同的数据行：

```mcfunction
# weapon/damage/1.mcfunction —— 完整内容
scoreboard players set @s wp.damage1 0
scoreboard players add @s wp.damage1 1   {Inventory:[{Slot:0b,tag:{dat:{atk_base:{0:1b}}}}]}
scoreboard players add @s wp.damage1 2   {Inventory:[{Slot:0b,tag:{dat:{atk_base:{1:1b}}}}]}
scoreboard players add @s wp.damage1 4   {Inventory:[{Slot:0b,tag:{dat:{atk_base:{2:1b}}}}]}
scoreboard players add @s wp.damage1 8   {Inventory:[{Slot:0b,tag:{dat:{atk_base:{3:1b}}}}]}
scoreboard players add @s wp.damage1 16  {Inventory:[{Slot:0b,tag:{dat:{atk_base:{4:1b}}}}]}
scoreboard players add @s wp.damage1 32  {Inventory:[{Slot:0b,tag:{dat:{atk_base:{5:1b}}}}]}
scoreboard players add @s wp.damage1 64  {Inventory:[{Slot:0b,tag:{dat:{atk_base:{6:1b}}}}]}
scoreboard players add @s wp.damage1 128 {Inventory:[{Slot:0b,tag:{dat:{atk_base:{7:1b}}}}]}
scoreboard players add @s wp.damage1 256 {Inventory:[{Slot:0b,tag:{dat:{atk_base:{8:1b}}}}]}
```

**这不是一张数据表，这是把 NBT 里的一段二进制逐位解出来。** 整个文件只由 4 个参数决定：

| 参数 | 例子 |
|---|---|
| 物品栏槽位 | `0b` / `1b` / `2b` / `100b` |
| NBT 字段名 | `atk_base` / `cooldown` / `spec` / `defence` |
| 目标 objective | `wp.damage1` / `wp.cd1` / `Eq.Defence` |
| 位宽 | 9 位 / 8 位 / 4 位 |

15 个文件（5 列 × 3 行）里没有任何控制流、条件或运行期决策。**它是数据，被写成了代码。**

## 1.2 第二种形状：真·数据行（只有 2 处，且都是死代码）

`rpg/misc/treasure/`（9 行）与 `rpg/team_boss/1c/block/line1/`（5 行）确实是 N 行 × M 列。
但**全项目零调用者**——我搜遍了 1963 个函数，没有任何一处引用它们。
（这正是 `W_UNREACHABLE_FN` 存在的理由，也说明我不该拿它们当主要证据。）

所以优先级明确：**先做位解码，数据行往后放。**

## 1.3 方案对比

### 方案 A：`comptime` 里循环 `emit`（现有能力）

```ggm
comptime {
    for slot in 0..3 {
        for field in ["atk_base", "cooldown", "spec"] {
            emit ...   // ← 问题在这
        }
    }
}
```

**卡点**：`emit` 只能产出一条常量命令，产不出「9 条结构相同的命令」这个**模式**。
而 NBT 里的 `{0:1b}` … `{8:1b}` 下标必须随位号变化，字符串拼接也得靠编译器。

### 方案 B：给 `comptime` 加 `emit_bits` 内建

能解决，但把语言语义塞进了编译期内建，`comptime` 会变成一个命令生成器的外壳。

### 方案 C：独立的 `table` 声明 ← **推荐**

```ggm
namespace rpg:api

// 一张表 = 一条声明
#[decode(slot = 0, field = "atk_base", width = 9)]
table wp.damage1
```

展开规则（**这就是全部语义**）：

```mcfunction
scoreboard players set @s <目标> 0
scoreboard players add @s <目标> 2^k {Inventory:[{Slot:<slot>b,tag:{dat:{<field>:{k:1b}}}}]}
                      ↑ 对 k = 0 .. width-1 各一条
```

**为什么是声明而不是语句**：没有控制流、没有条件、没有运行期决策，
4 个参数完全决定输出。用语句写只是把声明伪装成代码。

## 1.4 顺带解决「清单手写」的问题

`ApLoader/main.mcfunction` 显式列出 18 次调用——这是**列的枚举**，同样由参数决定：

```mcfunction
function rpg:api/ApLoader/weapon/damage/1
function rpg:api/ApLoader/weapon/pen_base/1
function rpg:api/ApLoader/weapon/passive/1_1
// … 共 18 行
```

所以声明应该能**顺带生成这份清单**：

```ggm
#[decode(slots = 0..2, fields = ["atk_base", "cooldown", "spec"])]
table weapon {
    emit_call_list   // 生成 main 里的调用顺序
}
```

## 1.5 明确不做的部分

- **运行期索引**（`table[i]`，i 是变量）：1.12.2 没有数组索引，只能展开成 N 条守卫。
  `末法` 里零需求——所有表都是全量加载。
- **表内容来自外部文件**（CSV/JSON）：那是 `py/` 生成器的事，不是语言的事。

## 1.6 验收标准

1. `ApLoader/weapon/` 下 15 个文件 + `equip/defence` 能被声明生成
2. 生成结果与现有手写文件**逐字节相同**（objective 名可不同）
3. `main.mcfunction` 的 18 行清单也自动生成
4. 做不到 1–3 就不该做这个特性

---


# 二、影子实体与 `on death`

## 2.1 问题陈述

`on death { ... }` 目前报 `E_HOOK_UNSUPPORTED`。原因是硬的：

**1.12.2 没有实体死亡事件。** 实体死后：
- `@e[type=zombie]` 立刻匹配不到它
- 它的计分板分数**还在**（分数挂在 UUID 上，不随实体消失）
- 但没有任何命令能告诉你「它刚刚死了」

## 2.2 `末法` 实际怎么做的（证据）

它不靠事件，靠**伤害流程自己驱动**：

```mcfunction
# rpg/monster/hurt/death/boss/main.mcfunction
function rpg:monster/hurt/death/boss/迟暮森灵/main if @s[tag=迟暮森灵]
```

调用点是攻击方造成伤害之后。所以死亡处理拿到的 `@s` 是**攻击方**，
不是死者——死者信息由调用方传入或由 `if @s[tag=…]` 区分。

**这是 1.12.2 里唯一能工作的形状：死亡处理必须由杀死它的那一方驱动。**

## 2.3 为什么影子实体方案不可靠

设想编译成：每个实例配一个不可见 armor_stand 保管状态，每 tick 检查本体是否还在。

三个硬障碍：

| 障碍 | 说明 |
|---|---|
| **没有实体引用** | 1.12.2 无法存「实体 X 的句柄」。唯一关联方式是坐标或自定义名，两者都会在移动/重名时失效 |
| **无法判断「特定实体是否还活着」** | 没有 `@e[uuid=…]`。选择器只能按属性筛，不能按身份筛 |
| **代价是每个实例一个额外实体** | 团本一次刷 20+ 怪 = 20+ 额外 armor_stand + 每 tick 20+ 条同步命令 |

第二点是致命的：**影子实体无法知道自己对应哪一个本体**。

## 2.4 建议：改成 `on kill`

```ggm
[selector(@e[type=zombie,tag=mob.monster])]
class Monster {
    var hp: int = 40

    on kill(killer: @a) {   // killer 是杀死它的实体
        killer.Coin += 10
    }
}
```

这个形状：
- **可正确实现**：伤害流程里判断 `hp <= 0`，此时 `@s` 是攻击方，死者由参数携带
- **和 `末法` 的实际写法一致**（2.2）
- **零额外实体、零额外 tick 开销**

## 2.5 如果坚持要 `on death`

唯一正确的形状是**显式登记 + 差集**：

```ggm
class Monster {
    on spawn { /* 编译器生成：登记进存活表 */ }
    on death { ... }
}
```

编译器维护「上一 tick 的存活快照」与「这一 tick 的存活集」的差集。代价：
- 每 tick 一次全量 `@e[selector]` 扫描
- 一个每类一份的登记 objective
- 差集运算（需要临时分数）

在 20 人 / 团本 20 怪的规模下**可以接受**，但它仍然只能发现「消失了」，
无法区分「被打死」「掉虚空」「被 `/kill`」「区块卸载」。

**我的建议：先不做。** 若需求只是「被打死时给奖励」，`on kill` 就够且便宜得多。

---

# 三、值域分析

## 3.1 一个根源，三条症状

| 症状 | 现在 | 有值域分析后 |
|---|---|---|
| `#[wrapping]` | 解析、接受、**无作用** | 未标注且可能溢出 → `W_WRAP_UNPROVEN` |
| 字面量范围 | 只查字面量 | `a + 1` 里 a 可能接近 INT32_MAX → 报警 |
| `W_DIV_ZERO` | 未实现 | `a / b` 里 b 可能为 0 → 报警 |

## 3.2 区间格

每个值携带区间 `[lo, hi]`（含端点），端点用 **int64** 表示——中间结果可能超出 int32。
超出即退化为 `⊤`（未知）。

```
        ⊤  未知 = [-2^31, 2^31-1]
        |
    [lo, hi]   任意区间
        |
        ⊥  不可达
```

- **join**（分支汇合）：`[min(lo1,lo2), max(hi1,hi2)]`
- **widen**（循环头防不收敛）：迭代 3 次后仍在扩大的端点直接推到 ±∞

## 3.3 传递函数

| 表达式 | 结果 |
|---|---|
| 字面量 `n` | `[n, n]` |
| `a + b` | `[a.lo+b.lo, a.hi+b.hi]`，溢出即退化 |
| `a - b` | `[a.lo-b.hi, a.hi-b.lo]` |
| `a * b` | 四端点两两相乘取 min/max |
| `a / b` | `b` 含 0 则退化；否则四端点相除 |
| `a % b` | `[-(|b|-1), |b|-1]` |
| `min(a,b)` | `[min(lo), min(hi)]` |
| `max(a,b)` | `[max(lo), max(hi)]` |
| `clamp(a,lo,hi)` | `[lo, hi]` ← **最有用的一条** |
| 比较运算 | `[0, 1]` |

## 3.4 值从哪来（关键难点）

**没有范围来源时，任何含变量的 `+` 都会报警。** roadmap 已指出这一点。

| 来源 | 例子 | 进入分析的方式 |
|---|---|---|
| 字面量初始化 | `let x = 5` | `[5,5]` |
| `clamp` | `x = clamp(x, 0, 100)` | `[0,100]` |
| 分支收窄 | `if hp > 0 { }` 内 | `[1, INT32_MAX]` |
| 范围注解 | `fn f(x: #[range(0,100)] int)` | 入口假设 |
| 字段初始化器 | `var hp: int = 40` | `[40,40]` |
| **`#[budget(N)]`** | 循环上界 | 迭代次数上界 |

### 分支收窄是价值最高的部分

```ggm
if hp > 0 {        // 此分支内 hp ∈ [1, INT32_MAX]
    hp -= 1        // 结果 [0, INT32_MAX-1] —— 安全
}
```

没有分支收窄，这个最常见的写法就会误报。**实现成本最高，也最不能省。**

### GGM 的一个意外优势

`while` **语言强制**必须带 `#[budget(N)]`。所以循环迭代次数有上界，
这正好压住 widen 带来的精度损失——其它语言要额外做循环边界分析，GGM 白拿。

## 3.5 分阶段落地

### 阶段 1：局部，不跨函数 ← **建议先做**

- 函数内的 `let` / 赋值 / `if` 收窄 / 字面量 / `#[budget]`
- 覆盖：函数内算术溢出、除数可能为 0
- 解锁 `#[wrapping]` 真正生效

### 阶段 2：签名上的 `#[range]`

```ggm
fn clamp_hp(hp: #[range(0, 1000)] int) -> int { ... }
```

调用点检查实参是否落在范围内 → 新检查 `E_RANGE_VIOLATION`。

### 阶段 3：过程间（跨函数）

沿调用图传播区间。**成本高、收益低**：GGM 的函数都是「同 tick 内跑完」，
参数区间在调用点就能算出来。**建议停在阶段 2。**

## 3.6 `#[wrapping]` 的验收标准

- 函数体所有算术都能证明不溢出 → 不需要注解
- 有任一无法证明 → 报 `W_WRAP_UNPROVEN`，除非标了 `#[wrapping]`
- **`luo/math/lcg_step` 是唯一合法的用户**——它的设计就是要回绕

所以验收很明确：**标了 `#[wrapping]` 的只有它，且它必须是必要的。**

---

# 四、实施顺序

| 顺序 | 项 | 理由 | 规模 |
|---|---|---|---|
| 1 | 值域分析阶段 1 | 一个实现解锁三条检查 | 中 |
| 2 | 数据表（方案 C） | 15 个文件 → 5 条声明 | 中 |
| 3 | 值域分析阶段 2（`#[range]`） | 依赖阶段 1 | 小 |
| 4 | `on kill` | 便宜、正确、与 `末法` 一致 | 小 |
| — | 影子实体 `on death` | **建议不做** | 大 |

---

# 五、需要你拍板

1. **数据表**：声明式（方案 C）还是 `comptime` 内建（方案 B）？我推荐 C。
2. **`on death`**：改成 `on kill`，还是坚持影子实体？我推荐 `on kill`。
3. **值域分析**：停在阶段 1，还是做到阶段 2？我推荐先做阶段 1。

