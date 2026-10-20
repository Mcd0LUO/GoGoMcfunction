# GGM 语法规范 v0.1

> 目标：编译到 Minecraft Java 1.12.2 的 `.mcfunction`。
> 设计约束来自 `luo/math` 库的真实用法（309 行 / 17 文件）与本项目知识库。
> 本文是**完整语法**：词法 → 声明 → 类型 → 效果 → 语句 → 表达式 → 降级规则 → 检查规则。

---

## 0. 设计公理

1. **`@s` 是唯一的执行主体。** 一切命令都在某个实体上下文里跑。没有 `@s` 的地方（`on load`）不能调用需要 `@s` 的函数——这是编译错误，不是运行时意外。
2. **三种「多」必须语法可区分**：扇出（`execute ... function`，每个实体一次）、广播写（一条命令写 N 个实体）、迭代（`for e in entities<sel>`）。混淆它们是性能事故的唯一来源。
3. **没有栈。** 递归只允许两种：`#[inline]` 展开，或带 `#[budget]` 的有界自递归。
4. **预算是语义。** `maxCommandChainLength` 超限后**静默丢弃**后续命令，所以成本必须能静态标注和检查。
5. **选择器是区间不是相等。** 用户写 `==`，编译器发射 `_min=N,=N`。这一条不可绕过。

---

## 1. 词法

```
// 行注释
/* 块注释 */

标识符      [A-Za-z_][A-Za-z0-9_]*
命名空间名   [A-Za-z0-9_-]+:[A-Za-z0-9_./-]+      // 如 luo:math/sqrt/NIM
整数        123  -123
定点        1.5  0.25  1000.0                    // 默认精度 3 位，值 = 字面量 × 1000
字符串      "..."  '...'

关键字
  namespace class type enum pure fn task extern comptime on every phase

  let var const global
  if else match when while for in step return cancel break continue
  spawn enter goto emit
  self none true false
  as where by requires ensure

  int fixed string bool ref list map
```

**保留但未实现**（v0.1 报 `E_NOT_IMPLEMENTED`）：`trait` `impl` `async` `yield` `macro` `mod` `use`。

---

## 2. 顶层声明

```
namespace combat                     // 每文件一个，映射到 data/functions/combat/

const MAX_HP: int = 1000             // 编译期常量，可参与 comptime
global var phase: int = 0            // fake player，全存档唯一
global var seed: int @ "seed"        // 显式绑定既有 objective

type Fixed3 = fixed<3>
type Hp     = int within 0..MAX_HP   // 范围类型，赋值处静态收窄

enum Phase {                         // ADT，可带字段
    Idle,
    Casting { timer: int, spell: SpellId },
    Active  { power: Fixed3 },
    Dead,
}

enum SpellId { Fireball, IceSpike, Heal }

pure fn damage(base: int, def: int) -> int { ... }
fn apply(t: ref<Monster>, amount: int) { ... }
task cast(spell: SpellId) { ... }

class Monster { ... }

extern fn luo:math/sqrt(n: int) -> int { in n => arg1, out return }
extern var arg1: int @ "arg1"

comptime { ... }
on load { ... }
on tick { ... }
on every 5s { ... }
```

### 2.1 `class` 的完整形态

```
[selector(@e[type=zombie,tag=mob.monster])]
class Monster {
    // 字段 = @s 上的 objective，默认按类前缀命名 M.hp
    var hp: int = 40                 // 强制初始化器
    var uid: int = -1
    var target: ref<Monster> = none

    // 生命周期
    on spawn { uid = uid_alloc() }
    on despawn { }
    on death { }

    // 每 tick（自动注册到调度器）
    @tick
    fn tick() { ... }

    fn deal(dmg: int) { hp -= dmg }              // self 调用，免费
    @broadcast fn shout(msg: string) { ... }     // 广播写，O(N)

    static fn count() -> int { ... }             // 无 @s，不能访问字段
}
```

**谓词契约**：`selector` 里的条件就是这个类的**成员判定**。继承要求
`selector(子) ⊆ selector(父)`，编译器做选择器语法子集检查，不通过报
`E_SELECTOR_NOT_SUBSET`。

