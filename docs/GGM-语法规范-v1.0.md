# GGM 语法规范 v1.0（削减版）

> 目标：编译到 Minecraft Java 1.12.2 的 `.mcfunction`。
> **设计原则：不为还没写过的项目设计语法。** v0.1 有 12 种声明、7 种类型、3 档效果、17 种语句；
> 在真实代码库 `luo/math`（309 行 / 17 文件）里，一半以上一次都没用到。v1.0 只保留有真实用法的东西。
> 被砍掉的全部在 `docs/roadmap.md`，注明「等出现第 3 个真实用例再实现」。

---

## 0. 公理

1. **`@s` 是唯一执行主体。** 没有 `@s` 的地方（`on load`）不能调用需要 `@s` 的函数——编译错误。
2. **「多」有两条路，成本差一个数量级**：扇出 `for e in entities<sel>` 是 N 次调用，
   广播写 `@sel f = v` 是一条命令写 N 个实体。字面可辨。
3. **没有栈。** 递归只有一种：有界自递归。每个循环都必须写 `budget <n>` 或
   用范围 `for i in lo..hi`，编译器无法证明的循环不收。
4. **预算是语义。** `maxCommandChainLength` 超限后**静默丢弃**，所以循环的上界
   必须是语法的一部分，不能靠运行期判断。
5. **选择器是区间不是相等。** 用户写 `==`，编译器发射 `_min=N,=N`。

---

## 1. 词法

```
// 行注释
标识符    [A-Za-z_][A-Za-z0-9_]*
命名空间   IDENT ":" IDENT                     // luo:math
整数      [0-9]+                               // 不支持小数
字符串    "..."  '...'
NBT 值    n"..."                               // SNBT，编译期解析
NBT 路径  p"..."                               // 从物品根起算的键路径
换行      语句分隔符（与分号等价）
```

关键字：

```
namespace comptime const extern obj nbt
fn class selector on
let if else while for loop in step
return self true false none
entities where budget wrapping
bits enum decode encode over
```

另有五个词是**普通标识符**，只在特定位置当关键字用，所以拿它们当变量名也可以在
别处用：`add` / `set`（在 `decode` 块里）、`matches`（在条件里）、`from`（在 `decode` 里）。

---

## 2. 类型

**只有两种**：`int`（int32）、`bool`（int32 的 0/1）。

没有定点、没有浮点、没有字符串类型（字符串只能是字面量，用于 `say` / `emit`）、
没有泛型、没有枚举、没有引用。小数用千分比 `int` 自己约定。

---

## 3. 顶层声明（8 种）

```
namespace luo:math                        // 每文件一个

comptime { ... }                          // 编译期求值，产出常量命令

const LCG_A = 1103515245                  // 编译期常量

extern arg1: int = @s.arg1               // 绑定既有 objective（实体自己）
extern SEED: int = seed.math             // 绑定假玩家 seed 的 math
extern fn luo:math/sqrt(n: int) -> int { in n => arg1, out return }

fn rand(lo: int, hi: int) -> int { ... }  // 普通函数，同 tick 内完成

[selector(@e[type=zombie,tag=mob.monster])]
class Monster { var hp: int = 40; ... }   // 谓词 + 字段 + 方法

on load { ... }                           // 入口
on tick { ... }
on every 5s { ... }

obj wp.damage1                            // 声明一个 objective，编译器负责创建
```

### 3.1 `fn`

```
fn name(p1: int, p2: int) -> int { body }
```

无 `pure` / `task` / 效果标注。**一个 `fn` 只有一种：同 tick 内跑完。**
跨 tick 靠 `class` + `@tick`（见 3.2），不靠 `wait`。

### 3.2 `class`

```
[selector(@e[type=zombie,tag=mob.monster])]
class Monster {
    var hp: int = 40                 // 必须有初始化器
    var timer: int = 0

    on spawn { }                     // summon 后自动执行
    on death { }

    @tick fn tick() { ... }          // 由调度器扇出
    fn deal(dmg: int) { hp -= dmg }
}
```

- `selector` 是这个类的**成员判定谓词**。`Monster` 与 `@e[type=zombie,tag=mob.monster]` 是同一件事的两面。
- 字段 = `@s` 上的 objective，默认前缀 `M.`（`M.hp`）。
- **跨 tick 的唯一写法**：一个类实例就是一个协程的状态。`spawn CastTimer()` = `summon` 一个带 tag 的实体。

