# GGM 语法规范 v1.0（削减版）

> 目标：编译到 Minecraft Java 1.12.2 的 `.mcfunction`。
> **设计原则：不为还没写过的项目设计语法。** v0.1 有 12 种声明、7 种类型、3 档效果、17 种语句；
> 在真实代码库 `luo/math`（309 行 / 17 文件）里，一半以上一次都没用到。v1.0 只保留有真实用法的东西。
> 被砍掉的全部在 `docs/roadmap.md`，注明「等出现第 3 个真实用例再实现」。

---

## 0. 公理

1. **`@s` 是唯一执行主体，且它不是到处都有。** `[load]` 函数与 `<ns>/cycle` 都
   没有执行者（1.12.2 从命令方块 / `gameLoopFunction` 调用函数时不给实体）；
   `[every(...)]`、`[spawn]`、`for e in entities<…>` 都有，因为它们在某个
   `execute` 的扇出体内。在没有执行者的地方写实体分数是**编译错误**，因为
   1.12.2 会静默丢弃那条命令。
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
fn class
let if else while for loop in step
return self true false none
entities where budget wrapping
bits enum decode encode over
```

另有四个词是**普通标识符**，只在特定位置当关键字用，所以拿它们当变量名也可以在
别处用：`add` / `set`（在 `decode` 块里）、`matches`（在条件里）、`from`（在 `decode` 里）。

**时长不是词法单位**，它是 `[every("5s")]` 里的一个字符串。所以 `1tick` 里
的 `tick` 不必是关键字，单位表也只有一处实现（`syntax.ParseDuration`）。

---

## 2. 类型

**只有两种**：`int`（int32）、`bool`（int32 的 0/1）。

没有定点、没有浮点、没有字符串类型（字符串只能是字面量，用于 `say` / `cmd`）、
没有泛型、没有枚举、没有引用。小数用千分比 `int` 自己约定。

---

## 3. 顶层声明（7 种）

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

obj wp.damage1                            // 声明一个 objective，编译器负责创建
```

**没有 `on` 家族。** 入口是 `fn` 或 `class` 上的**属性**，见 3.3。

### 属性

属性是声明前的一个方括号列表，逗号分隔，可带参数：

```ggm
[load]
fn on_load() { }

[every("5s")]
fn heartbeat() { }

[selector(@e[tag=mob]), every("1tick")]
class Monster {
    var hp: int = 40

    [spawn]
    fn spawned() { hp = 40 }

    [every("100ticks")]
    fn regen() { hp = hp + 10 }
}

[wrapping, export]
fn lcg_step(s: int) -> int { (LCG_A * s + LCG_C) % LCG_M }
```

| 属性 | 用在 | 含义 |
|---|---|---|
| `[load]` | fn | 世界加载时调用一次，**无 `@s`** |
| `[every("...")]` | fn / class 方法 | 每 N tick 调用一次 |
| `[spawn]` | class 方法 | 实体首次出现时调用一次 |
| `[selector(...)]` | class | 成员判定谓词 |
| `[wrapping]` | fn | 关闭溢出警告（尚未生效，见第 5 节） |
| `[export]` | fn | 库函数，抑制 `W_UNREACHABLE_FN` |

**属性是遍历的，不是内联的。** 编译期读属性列表，生成 P 值和 void 函数——只消
除属性或加新属性即可，不需要改语言语法。

### 3.3 入口

一个模块只有两个需要接线的入口，都是编译器生成的：

```mcfunction
# <ns>/boot —— 世界加载时，由命令方块调用一次
function <ns>/comptime
function <ns>/on_load
function std:lib/comptime
function std:lib/on_load

# <ns>/cycle —— gameLoopFunction 指向这里
function <ns>/make_clock unless @e[tag=ggm.clock]
execute @e[tag=ggm.clock,c=1] ~ ~ ~ function <ns>/on_cycle
```

`boot` 汇总**所有文件**的 `[load]` 函数和常量池。仅靠每文件一个 `<ns>/load`
是不可行的：用户只挂一个，库的初始化永远不会跑，且无任何提示。

`cycle` 里**没有 `@s`**——`gameLoopFunction` 不给执行实体。它只负责让时钟实体
存在，然后交给 `execute` 扇出：

```mcfunction
# <ns>/make_clock
summon armor_stand ~ ~ ~ {Tags:["ggm.clock"],Marker:1b,Invisible:1b,NoGravity:1b,Invulnerable:1b,PersistenceRequired:1b}

# <ns>/on_cycle —— @s = 时钟实体
scoreboard players add @s p.100 1
function <ns>/every/100t if @s[score_p.100_min=100,score_p.100=100]
```

时钟实体就是参考包的 `TickTimer`，不是新发明：1.12.2 上这是唯一能凭空造出
执行者的办法。

### 3.1 `fn`