```
[selector(@e[type=zombie,tag=mob.monster,tag=mob.elite])]
class Elite : Monster { var aura: int = 0 }
// OK：显含父类全部 tag
```

**字段初始化**：`on spawn` 由编译器在 `summon` 后自动插入；已有实体用
`Monster.attach(@e[...])` 补初始化。没有初始化器的 `var` 报
`E_UNINIT_FIELD`——因为没有分数的实体在 `score_X=0` 下是否匹配是
**未验证语义**，不能依赖。

---

## 3. 类型系统

### 3.1 类型

| 类型 | 存储 | 说明 |
|---|---|---|
| `int` | int32 objective | 唯一数值基础类型 |
| `fixed<N>` | int32，值为真值 × 10^N | `fixed<3>` 基线 1000；默认字面量精度 3 |
| `bool` | int32，0/1 | `int` 的细化 |
| `string` | NBT / 常量 | 只能 `emit` / `say` / `tellraw`，不能运算 |
| `ref<T>` | int32，存 `uid` | 跨实体引用，`none` = -1 |
| `list<int, N>` | 实体池 / 固定 objective | `N` 必须编译期已知 |
| `T within A..B` | 同 T | 范围细化，赋值插钳制 |

**没有**：浮点、无符号、64 位、数组、字典、字符。

### 3.2 推断规则

局部推断 + 标注补全，**不做完整 HM**：

- `let x = 1` 推 `int`；`let x = 1.5` 推 `fixed<3>`
- `a / b`：两侧同为 `int` 则 `int`；任一为 `fixed<N>` 则 `fixed<N>`
- 函数返回类型必须显式标注（错误信息质量考虑）
- 泛型只在 `pure fn` 上，通过单态化展开

### 3.3 泛型与单态化

```
pure fn max<T: Ord>(a: T, b: T) -> T { if a > b { a } else { b } }
// max<int> 与 max<fixed<3>> 各生成一份，无运行时开销
```

约束只有 `Ord` / `Eq` / `Num` 三种内置，不支持用户自定义 trait。

### 3.4 类型错误

| 代码 | 场景 |
|---|---|
| `E_TYPE_MISMATCH` | `int` 与 `fixed<3>` 直接混用，需 `to_fixed<3>(x)` |
| `E_NO_FLOAT` | 浮点字面量出现在非 `fixed` 期望位置 |
| `E_RANGE_VIOLATION` | `Hp` 类型收到可能越界的值 |
| `E_NOT_EXHAUSTIVE` | `match` 未覆盖 `enum` 全部变体 |
| `E_UNINIT_FIELD` | `class` 字段无初始化器 |

---

## 4. 效果系统

### 4.1 三档效果

| 效果 | 含义 | 允许的操作 |
|---|---|---|
| `pure` | 纯计算 | 不可发命令、不可等待、不可读写 objective、可 `comptime` 求值 |
| `fn` | 同 tick 内完成 | 可发命令、可读写 objective、**不可** `wait` |
| `task` | 可跨 tick | 全部，含 `wait` / `spawn` / `enter` |

`fn` 与 `task` 的差别**只有 Tick 效果**。调用 `task` 必须 `spawn`，调用 `fn` 直接调。

### 4.2 上下文字段

| 字段 | 含义 | 提供者 |
|---|---|---|
| `needs @s` | 需要实体上下文 | 默认所有非 `static` 函数 |
| `needs position` | 需要 `~ ~ ~` 相对坐标 | 与 `@s` 同时提供 |
| `broadcast` | 一条命令写多实体 | 只用于 `@broadcast fn` |

`on load` / 命令方块入口 / `static fn` **不提供 `@s`**。在其中调用
`needs @s` 的函数报 `E_NO_SELF_CONTEXT`——这是知识库「静默失败」一节里最常见的坑。

### 4.3 效果推断

`fn` 的效果 = 自身语句效果 ∪ 被调用者效果。`pure` 调用 `fn` 是**编译错误**，
即使函数体只有一行。效果写在签名上，由三个关键字编码，不需要额外子句。

---

## 5. 语句

### 5.1 变量

```
let x = 5                    // 局部，@s 上的 objective L.x
let y: fixed<3> = 1.5        // 定点
var hp = 40                  // 可重新赋值
const N = 10                 // 编译期常量
x = x + 1
hp += 5                      // += -= *= /= %=   （都是 operation）
```

