# CMCF 语法规范 v1.0（削减版）

> 目标：编译到 Minecraft Java 1.12.2 的 `.mcfunction`。
> **设计原则：不为还没写过的项目设计语法。** v0.1 有 12 种声明、7 种类型、3 档效果、17 种语句；
> 在真实代码库 `luo/math`（309 行 / 17 文件）里，一半以上一次都没用到。v1.0 只保留有真实用法的东西。
> 被砍掉的全部在 `docs/roadmap.md`，注明「等出现第 3 个真实用例再实现」。

---

## 0. 公理

1. **`@s` 是唯一执行主体。** 没有 `@s` 的地方（`on load`）不能调用需要 `@s` 的函数——编译错误。
2. **三种「多」语法可区分**：扇出 `for e in entities<sel>`、广播写 `@sel f = v`。成本差一个数量级。
3. **没有栈。** 递归只有一种：带 `#[budget]` 的有界自递归。
4. **预算是语义。** `maxCommandChainLength` 超限后**静默丢弃**，所以必须静态标注。
5. **选择器是区间不是相等。** 用户写 `==`，编译器发射 `_min=N,=N`。

---

## 1. 词法

```
// 行注释
标识符    [A-Za-z_][A-Za-z0-9_]*
命名空间   IDENT ":" IDENT                     // luo:math
整数      [0-9]+                               // 不支持小数
字符串    "..."  '...'
换行      语句分隔符（与分号等价）
```

关键字（共 19 个）：

```
namespace comptime const extern
fn class selector on
let if else while for in step
return self true false none
entities where budget wrapping
```

---

## 2. 类型

**只有两种**：`int`（int32）、`bool`（int32 的 0/1）。

没有定点、没有浮点、没有字符串类型（字符串只能是字面量，用于 `say` / `emit`）、
没有泛型、没有枚举、没有引用。小数用千分比 `int` 自己约定。

---

## 3. 顶层声明（7 种）

```
namespace luo:math                        // 每文件一个

comptime { ... }                          // 编译期求值，产出常量命令

const LCG_A = 1103515245                  // 编译期常量

extern var arg1: int @ "arg1"             // 绑定既有 objective
extern global SEED: int @ "math" @holder "seed"
extern fn luo:math/sqrt(n: int) -> int { in n => arg1, out return }

fn rand(lo: int, hi: int) -> int { ... }  // 普通函数，同 tick 内完成

[selector(@e[type=zombie,tag=mob.monster])]
class Monster { var hp: int = 40; ... }   // 谓词 + 字段 + 方法

on load { ... }                           // 入口
on tick { ... }
on every 5s { ... }
```

### 3.1 `fn`

`
fn name(p1: int, p2: int) -> int { body }
`

无 `pure` / `task` / 效果标注。**一个 `fn` 只有一种：同 tick 内跑完。**
跨 tick 靠 `class` + `@tick`（见 3.2），不靠 `wait`。

### 3.2 `class`

`
[selector(@e[type=zombie,tag=mob.monster])]
class Monster {
    var hp: int = 40                 // 必须有初始化器
    var timer: int = 0

    on spawn { }                     // summon 后自动执行
    on death { }

    @tick fn tick() { ... }          // 由调度器扇出
    fn deal(dmg: int) { hp -= dmg }
}
`

- `selector` 是这个类的**成员判定谓词**。`Monster` 与 `@e[type=zombie,tag=mob.monster]` 是同一件事的两面。
- 字段 = `@s` 上的 objective，默认前缀 `M.`（`M.hp`）。
- **跨 tick 的唯一写法**：一个类实例就是一个协程的状态。`spawn CastTimer()` = `summon` 一个带 tag 的实体。

`
// 「20 tick 后执行」——不需要 wait
class CastTimer {
    var timer: int = 0
    @tick fn tick() {
        timer += 1
        if timer >= 20 { fire(); kill(self) }
    }
}
`

### 3.3 入口

| 写法 | 降级 |
|---|---|
| `on load` | 生成 `luo:math/load`，由 `gameLoopFunction` 的前置或命令方块调用 |
| `on tick` | 生成 `luo:math/tick`，挂到 `gamerule gameLoopFunction` |
| `on every 5s` | 相位分配 + **编译器生成归零**，整除 20 检查 |
| `@tick fn` in class | `execute <selector> ~ ~ ~ function 类名/tick` |
| `on spawn` in class | 扇出到`<selector> 且没有标记标签`的实体，函数体先打标签再执行 |

> **实现状态（0.3.0）**：`@tick`、`on spawn`、字段、方法解析、方法参数都已实现。
> `on death` **有非空体时报错**——1.12.2 无法观察已经消失的实体，正确实现需要一个
> 影子实体，假装能做到只会产出一个永不触发的处理器。空体是占位符，接受。
> 字段 objective 名按 16 字符截断，所以类名前 6 个字符相同的两个类会撞车，
> 由 `E_FIELD_COLLISION` 报出。