```
// 「20 tick 后执行」——不需要 wait
class CastTimer {
    var timer: int = 0
    @tick fn tick() {
        timer += 1
        if timer >= 20 { fire(); kill(self) }
    }
}
```

### 3.3 入口

| 写法 | 降级 |
|---|---|
| `on load` | 生成 `luo:math/load`，由 `gameLoopFunction` 的前置或命令方块调用 |
| `on tick` | 生成 `luo:math/tick`，挂到 `gamerule gameLoopFunction` |
| `on every 5s` | 生成 `luo:math/every/5s`，**与其它入口一样是普通函数** |
| `@tick fn` in class | `execute <selector> ~ ~ ~ function 类名/tick` |
| `on spawn` in class | 扇出到`<selector> 且没有标记标签`的实体，函数体先打标签再执行 |

> **实现状态（0.11.0）**：`@tick`、`on spawn`、字段、方法解析、方法参数都已实现。
>
> `on every N` **只生成函数，不含相位门**——调用它几次就跑几次，与 `on tick` 一样。
> 真要做到「每 N 秒一次」，需要一个每 tick 递增的计数器加整除检查，而
> `gameLoopFunction` 每 tick 调一次正好提供这个节拍。**未实现**，因为至今没有
> 真实用例（示例与参考数据包里零使用）。见 `docs/roadmap.md`。
> `on death` **有非空体时报错**——1.12.2 无法观察已经消失的实体，正确实现需要一个
> 影子实体，假装能做到只会产出一个永不触发的处理器。空体是占位符，接受。
> 字段 objective 名按 16 字符截断，所以类名前 6 个字符相同的两个类会撞车，
> 由 `E_FIELD_COLLISION` 报出。

### 3.4 `extern` —— 绑定已经存在的分数

```ggm
extern arg1: int = @s.arg1        // 执行者自己的 arg1
extern SEED: int = seed.math      // 假玩家 seed 的 math
extern fn luo:math/sqrt(n: int) -> int { in n => arg1, out return }
```

`外部的分数 = <持有者>.<objective>`，**持有者在前**——和 1.12.2 自己的写法
`scoreboard players set <持有者> <objective>` 一致。

- 持有者是 `@s` 时就是普通的 `@s` 分数；写成名字（`seed`）就是**假玩家**，
  也就是全局变量：跨 tick、跨实体、存进存档。
- 第一个点号分隔持有者与 objective，之后全归 objective——因为 objective 名
  自己带点号（`wp.damage1`）。
- `extern` **不发射任何命令**。它声明「这东西已经在存档里了」，编译器只负责引用。
  要让它存在，用 `obj`（3.5）。

> 旧写法 `extern var x: int @ "obj"` 与 `extern global X: int @ "obj" @holder "名字"`
> 已删除。两个关键字的全部信息就是持有者，而持有者现在写在明面上了；
> `@holder` 还和选择器的 `@` 撞形状。

### 3.5 `obj` —— 声明一个 objective

```ggm
obj wp.damage1
obj Eq.Defence "防御"
obj post_damage stat.damageDealt "造成伤害"
```

对应 1.12.2 的三段：

```
scoreboard objectives add <名字> <criteria> [<display>]
```

| 写法 | 生成 |
|---|---|
| `obj wp.damage1` | `scoreboard objectives add wp.damage1 dummy` |
| `obj Eq.Defence "防御"` | `… Eq.Defence dummy 防御` |
| `obj post_damage stat.damageDealt "造成伤害"` | `… post_damage stat.damageDealt 造成伤害` |

- 省略 criteria 时是 `dummy`。其它 criteria 全放行（`health` `food` `air`
  `deathCount` `playerKillCount` `totalKillCount` `teamkill.<color>` `stat.*` `trigger`）。
- **`obj` 和 `extern` 的分工**：`obj` 由编译器发射 `objectives add`；
  `extern` 只绑定、不发射。两者同名报 `E_OBJECTIVE_CONFLICT`。

**为什么必须有这个声明**：1.12.2 里写一个不存在的 objective **不报错、也不生效**，
分数永远是 0。不声明就写等于静默丢掉结果——和选择器区间那条是同一类失败。

---

## 4. 语句（8 种）