### 5.2 局部变量 = 临时 objective

`let` 在 `@s` 上分配临时 objective，作用域结束由编译器 `reset`。
**临时槽复用**是必做的优化：按活跃区间分配 `T.0` `T.1` …，而不是每条 `let` 一个名字。

### 5.3 条件

```
if hp <= 0 {
    die()
} else if hp < MAX_HP / 4 {
    say "low"
} else {
    ratio = hp * 1000 / MAX_HP
}

when key == CAST { cast(SpellId::Fireball) }    // 守卫式，等价于无 else 的 if
```

条件必须是**单比较**（`< <= > >= == !=`）或 `bool` 变量。复合条件自动拆成嵌套
`execute`，但短路语义不成立——见 §6.4。

### 5.4 匹配

```
match phase {
    Idle                    => idle_tick()
    Casting { timer, spell } => {
        timer += 1
        if timer >= 20 { enter Active { power = 1500 } }
    }
    Active  { power }        => { ... }
    Dead                    => {}
}

// 整数区间匹配，>8 个分支自动生成二分分派树
match roll {
    0        => miss()
    1..40    => normal()
    41..90   => crit()
    else     => execute()
}
```

`match` 必须穷尽（`E_NOT_EXHAUSTIVE`）。区间匹配降级为选择器区间，
等值匹配降级为 `_min=N,=N`。

### 5.5 循环

```
// 1. 静态展开：编译期完全展开，零运行时成本
for i in 0..10 step 2 {
    emit particle(reddust, i, 0, 0)
}

// 2. 有界运行时循环：自递归 + 递减计数器
for i in 0..n {                 // n 是运行时值，必须带 #[budget]
    spawn_one(i)
}

// 3. 数据相关：必须显式预算
while abs(x - prev) >= 2 #[budget(commands = 64, iterations = 32)] {
    prev = x
    x = (x + n / x) / 2
}

// 4. 迭代实体集合（见 5.6）
for p in entities<@a[tag=math.sort]> { ... }

// 5. 固定次数重复
repeat 3 { ... }
```

`while` 无预算报 `E_NO_BUDGET`。`for i in 0..constant` 走静态展开，不消耗预算。

### 5.6 实体迭代（三件套之一：迭代）

```
// 迭代选择器匹配的实体，@s 在循环体内是当前实体
for p in entities<@e[type=zombie,tag=mob.monster]> {
    p.hp -= 1                    // 每个实体各写一次（扇出语义）
}

// 带过滤
for p in entities<@a> where hp == 0 { ... }

// 求值器（在 @s 上求，结果写到 @s）
let m   = reduce<min>(@a[tag=math.sort], math)
let cnt = count<@e[tag=x]>
let any = any<@a[tag=fly]>
```

**`reduce<min|max>` 使用 `operation < / >` 多目标归约**。该语义在知识库里标为
⚠️ 未验证；`luo/math/sort` 依赖它且工作正常，视为已证实。若目标服实测失败，
编译器提供 `--no-reduce` 降级为逐个比较。

### 5.7 跨 tick（`task`）

```
task cast(spell: SpellId) {
    enter Phase::Casting { timer: 0, spell }
    wait 20                                  // 1 秒
    effect_of(spell)
    repeat 3 {
        wait 4
        say "轰"
    }
    enter Phase::Active { power = 1500 }
}

fn on_key() {
    spawn cast(SpellId::Fireball)            // 创建实例，立即返回
}
```

```
wait <int>           // 等待 N tick
wait until <条件>    // 每 tick 求值，条件成立继续（需预算）
cancel               // 中止当前 task
```

**每 tick 调用**：`@tick` 注解的方法由调度器扇出，不需要手写 `execute @e[...]`。

### 5.8 中断与取消

```
task cast() {
    wait 20
} on cancel {                                 // 被 cancel 时的清理
    enter Phase::Idle
}
```

`cancel` 只对 `task` 合法。`cancel all` 要求 `self` 是实例承载实体。

---

## 6. 表达式

### 6.1 算术与比较