---

## 4. 语句（6 种）

```
let x = 5                      // 临时 objective，编译器按活跃区间分配
x = x + 1                      // = += -= *= /= %=
if cond { } else { }
while cond #[budget(32)] { }   // 必须带预算
for e in entities<@a[tag=x]> { }                 // 扇出
@a[tag=math.sort] sort.diff = self.sort.key - lo // 广播写
```

**没有**：`match` `enum` `repeat` `break` `continue` `goto` `return` 之外的控制流。

### 4.1 条件

条件只能是**单比较**：`< <= > >= == !=`，或一个 `bool` 变量。
`&&` 降级为嵌套 `execute`；`||` 语义上无法短路，含副作用时报错。

降级规则（**这是整个编译器存在的理由**）：

| 源码 | 发射的 `score_` 条件 |
|---|---|
| `a < b` | `score_T=-1`（即 ≤ -1） |
| `a <= b` | `score_T=0` |
| `a > b` | `score_T_min=1` |
| `a >= b` | `score_T_min=0` |
| `a == b` | `score_T_min=0,score_T=0` |
| `a != b` | 两条 `execute` |

### 4.2 广播写 vs 扇出

`
@a[tag=math.sort] temp1 = #m - self.math    // 1 条命令写 N 个实体
for p in entities<@a[tag=math.sort]> { p.temp1 = ... }   // N 次调用
`

选择器在行首 `@` 开头 = 广播写；`p.field` 形式 = 扇出。字面可辨。

---

## 5. 表达式

```
a + b   a - b   a * b   a / b   a % b
a < b   a <= b  a > b   a >= b  a == b  a != b
-a
min(a,b)  max(a,b)  abs(a)  clamp(a,lo,hi)  rem_euclid(a,m)
reduce<min>(@a[tag=x], obj)   count<@e[tag=x]>   any<@a[tag=x]>
```

**字面量作 `operation` 操作数时自动物化**为 `#cmcf.const.<值>` 常量
（1.12.2 的 `operation` 右操作数必须是记分板名字，不能是字面量）。

`#[wrapping]` 注解关闭溢出检查（LCG 需要）；若函数体内有 `% M` 且编译器能证明 M 是 2 的幂，则无需标注。

> **实现状态（2026-10-19）**：`W_WRAP_UNPROVEN` 尚未实现，因此 `#[wrapping]`
> 目前**被接受但无作用**——1.12.2 的计分板算术本来就按 32 位回绕，注解所要求的
> 行为就是默认行为。它真正生效的时机是范围分析落地、未标注的函数能报出溢出警告时。
>
> 与之相关且**已实现**的是字面量范围检查：超出 int32 的整数字面量一律拒收
> （`E_INT_RANGE`），因为它没有正确编码——运行时会静默回绕成另一个数。
> 前导负号在检查前并入字面量，所以 `-2147483648` 合法。

---

## 6. `comptime`

```
comptime {
    for n in -1..100 {
        emit scoreboard.set("#math." + str(n), "math", n)
    }
}
```

块内用 Go 的 `int64` 求值，`emit` 输出常量命令。不可调用 `fn`。

---

## 7. 检查清单

### 已实现

| 代码 | 检查 |
|---|---|
| `E_PARSE` | 语法错误（含未知属性、属性未附着到声明） |
| `E_NO_NAMESPACE` | 文件没有 `namespace` |
| `E_INT_RANGE` | 字面量或 `emit` 值超出 int32 计分板范围 |
| `E_FN_COLLISION` | 两个声明在 1.12.2 归一化后是同一个函数 |
| `E_FN_RESERVED` | 用户声明占用了编译器生成的名字（`init` 等） |
| `E_UNKNOWN_CALLEE` | 调用了本模块里不存在的函数 |
| `E_FIELD_COLLISION` | 两个字段截断后落在同一个 objective 上 |
| `E_HOOK_UNSUPPORTED` | `on death` 有非空函数体（需要影子实体） |
| `E_BUILTIN_UNIMPLEMENTED` | 用了本构建尚未实现的内置（`abs` 作值、`clamp`、`count`） |
| `W_UNREACHABLE_FN` | 声明了但本模块无人调用 |
| `E_NO_BUDGET` | `while` / `loop` 没有 `#[budget(N)]` |
| `E_UNINIT_FIELD` | `class` 字段没有常量初始化器 |
| `E_ARITY` | 调用实参个数与声明不符 |
| `E_ASSIGN_TARGET` | 赋值目标不是可写位置 |
| `E_COND` / `E_EXPR` / `E_ARG` / `E_STMT` | 表达式或语句无法降级 |
| `E_FOR_SELECTOR` | `for e in` 的来源不是 `entities<...>` |
| `E_BROADCAST_SELECTOR` / `E_BROADCAST_VALUE` | 广播写语句的选择器或值非法 |
| `E_COMPTIME_RANGE` / `E_COMPTIME_LIST` / `E_EMIT_CONST` | `comptime` 的范围、列表或 `emit` 参数不是编译期常量 |
| `E_STR_RUNTIME` | 字符串在运行期才求值 |
| `E_BAD_COMMAND` | 后端生成的命令没通过语法检查（编译器 bug） |
| `E_IR_INVARIANT` | IR 结构不变量被破坏（编译器 bug） |