```
fn name(p1: int, p2: int) -> int { body }
```

无 `pure` / `task` / 效果标注。**一个 `fn` 只有一种：同 tick 内跑完。**
跨 tick 靠 `class` + `[every(...)]`（见 3.2），不靠 `wait`。

### 3.2 `class`

```
[selector(@e[type=zombie,tag=mob.monster])]
class Monster {
    var hp: int = 40                 // 必须有初始化器
    var timer: int = 0

    [spawn]                          // summon 后自动执行一次
    fn spawned() { hp = 40 }

    [every("1tick")]                 // 由调度器扇出
    fn tick() { timer += 1 }

    fn deal(dmg: int) { hp -= dmg }
}
```

- `selector` 是这个类的**成员判定谓词**。`Monster` 与 `@e[type=zombie,tag=mob.monster]` 是同一件事的两面。
- 字段 = `@s` 上的 objective，名字是 `<类名>.<字段名>`，截断到 16 字符（`Monster.hp` → `Monste.hp`）。
- **跨 tick 的唯一写法**：一个类实例就是一个协程的状态。

```
// 「20 tick 后执行」——不需要 wait
[selector(@e[tag=cast.timer])]
class CastTimer {
    var timer: int = 0
    [every("1tick")]
    fn tick() {
        timer += 1
        if timer >= 20 { fire(); kill(self) }
    }
}
```

`[death]` 不存在：1.12.2 无法观察一个已经消失的实体，唯一正确的实现需要影子实体。
与其留一个永远报错的属性，不如不写。

### 3.3 入口

| 写法 | 生成 | 有 `@s`？ |
|---|---|---|
| `[load]` | 函数体保留作者的名字，由 `boot` 调用 | **没有** |
| `[every("5s")]` | 函数体保留作者的名字，由周期链调用 | 有（时钟实体） |
| `[every(...)]` on class method | `execute <selector> ~ ~ ~ function 类/方法` | 有（被扇出的实例） |
| `[spawn]` on class method | 扇出到`<selector> 且没有标记标签`的实体，函数体先打标签再执行 | 有（被扇出的实例） |

#### 周期：一个周期一个调度器

周期的**身份是 tick 数**，不是写法。`[every("5s")]` 与 `[every("100ticks")]`
是同一个周期：一个计数器 `p.100`、一个调度器 `every/100t`。按写法命名会得到两个
互相漂移的计数器，而各自单独看都正确。

```mcfunction
# <ns>/every/100t —— 两个声明共用这一个文件
scoreboard players set @s p.100 0
function app:slow
function app:also_slow
```

同一周期的多个声明按**命令行文件顺序、再按源文件顺序**排进同一个调度器。

周期之间是**链**不是扇出：只有不被更小周期整除的那些从 `on_cycle` 驱动，
其余由最近的更小周期按**比值**计数驱动。

```mcfunction
# on_cycle 驱动 2t 和 5t；1s 由 5t 驱动（20/5=4），3s 由 1s 驱动（3）
scoreboard players add @s p.5 1
function <ns>/every/5t if @s[score_p.5_min=5,score_p.5=5]
```

**`1tick` 直接内联进 `on_cycle`**：每 tick 计数到 1 的计数器是两条废命令加一个
恒真判断。所以没有 `p.1`，也没有 `every/1t`。

#### `[load]` 里不能写实体分数

```ggm
obj counter
[load]
fn on_load() { counter = 7 }   // E_NO_SELF_CONTEXT
```

`obj` 声明的 objective、类字段、`extern x: int = @s.x` 都是**执行者的**分数。
`[load]` 从命令方块进来，`@s` 什么都不指，`set @s counter 7` 会被 1.12.2
静默丢弃。所以这是编译错误，不是警告。

```ggm
extern SEED: int = seed.math   // 假玩家，任何上下文都存在
[load]
fn on_load() { SEED = 12345 }  // 可以
```

编译器自己的寄存器（`t0`、`l0`）在无执行者的函数里搬到假玩家 `#ggm.reg`，
所以 `[load]` 里仍能做算术——只是**作者的**数据不能假装挂在实体上。

#### 同时触发时，取交集

```ggm
[load, every("5s")]
fn mixed() { hp = 1 }          // E_NO_SELF_CONTEXT
```

一个函数若同时是 `[load]` 和 `[every]`，`@s` 的可用性是两种上下文的**交集**。
交集里有「没有」，所以没有。这是纯局部规则，不需要调用图分析。
> **实现状态（0.18.0）**：`[load]`、`[every(...)]`、`[spawn]`、`[selector(...)]`、
> `[wrapping]`、`[export]`、字段、方法解析、方法参数都已实现。
>
> `[every(...)]` **有真正的相位门**：每个周期一个计数器 `p.<ticks>`，
> 由 `on_cycle` 每 tick 递增，到达上界时调用该周期的调度器；
> 调度器**先把自己归零**，所以下一次恰好是一个周期之后。
>
> 时长支持 `tick(s)` / `s(ecs)` / `m(in)`，其它单位直接报错——一个排不了期的
> 入口比没有入口更糟。