```
a + b   a - b   a * b   a / b   a % b        // operation += -= *= /= %=
a < b   a <= b  a > b   a >= b  a == b  a != b
-a                       // * -1
min(a,b)  max(a,b)       // operation < / >
abs(a)  clamp(a, lo, hi)  ceil_div(a, b)
```

### 6.2 定点

```
let r: fixed<3> = hp * 1000 / MAX_HP      // 显式换算
let r: fixed<3> = hp / MAX_HP as fixed<3> // 自动插 * 1000
let d = base * AMP / to_int(1000)         // 混合运算需显式
to_fixed<3>(x)    to_int(f)
```

定点乘法自动插缩放：`f1 * f2` 降级为 `(f1*f2)/1000`，编译器检查溢出风险并警告
`W_FIXED_OVERFLOW`。

### 6.3 位运算与模运算

**1.12.2 没有位运算原语**。`&` `|` `^` `<<` `>>` 一律报
`E_NO_BITWISE`。替代：

```
// 2 的幂取模 → %=
x % 1024

// 位编码解码 → 用 NBT 条件累加（知识库做法），仅在 class on spawn 中
#[nbt_bits] var flags: int

// 显式回绕（LCG 需要）
#[wrapping] pure fn lcg_next(s: int) -> int {
    (1103515245 * s + 1234) % 67108864
}
```

`#[wrapping]` 关闭溢出检查。若函数内出现 `% M` 且编译器能证明 `M | 2^32`
（即 M 是 2 的幂），则允许省略 `#[wrapping]`——这是 LCG 唯一合法的免检查路径，
其余情况报 `W_WRAP_UNPROVEN`。

### 6.4 复合条件与短路

```
if hp > 0 && hp < 100 { ... }
```

降级为嵌套：

```mcfunction
execute @s[score_L.0_min=1] ~ ~ ~ execute @s[score_L.0=99] ~ ~ ~ function ns:then
```

**语义警告**：`&&` 左侧为假时右侧不执行，但 `||` **不能**用同样方式短路——
1.12.2 的选择器无法表达「或」。编译器把 `a || b` 降级为两条独立的
`execute`，**两侧副作用都会执行**。若任一操作数有副作用（函数调用、`+=`），
报 `E_NO_SHORT_CIRCUIT`。

### 6.5 表达式形式的 if / match

```
let x = if hp <= 0 { 1 } else { 0 }      // 降级为两条 execute 写临时槽
let y = match phase { Idle => 0, _ => 1 }
```

---

## 7. 广播写（三件套之二）

**一条命令同时写多个实体的 objective。** 与扇出是完全不同的成本模型：

```
// 广播写：1 条命令，N 个实体各写一次
@a[tag=math.sort] temp1 = #sort_matcher math

// 扇出：N 次函数调用，每次 M 条命令
for p in entities<@a[tag=math.sort]> { p.temp1 = ... }     // 等价但成本 N×M

// 广播比较后条件写（sort 的核心）
@a[tag=math.sort] temp1 = #sort_matcher math - self.math
@a[tag=math.sort] where temp1 == 0 {
    self.return = rank
    tag remove(self, math.sort)
}
```

**语法形式**：`<选择器> <目标字段> = <值表达式>`，选择器在行首且以 `@` 开头。
若左侧写的是 `p.field`（`p` 为 `for` 变量），则是扇出。
**两者字面不同，成本一眼可辨。**

广播写右侧只能引用 `#全局` 与 `self` 的字段——因为每条命令的 `@s` 会变。

---

## 8. 编译期求值（`comptime`）

```
comptime {
    const R = 3.0
    for deg in 0..360 step 5 {
        let a = deg * PI / 180.0
        emit particle(reddust, R * cos(a), 1.0, R * sin(a))
    }
}

const DAMAGE: list<int, 16> = comptime map(0..16, |i| i * i / 3)

comptime {
    // 生成常量池（替代手写 128 行的 ini.mcfunction）
    for n in -1..100 { emit_const("#math", n) }
}
```

`comptime` 块内是**编译器内求值**，用 `f64` 计算，`emit` 输出为常量命令。
块内不可调用 `fn` / `task`。

`emit_const` 是内建：`scoreboard players set #<name> <obj> <value>`。

---