### 计划中

| 代码 | 检查 | 缺什么才能做 |
|---|---|---|
| `W_WRAP_UNPROVEN` | 可能溢出且未标 `#[wrapping]` | 值域分析 |
| `W_DIV_ZERO` | 除数可能为 0 | 值域分析 |
| `E_NO_SELF_CONTEXT` | 无 `@s` 处调用需要 `@s` 的函数 | 调用图上的 `@s` 可用性分析 |
| `E_TYPE_MISMATCH` | `int` / `bool` 混用 | 类型检查（当前只有 `int`） |
| `E_NO_SHORT_CIRCUIT` | `\|\|` 含副作用 | 副作用分析 |
| `E_SELECTOR_NOT_SUBSET` | 子类选择器不是父类子集 | 选择器蕴含判定 |
| `W_BUDGET_ESTIMATE` | 预算估计超实际 2 倍 | 循环迭代次数估计 |

**`E_UNKNOWN_CALLEE` 和 `W_UNREACHABLE_FN` 是只有编译器能做的检查。**
1.12.2 对「调用不存在的函数」既不报错也不执行——调用指令合法、文件加载成功、运行期什么都不发生。
所以一个拼错的名字买到的是一条**静默死掉的代码路径**。同理，没有任何调用者的函数只是躺在那里，
它本该驱动的功能永不触发。这两件事在单文件视角下不可见，在整模块视角下很廉价（一次遍历）。

`W_UNREACHABLE_FN` 只能是警告：命令方块和 `gameLoopFunction` 都能从编译器看不见的地方调用它，
所以消息里直接写明这个出口，而不是断言该函数是死代码。

`E_BAD_COMMAND` 与 `E_IR_INVARIANT` 没有语言级触发路径：触发它们的是编译器自身的 bug，
不是用户代码。它们是**在渲染前后各设的一道闸**，因为这类失败是静默误编译而不是崩溃。
`internal/analysis/invariants.go` 里的结构不变量（空 objective、空 holder、不可达块、
objective 名超 16 字符）同理。

**复杂度花在这里，不是特性上。** 20 个用户写错 `@s[score_hp=0]`（以为等于 0，实际是 ≤ 0）
的那一刻，比多一种 `enum` 重要一百倍。但**清单上写着不等于实现了**——上面第一张表才是
当前真实存在的检查。

---

## 8. 完整示例：`luo/math`

```
namespace luo:math

comptime {
    for n in -1..100 { emit scoreboard.set("#math." + str(n), "math", n) }
    for n in [120, 200, 1000, 2000, 20000] { emit scoreboard.set("#math." + str(n), "math", n) }
}

extern var arg1:   int @ "arg1"
extern var return: int @ "return"
extern global SEED: int @ "math" @holder "seed"

const LCG_A = 1103515245
const LCG_C = 1234
const LCG_M = 67108864

#[wrapping]
fn lcg_step(s: int) -> int { (LCG_A * s + LCG_C) % LCG_M }

fn rand(lo: int, hi: int) -> int {
    let span = hi - lo + 1
    let x = lcg_step(SEED)
    SEED = SEED + LCG_C
    lo + rem_euclid(x, span)
}

fn sqrt(n: int) -> int {
    if n < 4 { return 1 }
    let x = n / 4
    let prev = 0
    while abs(x - prev) >= 1 #[budget(32)] {
        prev = x
        x = (x + n / x) / 2
    }
    x
}

fn sort_asc() {
    tag_add(@a, "math.sort")
    let rank = 0
    while any<@a[tag=math.sort]> #[budget(64)] {
        let lo = reduce<min>(@a[tag=math.sort], sort.key)
        @a[tag=math.sort] sort.diff = self.sort.key - lo
        rank += 1
        @a[tag=math.sort] where sort.diff == 0 {
            self.sort.rank = rank
            tag_remove(self, "math.sort")
        }
    }
}
```

**309 行 → 约 45 行。** 并且 4 个原库缺陷在编译期被消掉（除零、收敛判据、范围映射偏置、收尾摧毁调用方数据）。

---

*任何降级规则变更必须同步本文。砍掉的东西见 `docs/roadmap.md`。*