**时长是一个字符串字面量**，不是词法单位：`[every("1tick")]`、`[every("5s")]`。

> 这不是可有可无的润色。当初时长是 `INT + IDENT` 两个 token，解析器手工拼接，
> 于是 `1tick` **根本写不出来**——绕过办法是让单复数承载语义（`1ticks`），
> 再后来干脆加了一个 `DURATION` 词法单位。
>
> 字符串更省：`tick` 不必是关键字，词法器不必知道单位表，`[every(...)]` 也不必
> 和别的属性用两套括号。单位到 tick 的换算只剩一处（`syntax.ParseDuration`）。

**亚 tick 时长直接报错**，不四舍五入。1.12.2 的最小单位就是游戏刻，
把 `120ms` 悄悄变成 3 tick 会让周期按作者从未要求的节奏触发。

**周期只接受字面量**。周期决定调度器函数名和计数器 objective 名，两者都在
编译期定死，所以值必须编译期已知。

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
是属性语法（`[wrapping]`）。必填的东西打扮成可选的，读者会以为可以省。

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

`[wrapping]` 注解关闭溢出检查（LCG 需要）；若函数体内有 `% M` 且编译器能证明 M 是 2 的幂，则无需标注。

> **实现状态（2026-10-19）**：`W_WRAP_UNPROVEN` 尚未实现，因此 `[wrapping]`
> 目前**被接受但无作用**——1.12.2 的计分板算术本来就按 32 位回绕，注解所要求的
> 行为就是默认行为。它真正生效的时机是范围分析落地、未标注的函数能报出溢出警告时。
>
> 与之相关且**已实现**的是字面量范围检查：超出 int32 的整数字面量一律拒收
> （`E_INT_RANGE`），因为它没有正确编码——运行时会静默回绕成另一个数。
> 前导负号在检查前并入字面量，所以 `-2147483648` 合法。

---

## 6. `comptime`

```ggm
comptime {
    for n in -1..100 {
        score("#math." + str(n), "math", n)
    }
}
```

块内用 Go 的 `int64` 求值。不可调用 `fn`。

### 6.1 两种输出

| 调用 | 产出 |
|---|---|
| `score(holder, objective, value)` | `scoreboard players set <holder> <objective> <value>` |
| `cmd(text)` | 一整条命令，原样 |

`score` 建常量池；`cmd` 用来**生成命令表**。两者缺一不可——
一张 88 行的二分表里全是 `tp`，它不是常量分数，`score` 写不出来。

### 6.2 字符串化

| 调用 | 例 |
|---|---|
| `str(n)` | `str(42)` → `"42"` |
| `strpow2(bit)` | `strpow2(10)` → `"1024"` |
| `strdec2(bit, scale)` | `strdec2(31, 1000)` → `"2147483.648"` |

后两个是**位宽表**的必需品：偏移是 `2^bit / 1000`，权重是 `2^bit`。
没有它们，作者只能把表手写出来——而手写的表不是生成的表。

### 6.3 只支持三种语句

`for` / `score` / `cmd`。**别的语句会报 `E_COMPTIME_STMT`，不会被静默跳过。**

这条是硬约束而不是限制：`if` 曾经掉进 switch 的 default 直接消失——被它包住的
那些行一条都没生成，没有任何提示，而输出看起来是完整的。**生成器里的静默
no-op 比缺功能更糟。**

需要按条件生成时，把范围拆成多个循环（`0..30` 和 `0..21`），而不是写 `if`。

### 6.4 命令内置函数

编译期之外，有些命令要在**运行期**发出去。它们单独列在这里，因为它们
不是 `comptime`——它们在程序跑的时候执行，参数可以来自选择器。

| 调用 | 生成 |
|---|---|
| `summon(type, [nbt], [x, y, z])` | `summon <type> [nbt] [x y z]` |
| `entitydata(target, n"...")` | `entitydata <target> <nbt>` |
| `effect(target, effect, seconds, [amp], [hide])` | `effect <target> <...>` |
| `tp(target, x, y, z)` | `tp <target> <x> <y> <z>` |
| `kill(target)` | `kill <target>` |
| `say(text)` | `say <text>` |
| `cmd(text)` | `<text>`，原样 |

#### 参数只能是字面量

```ggm
summon("armor_stand", n"{Tags:[\"mob\"],Marker:1b}")   // 可以
effect(self, "speed", 10, 1, true)                    // 可以
summon("pig", hp)                                     // E_ARG
```