```
let x = 5                      // 临时 objective，编译器按活跃区间分配
x = x + 1                      // = += -= *= /= %=
if cond { } else { }
while cond, budget 32 { }      // 上界必填
for i in 0..64 { }               // 计数循环
for e in entities<@a[tag=x]> { }  // 扇出
@a[tag=math.sort] sort.diff = self.sort.key - lo // 广播写
decode wp.damage1 from @s.Inventory(0)[atk_base] // NBT → 分数
encode @s.Item[money] = return over 1..10        // 分数 → NBT
loop cond, budget 32 { }        // 后测循环
```

**没有**：`match` `switch` `repeat` `break` `continue` `goto`。
跳出循环靠 `return`（整个函数返回）或自递归 + 标志位。

### 4.1 循环：一个 `for`，两个 `while`

```ggm
for i in 0..64 { }               // 计数：范围即上界
for e in entities<@a[tag=x]> { } // 扇出：集合即上界
while i < n, budget 64 { }       // 条件：先判后做，上界必填
loop  i > 0, budget 32 { }       // 条件：先做一次，再判
```

**`for` 的两种来源是同一件事**——「对一组东西各做一次」——所以共用一个写法：
范围是计数，`entities<…>` 是扇出。两者都**构造上有界**，因此都不需要 `budget`。

`i in lo..hi` **含两端**，`lo > hi` 时零次。上界可以是变量（`for i in 1..n`），
只在进入循环前求值一次——循环体改不了循环长度。

**只有条件循环需要 `budget`**，因为它的收敛性编译器证明不了。
逗号是这句语法的要点：它把循环头说的两件事分开——**测什么**，和**最多跑多久**。
写成 `while i < n budget 64` 就没有这个分界，上界会被读成条件的延续。

```ggm
while i < n, budget 64 { }     // 先判后做
loop  i > 0, budget 32 { }     // 先做一次，再判
```

1.12.2 **没有循环**。两者都降级成一个**调用自己**的函数：
一个计数器加条件，两者一起挂在 `if` 上，预算耗尽自然停下。

| | 判断位置 | 降级 |
|---|---|---|
| `while` | 循环体**之前** | `if cond { loop cond { body } }` |
| `loop` | 循环体**之后** | 循环体至少执行一次 |

**`, budget <n>` 是必填的**，漏了直接是解析错误。1.12.2 的 `function` 是入队展开，
没有这个上界，一个写错的条件就是无限递归——它不会报错，只会卡死服务器。

为什么不写 `#[budget(N)]`：它是循环的**操作数**，不是可选修饰，而 `#[…]` 在 GGM 里
是属性语法（`#[wrapping]`）。必填的东西打扮成可选的，读者会以为可以省。

### 4.2 条件

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

### 4.3 广播写 vs 扇出

```
@a[tag=math.sort] temp1 = #m - self.math    // 1 条命令写 N 个实体
for p in entities<@a[tag=math.sort]> { p.temp1 = ... }   // N 次调用
```

选择器在行首 `@` 开头 = 广播写；`p.field` 形式 = 扇出。字面可辨。

### 4.4 `decode` / `encode` —— NBT 与分数互转

**NBT 是谓词，不是数据源。** `scoreboard players add @s X 1 {…}` 的语义是
「**如果** NBT 匹配这个模式，**就**加 1」。所谓「读」是对 w 个谓词做加权折叠。

**两条方向的成本来自不同的地方**，所以是两个动词而不是一个 `=`：

| 方向 | 命令数 | 由谁决定 |
|---|---|---|
| `decode` | `1 + 位宽` | 字段（编码事实） |
| `encode` | **值域大小** | 使用点（作者的选择） |

实测两者可差 200 倍，所以成本必须写在语法里。

#### `decode` 只有一个原语

```ggm
decode <objective> from <容器> {
    init 0
    <NBT 谓词> add <v>
    <NBT 谓词> set <v>
}
```

发射 `set @s <obj> <init>`，然后每条 case 一条命令。

**位梯子、枚举、标签枚举、哨兵位都是它的缩写：**

| 想要 | 缩写 |
|---|---|
| 位折叠 | `[字段]`，字段声明为 `bits N` |
| 恒等枚举 | `[字段]`，字段声明为 `enum 1..4` |
| 映射枚举 | `[字段]`，字段声明为 `enum { "残响":1, … }` |
| 哨兵位 | `[字段]` 展开位梯子后，**块里追加一条 `set` case** |

哨兵位由作者自己放，不需要专门语法：