## 9. 外部绑定与兼容

```
// 绑定既有 objective 名（保住 luo:math 的 ABI）
extern var arg1: int   @ "arg1"
extern var return: int @ "return"
extern var temp1: int  @ "temp1"

// 绑定外部 objective 上的特定 holder
extern global TPS: int @ "TpsMoniter:TPS" @holder "@TPS_TEST"

// 调用既有 mcfunction
extern fn luo:math/sqrt(n: int) -> int {
    in  n => arg1
    out return
}

extern task luo:x/async_thing()          // 外部跨 tick，不可分析
```

`extern fn` 的调用降级为：

```mcfunction
scoreboard players operation @s arg1 = @s <n 的临时槽>
function luo:math/sqrt
scoreboard players operation @s <结果槽> = @s return
```

**extern 函数不参与效果推断**，一律视为 `fn + needs @s`。`extern task` 视为 `task`。

### 9.1 实体池

```
pool dice = entities<@e[type=armor_stand,tag=range.0_9]> size 10

on load {
    populate dice with [
        { Tags: ["rand", "rand.0", "range.0_9"], Marker: 1b, ... },
    ]
}

let v = pool_sample(dice, math)                         // @r[...]
let v = pool_sample_unique(dice, math) using team dice.sampled
```

`pool_*` 是 stdlib，编译成 `@r[...]` / `@r[...team=!X]` + `scoreboard teams join`。

---

## 10. 降级规则总表

| 语法 | 1.12.2 产物 |
|---|---|
| `if a < b` | `execute @s[score_T=-1] ~ ~ ~ function L`（`<` ⟺ `≤ -1`） |
| `if a <= b` | `execute @s[score_T=0] ~ ~ ~ function L` |
| `if a > b` | `execute @s[score_T_min=1] ~ ~ ~ function L` |
| `if a >= b` | `execute @s[score_T_min=0] ~ ~ ~ function L` |
| `if a == b` | `execute @s[score_T_min=0,score_T=0] ~ ~ ~ function L` |
| `if a != b` | 两条 `execute`（`<` 和 `>`） |
| `a += b` | `scoreboard players operation @s T += @s U` |
| `min(a,b)` | `scoreboard players operation @s T < @s U` |
| `match N>8` | 二分分派树，`function L/lo_hi if @s[...]` |
| `@sel.f = v` | `scoreboard players operation @sel <obj> = <v>` |
| `for e in entities<sel>` | `execute @sel ~ ~ ~ function L` |
| `for i in 0..const` | 静态展开，无运行时成本 |
| `while [budget]` | 自递归 + 中止标志 |
| `wait N` | 状态机：`t.wait -= 1` + `pc` 推进 |
| `every 5s` | 相位分配，整除 20 检查，末尾**编译器生成**归零 |
| `enter State {f: v}` | `scoreboard players set @s st.pc N` + 字段赋值 |
| `spawn task(...)` | `summon` 承载实体 + 参数写入 + 入活动列表 |
| `comptime { emit }` | 常量命令序列 |
| `extern fn` | `operation arg1 = T` + `function ext` + `operation T = return` |

---

## 11. 编译期检查清单

| 代码 | 检查 | 严重 |
|---|---|---|
| `E_NO_SELF_CONTEXT` | 无 `@s` 处调用需要 `@s` 的函数 | error |
| `E_WAIT_IN_FN` | `fn` 内使用 `wait` | error |
| `E_PURE_CALLS_FN` | `pure` 调用 `fn` / `task` | error |
| `E_NO_BUDGET` | `while` / 运行时 `for` 无 `#[budget]` | error |
| `E_NOT_EXHAUSTIVE` | `match` 未穷尽 | error |
| `E_SELECTOR_NOT_SUBSET` | 子类选择器不是父类子集 | error |
| `E_UNINIT_FIELD` | `class` 字段无初始化器 | error |
| `E_NO_BITWISE` | 使用 `& \| ^ << >>` | error |
| `E_NO_SHORT_CIRCUIT` | `\|\|` 或 `&&` 含副作用 | error |
| `E_TYPE_MISMATCH` | `int` / `fixed` 混用 | error |
| `E_NOT_IMPLEMENTED` | 保留关键字 | error |
| `W_WRAP_UNPROVEN` | 可能溢出且未标 `#[wrapping]` | warning |
| `W_FIXED_OVERFLOW` | 定点乘法可能溢出 int32 | warning |
| `W_DIV_ZERO` | 除数可能为 0 | warning |
| `W_BUDGET_ESTIMATE` | 预算估计超出实际 2 倍 | warning |