不是实现偷懒，是 1.12.2 的硬限制：**命令是文本，没有任何语法能把运行期的
分数拼进文本里。** 接受一个 `int` 然后写它此刻的值，会产出一条看起来正确、
却忽略此后每一次变化的命令——正是这个编译器存在的理由。

所以 `E_ARG` 的措辞说的是边界而不是语法：作者写 `summon("pig", hp)` 时该
知道为什么这条路不存在，而不是只看到「参数 2 类型不对」。

#### `tp` 的相对与绝对

```ggm
tp(self, "~", "~1", "~")      // 相对：从当前位置往上 1 格
tp(self, "100", "64", "-20")  // 绝对：到那个方块
```

`~` 由作者写。旧写法替作者加 `~`，所以**只能**表达相对移动，绝对传送根本
写不出来。让波浪号可见，每个轴多一个字符，两种形式就都能说清自己是什么——
而且这正是 1.12.2 自己的区分方式，中间不再有可错的翻译。

#### `cmd` 是逃生口，但仍然过校验

`cmd` 和编译期的 `cmd` **同名**：「发一条任意命令」在语言里只有一个名字。
编译期那份从常量拼出来，运行期这份是源码里的字面量。

它绕过内置表，但**不绕过** `mcfunction.Validate`：渲染出的那一行仍然要过
选择器参数白名单，所以 `score_x>=1` 这种 1.12.2 不认识的键还是会被抓出来。

#### `n"..."` 是普通表达式

以前 `n"..."` 只在语法专门写死的地方合法（`matches` 条件和 `nbt` 声明），
于是 NBT 编解码器**够不到命令**——能解码一件已经存在的物品，却没法给出一件。
现在它是普通表达式节点，可以出现在任何参数位置，解析期仍然校验合法性。

---

## 7. 检查清单

### 已实现

| 代码 | 检查 |
|---|---|
| `E_PARSE` | 语法错误（含未知属性、属性未附着到声明） |
| `E_NO_NAMESPACE` | 文件没有 `namespace` |
| `E_INT_RANGE` | 字面量或 `score` 值超出 int32 计分板范围 |
| `E_FN_COLLISION` | 两个声明在 1.12.2 归一化后是同一个函数 |
| `E_FN_RESERVED` | 用户声明占用了编译器生成的名字（`init` 等） |
| `E_UNKNOWN_CALLEE` | 调用了本模块里不存在的函数 |
| `E_FIELD_COLLISION` | 两个字段截断后落在同一个 objective 上 |
| `E_BUILTIN_UNIMPLEMENTED` | 用了本构建尚未实现的内置（`abs` 作值、`clamp`、`count`） |
| `W_UNREACHABLE_FN` | 声明了但本模块无人调用 |
| `E_NO_BUDGET` | `while` / `loop` 没有 `cap <n>` 上界 |
| `E_NO_SELF_CONTEXT` | 在无执行者的函数（`[load]`）里读写实体分数 |
| `E_LOAD_PARAMS` | `[load]` 函数带参数——世界加载时没人能给它 |
| `E_ATTR_UNKNOWN` | 属性不在 `DeclAttrs` 里（如 `[death]`、`[wraping]`） |
| `E_ATTR_DUP` | 同一个属性写了两次 |
| `E_EVERY_ARG` | `every` 的参数不是时长字符串字面量 |
| `E_UNINIT_FIELD` | `class` 字段没有常量初始化器 |
| `E_ARITY` | 调用实参个数与声明不符 |
| `E_ASSIGN_TARGET` | 赋值目标不是可写位置 |
| `E_COND` / `E_EXPR` / `E_ARG` / `E_STMT` | 表达式或语句无法降级 |
| `E_FOR_SELECTOR` | `for e in` 的来源不是 `entities<...>` |
| `E_BROADCAST_SELECTOR` / `E_BROADCAST_VALUE` | 广播写语句的选择器或值非法 |
| `E_COMPTIME_RANGE` / `E_COMPTIME_LIST` / `E_EMIT_CONST` | `comptime` 的范围、列表或 `score` 参数不是编译期常量 |
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
| `W_WRAP_UNPROVEN` | 可能溢出且未标 `[wrapping]` | 值域分析 |
| `W_DIV_ZERO` | 除数可能为 0 | 值域分析 |
| `E_NO_SELF_CONTEXT`（调用图部分） | 无 `@s` 处**调用**需要 `@s` 的函数 | 调用图上的 `@s` 可用性分析 |
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
    for n in -1..100 { score("#math." + str(n), "math", n) }
    for n in [120, 200, 1000, 2000, 20000] { score("#math." + str(n), "math", n) }
}

extern arg1:   int = @s.arg1
extern return: int = @s.return
extern SEED:   int = seed.math

const LCG_A = 1103515245
const LCG_C = 1234
const LCG_M = 67108864

[wrapping]
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