```ggm
decode temp2 from @s.SelectedItem[lv] {
    n"{tag:{dat:{lv:{max:1b}}}}" set -1
}
```

生成（与手写 `trigger.mcfunction` 逐条同序）：

```mcfunction
scoreboard players set @s temp2 0
scoreboard players add @s temp2 1 {SelectedItem:{tag:{dat:{lv:{0:1b}}}}}
scoreboard players add @s temp2 2 {SelectedItem:{tag:{dat:{lv:{1:1b}}}}}
scoreboard players add @s temp2 4 {SelectedItem:{tag:{dat:{lv:{2:1b}}}}}
scoreboard players set @s temp2 -1 {SelectedItem:{tag:{dat:{lv:{max:1b}}}}}
```

`add` 与 `set` 就是 1.12.2 的 `scoreboard players add` / `set`。
哨兵必须用 `set`（覆盖），用 `add` 会得到「位梯子 + (-1)」。

#### 容器

用 1.12.2 自己的 NBT 键名，不另造词：

| 写法 | 生成的 NBT 前缀 |
|---|---|
| `@s.SelectedItem` | `{SelectedItem:{tag:{…}}}` |
| `@s.Inventory(0)` | `{Inventory:[{Slot:0b,tag:{…}}]}` |
| `@s.Inventory(100..103)` | 四个槽位，**一次清零**后逐槽累加 |
| `@s.Item` | `{Item:{tag:{…}}}` |

`Inventory(n)` 里的 `n` 是**槽位号**（`Slot:nb`），不是列表下标。

#### 字段

```ggm
nbt atk_base = p"tag.dat.atk_base" : bits 11
nbt spec     = p"tag.dat.spec"     : enum 1..4
nbt quality  = p"tag.Tags"         : enum { "残响":1, "蚀刻":2, "回响":3, "星烬":4, "窄门":5 }
nbt count    = p"Count"            : enum 1..64
```

`p"…"` 的路径**从物品根起算**，和文件里的键写法一致（物品额外数据都在 `tag.dat` 下，
所以字段写 `tag.dat.atk_base`）。不把 `tag` 设成隐式根，因为 `Count` 与 `tag` 平级，
隐式根就表达不了它——**一条规则覆盖所有情况**，好过一条更短的写法加一个例外。

冒号后是编码方式，只有两种，区别在**叶子多不多一层复合**：

| 写法 | 生成 | 为什么 |
|---|---|---|
| `bits 11` | `{tag:{dat:{atk_base:{0:1b}}}}` | 位需要一层复合放下标 |
| `enum 1..4` | `{tag:{dat:{spec:1b}}}` | 值直接贴在键上 |
| `enum { "残响":1 }` | `{tag:{Tags:["残响"]}}` | 标签名是列表元素 |

#### `matches` —— 用 NBT 模式做条件

```ggm
if @s.Item matches n"{Item:{tag:{dat:{qlt:0b}}}}" {
    temp3 = 1
}
```

**这是模式匹配，不是「键存在」。** 数据包自己的写法（19 处）：

```mcfunction
scoreboard players tag @s add 残响 {Item:{tag:{dat:{qlt:0b}}}}
```

`qlt:0b` 要**等于** 0 才算匹配，物品上是 `qlt:1b` 就不匹配。

1.12.2 的选择器放不下 NBT，所以降级成两步：先
`scoreboard players tag <sel> add <编译器私有标记> <NBT>`，再用
`execute <sel>[tag=<标记>]` 分支。标记在每次探测前**先移除**——否则上一次匹配留下的
标记会让下一次无条件通过。

`n"…"` 是**完整的** NBT 值（自带 `Item:`/`SelectedItem:` 这层），不是片段。

#### `encode`

```ggm
encode @s.Item[money] = return over 1..10
```

**`over` 是必须的**：字段 `money` 是 14 位（decode 要 16384 个值），
但这一轮只掷出 1..10，所以只发 10 条 `entitydata`。
1.12.2 无法用分数构造 NBT——NBT 是常量文本，分数只决定发射哪一条。

> **不允许 `over full`。** 值域必须写死数字：`bits` 决定的是范围上界，
> 而 `2^14 = 16384` 条命令会撞上 `maxCommandChainLength`，超限后**静默丢弃**。

---

## 5. 表达式