**除零警告的判定**：`luo/math/sqrt/NIM` 对 `arg1 ∈ [0,3]` 会除零。
加 `requires n >= 0` 与小值分支后静态消掉——这是 `W_DIV_ZERO` 的典型用例。

---

## 12. 完整示例：`luo/math` 的 GGM 版本

```
namespace luo:math

// ── 常量池由 comptime 生成，替代 128 行 ini.mcfunction ──
comptime {
    for n in -1..100 { emit_const("#math", n) }
    for n in [120, 200, 1000, 2000, 20000] { emit_const("#math", n) }
    emit_const("#math", "2147483647")
    emit_const("#math", "-2147483647")
}

global var seed: int = 12345
const LCG_A = 1103515245
const LCG_C = 1234
const LCG_M = 67108864

#[wrapping]
pure fn lcg_next(s: int) -> int { (LCG_A * s + LCG_C) % LCG_M }

fn rand(lo: int, hi: int) -> int requires lo <= hi {
    let span = hi - lo + 1
    let x = lcg_next(seed)
    seed = lcg_next(seed)
    lo + abs(x % span)
}

fn rand_p(scale: int, chance: int) -> bool requires chance >= 0 {
    rand(1, scale) <= chance
}

// ── 开方：牛顿迭代 ──
pure fn sqrt(n: int) -> int requires n >= 0 {
    if n < 4 { return if n == 0 { 0 } else { 1 } }   // 消掉 n/4 == 0 的除零
    let x = n / 4
    let prev = 0
    while abs(x - prev) >= 2 #[budget(iterations = 32)] {
        prev = x
        x = (x + n / x) / 2
    }
    x
}

// ── 排序：选择排序 + 极值归约 ──
fn sort_asc() {
    for p in entities<@a> { p.rank = 0; tag add(p, math.sort) }
    let rank = 0
    while any<@a[tag=math.sort]> #[budget(iterations = 64)] {
        let m = reduce<min>(@a[tag=math.sort], math)
        @a[tag=math.sort] temp1 = #sort_matcher math - self.math   // 广播写
        rank += 1
        @a[tag=math.sort] where temp1 == 0 {
            self.return = rank
            tag remove(self, math.sort)
        }
    }
}
```

---

## 13. 与 `luo/math` 现实的差距（需先打的 5 个补丁）

1. **`while` + `#[budget]`** —— §5.5
2. **`for e in entities<>` + 集合缩小** —— §5.6
3. **广播写 `@sel f = v`** —— §7
4. **`#[wrapping]` + 2 的幂取模证明** —— §6.3
5. **兼容层 `extern var` + `comptime` 常量池** —— §9

已确认的 4 个原库 bug（不进语言，进迁移报告）：
- `sqrt/NIM` 对 `arg1 < 4` 除零
- `lcg` 范围映射最小值权重减半
- `sample` 不重置，抽满失效
- `sort` 末尾 `reset @a math` 摧毁调用方数据

---

## 14. 实现优先级

| 阶段 | 内容 | 验收 |
|---|---|---|
| P0 | 词法 + 语法 + 选择器构造器 + 1.12.2 命令模型 + 发射器 | `hello` 编译通过，golden 全绿 |
| P1 | 类型检查 + 效果检查 + `extern fn` + `comptime emit_const` | `luo/math` 的 `sqrt` `is_happen` 可编译并逐字节匹配 |
| P2 | `class` + `@tick` + 调度器 + 状态机 | `Monster` 类可跑，相位不漂移 |
| P3 | `while` + `for entities` + 广播写 + `sort` | `luo/math/sort` 迁移完成 |
| P4 | 预算分析 + `#[wrapping]` + 错误信息打磨 | 全部检查项有测试 |

---

*本规范是 v0.1，`luo/math` 迁移完成前会不断修订。任何降级规则变更必须同步本文。*