```
a + b   a - b   a * b   a / b   a % b
a < b   a <= b  a > b   a >= b  a == b  a != b
-a
min(a,b)  max(a,b)  abs(a)  clamp(a,lo,hi)  rem_euclid(a,m)
reduce<min>(@a[tag=x], obj)   count<@e[tag=x]>   any<@a[tag=x]>
```

**字面量作 `operation` 操作数时自动物化**为 `#ggm.const.<值>` 常量
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
| `E_NO_BUDGET` | `while` / `loop` 没有 `cap <n>` 上界 |
| `E_UNINIT_FIELD` | `class` 字段没有常量初始化器 |
| `E_ARITY` | 调用实参个数与声明不符 |
| `E_ASSIGN_TARGET` | 赋值目标不是可写位置 |
| `E_COND` / `E_EXPR` / `E_ARG` / `E_STMT` | 表达式或语句无法降级 |
| `E_FOR_SELECTOR` | `for e in` 的来源不是 `entities<...>` |
| `E_BROADCAST_SELECTOR` / `E_BROADCAST_VALUE` | 广播写语句的选择器或值非法 |
| `E_COMPTIME_RANGE` / `E_COMPTIME_LIST` / `E_EMIT_CONST` | `comptime` 的范围、列表或 `emit` 参数不是编译期常量 |
| `E_STR_RUNTIME` | 字符串在运行期才求值 |
| `E_BAD_COMMAND` | 后端生成的命令没通过语法检查（编译器 bug）；含选择器参数白名单 |
| `E_IR_INVARIANT` | IR 结构不变量被破坏（编译器 bug） |

### 计划中

| 代码 | 检查 | 缺什么才能做 |
|---|---|---|
| `E_OBJECTIVE_CONFLICT` | `obj` 与 `extern` 绑定了同一个 objective | 无，可直接实现 |
| `E_OBJECTIVE_NAME` | objective 名超过 16 字符（1.12.2 会截断，截断后静默撞车） | 无，可直接实现 |
| `E_OBJECTIVE_DUP` | 同一个 `obj` 声明了两次 | 无，可直接实现 |
| `E_OBJECTIVE_UNDECLARED` | `decode` 的目标没被 `obj` 或 `extern` 声明过 | 无，可直接实现 |
| `E_NBT_PARSE` | `n"…"` / `p"…"` 不是合法 SNBT | 无，可直接实现 |
| `E_NBT_TYPE` | 字面量的类型与使用点要求不符 | 无，可直接实现 |
| `E_FIELD_BITS` | `bits` 不在 1..30（`bits=32` 时最高位 `2^31` 超出 int32） | 无，可直接实现 |
| `E_ENCODE_RANGE` | `over` 的上下界超出字段能表示的范围（会静默截断） | 无，可直接实现 |
| `W_FIELD_UNUSED` | 字段声明了但既没 `decode` 也没 `encode` | 无，可直接实现 |
| `W_WRAP_UNPROVEN` | 可能溢出且未标 `#[wrapping]` | 值域分析 |
| `W_DIV_ZERO` | 除数可能为 0 | 值域分析 |
| `E_NO_SELF_CONTEXT` | 无 `@s` 处调用需要 `@s` 的函数 | 调用图上的 `@s` 可用性分析 |
| `E_TYPE_MISMATCH` | `int` / `bool` 混用 | 类型检查（当前只有 `int`） |
| `E_NO_SHORT_CIRCUIT` | `\|\|` 含副作用 | 副作用分析 |
| `E_SELECTOR_NOT_SUBSET` | 子类选择器不是父类子集 | 选择器蕴含判定 |
| `W_BUDGET_ESTIMATE` | 预算估计超实际 2 倍 | 循环迭代次数估计 |

> **实现状态（0.8.0）**：第 3.4 节（`obj`）与第 4.3 节（`decode` / `encode` /
> `matches` / `n"…"` / `p"…"`）**已实现**，并在 GoMcRuntime 上做过往返验证。
> 上面「计划中」表里与它们相关的码也随之实现。
>
> `p"…"` 目前只在 `nbt` 声明里可用，还不是一般表达式——没有测量到独立使用的需求。

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

extern arg1:   int = @s.arg1
extern return: int = @s.return
extern SEED:   int = seed.math

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
    while y < x, budget 64 {
        prev = x
        x = (x + n / x) / 2
    }
    x
}

fn sort_asc() {
    tag_add(@a, "math.sort")
    let rank = 0
    while any<@a[tag=math.sort]>, budget 64 {
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