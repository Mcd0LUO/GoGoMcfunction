# 更新日志

本项目遵循[语义化版本](https://semver.org/lang/zh-CN/)。

**有两个版本号，它们不是同一个。**

| | 含义 | 当前值 |
|---|---|---|
| 编译器版本 | 这个二进制做什么 | `0.29.1` |
| 语言规范版本 | 语言是什么 | `1.0` |

编译器实现的是规范的一个**子集**。二进制到 0.27 不代表语言到 0.27；
语言到 1.0 也不代表规范里每一句都实现了——规范第 7 节的检查清单里有一整张
「计划中」的表。把两者印在一起，是唯一不需要加脚注就能说清这件事的方式。

---

## [0.29.1] - 2026-10-22

### 定了：选择器后面的 objective 一律加引号

`@s.arg1` 不支持，写 `@s."arg1"`。作者拍板，解析器原本就是这么做的，现在有测试钉住。

**注意语法在这里是不对称的**，不是笔误：

| 位置 | 写法 |
|---|---|
| 绑定 `extern ATK: int = @s.atk` | 不加引号 |
| 表达式 `X = @s."arg1"` | 加引号 |

绑定位置按定义读名字，读到行尾都是 objective，没有歧义；表达式位置选择引号形式，
因为 objective 名里可以有点。两种都接受会得到两种拼法，所以各留一种。

### 修掉：extern 绑定的 objective 名超长时报成编译器内部错误

```ggm
extern SEARCHMAX: int = num.SearchMax.math.operation1
```

这个名字 25 字符，1.12.2 在 16 字符处截断，会和任何共享前缀的名字撞车。源码层面没人检查，
第一个抱怨来自结构不变量：

```
-: error: t:f: block 0: objective "SearchMax..." exceeds 16 characters [E_IR_INVARIANT]
```

没有行号，用的是编译器内部的说法，读起来像编译器自己出了故障。

现在由 lower 在 `extern` 声明处检查，和 `obj` 声明用同一句话、同一个码：

```
p12_sort.ggm:12:1: error: objective "SearchMax.math.operation1" is 25 characters;
  1.12.2 truncates at 16, so it collides with any name sharing that prefix [E_OBJECTIVE_NAME]
```

一个错误只报一次：已经报过的名字随 IR 传到结构检查，那里跳过。语言服务器组这个改动
让参考语料上 27 条无行号的 invariant 收敛成 1 条带位置的诊断。

---
## [0.29.0] - 2026-10-22

0.28.0 之后又修了一批「编译通过但编辑器看不见」的问题。它们的共同形状是：
**程序是对的，索引是空的**，所以作者以为工具坏了。

### 修掉：函数、类、字段、常量在声明处点不动

`obj X` 记的是 `obj` 关键字的位置，不是 `X` 的位置；常量则完全没有位置
（`File.Consts` 是 名字 → 表达式 的映射，名字不是节点）。所以 hover 名字没反应，
跳转会跳到关键字。

现在 `internal/symbols` 拿到源码，在声明所在行上定位名字。常量按行首的 `const` 锚定，
注释里的同名提及不会误伤。

### 修掉：从类外面调用类方法解析不了

方法索引成 `Hero/heal`，调用写成 `t:c/Hero/heal`，两者对不上。而且**函数体是在声明
还没加完时就遍历的**，所以文件里靠后的类对它前面的函数不可见——这一条最值得记住：
符号索引必须两遍，先声明全部，再解释函数体。

### 修掉：`self.hp` 和 `decode ... [flags]` 看不见

`Field` 节点的位置在基对象上，字段名不是标识符节点；`decode` / `encode` 只存名字、
不存位置，而 `WalkStmts` 只上报表达式。Visitor 因此多了 `Field`、`NBTField`、
`NBTObjective` 三个回调，`DecodeStmt` / `EncodeStmt` 多了位置字段。

现在 nbt 语句里的每个名字都能 hover、能跳转、能查引用：字段、目标 objective、
以及 encode 的来源。

### 修掉：新文件在写 `namespace` 之前没有任何补全

服务器只为声明了命名空间的文件建索引。而新文件的第一件事就是写 `namespace`——
正是最需要帮助的时候什么都没有。现在无名空间文件按文件各建一份索引
（不共用：两个无关的新缓冲区互相应答比不应答更糟）。

### 修掉：一条畸形消息打死服务器

读循环把任何错误都当成终止。一个不是 JSON 的 frame 就让进程退出（实测：下一个请求
直接 EOF）。现在区分「这一条读不懂」和「管道断了」，前者跳过。

### 修掉：给未声明的名字赋值被静默接受

```ggm
fn f() { X = 1  ghost = 2 }
```

`ghost` 分到新局部变量，没人读。读侧早就拒绝了；写侧现在一致。三个语料 0 误报。

### 新增：全工作区搜索、重命名、快速修复

`workspace/symbol`、`textDocument/rename`（按解析结果匹配，不会碰到同名但无关的局部变量）、
`textDocument/codeAction`（给未声明的 objective 一键补声明）。

### 语料状态

| 语料 | 文件 | 错误 | 原因 |
|---|---|---|---|
| port-shunjie | 5 | 0 | |
| port-mofa | 45 | 1 | objective 名含 `:`，明确不支持 |
| port-jiu-mofa | 50 | 1 | `@s.arg1` 选择器后不加引号，待拍板 |

**两条都是设计决定，不是实现缺口。**

---
## [0.28.0] - 2026-10-22

编辑器和编译器第一次连起来。这个版本的主体是一个语言服务器，以及为了让
它能工作而修掉的三个 bug —— 其中一个让编辑器的诊断**全部**失效。

### 新增：语言服务器与查询模式

`ggmls` 提供诊断、hover、跳转、补全、大纲、引用、签名提示、重命名、快速修复。

这些能力共用 `internal/symbols`（AST → 声明 + 解析结果），编译器也暴露了同一个索引：

```sh
ggm -query hover    -at a.ggm:9:13  a.ggm b.ggm
ggm -query def      -at a.ggm:9:13  a.ggm b.ggm
ggm -query refs     -at a.ggm:9:13  a.ggm b.ggm
ggm -query complete -at a.ggm:9:13  a.ggm b.ggm
ggm -query sig      -at a.ggm:9:20  a.ggm b.ggm
```

**这是为了迭代速度，不是为了方便。** 只能靠点编辑器验证的功能，文案和区间会被
检查一次然后烂掉。有了查询模式，改一句 hover 文案是一条命令。

### 修掉：lower 层的诊断全部被编辑器丢弃

`lower` 从 AST 构造位置，而 AST 只有行列、没有文件名。所以每一条下降阶段的诊断
都带着空的 `File` 字段，服务器按文件分组时把它们全扔了：

```
$ ggmls   # 模块里有一个未声明的调用
publishDiagnostics a.ggm []
```

同一个文件 `ggm -check` 是报错的。只有 `E_PARSE` 正常，因为那是 driver 自己抛的。

测试没抓住它的原因是：那条测试断言「**不出现**某个码」，而空诊断列表完美满足这个断言。
现在有一条要求**语义错误**必须出现的测试，并且做过变异验证。

### 修掉：给未声明的名字赋值被静默接受

```ggm
fn f() { X = 1  ghost = 2 }
```

`ghost = 2` 分到一个新局部变量，没人读它 —— 1.12.2 照样执行，改了一个谁也看不到的分数。
读侧早就拒绝了同一个名字，写侧没有。现在两侧一致。

三个参考语料 0 误报。

### 修掉：1.12.2 允许的函数名解析不了

函数 id 允许 `[a-z0-9_-]`，所以 `random/1-10`、`a/1_5`、`fn 1a()`、`namespace x:ph/1`
都是合法名字 —— 而词法器会把它们切成多个 token（减号是运算符，下划线开头的不是标识符）。
路径段现在按**源码相邻性**合并；带空格的 `a/1 - 10` 仍然是运算符，有测试钉住。

### 增量解析

服务器每个文件缓存一份 AST，编译器通过 `Source.Parsed` 复用它。实测：

| | 之前 | 现在 |
|---|---|---|
| 5 文件模块，改一处 | 15 次解析 | 1 次 |
| 无改动的保存 | 重新编译 | 0 次 |
| 50 文件模块，每次按键 | — | 1.76ms |

数字由测试**数真实的解析调用次数**得出，不是看代码推断：缓存从不命中也会给出正确答案，
只是慢，所以只能靠数。

### 新增：`workspace/didChangeWatchedFiles`

在此之前服务器只认识启动时存在的文件。新建一个模块文件并从别处调用它，会被报成
「本模块没有这个函数」—— 在正确代码上的误报，正是这个服务器存在要避免的失败模式。

### 仍然存在的语法缺口（需要作者决定）

`@s.arg1` 这种选择器后不加引号的 objective 解析不了，规范要求 `@s."arg1"`。
两个参考语料因此各有一处无法解析。当初的理由是「读法不唯一」，但选择器不可能是
objective 名，所以点后面整段就是 objective 名 —— 这个理由站不住。是否放开属于语法决定。

---
## [0.27.0] - 2026-10-21

### 修掉 `-O2` 把常量物化包进 guard 的误编译

**运行时会算错答案，且只在 -O2 及以上。**

```ggm
if Ap.Amount <= -1 { Ap.Amount *= -1 }
```

`x *= -1` 降级成 `t = -1; x *= t`。内联把**两条**都包进了 guard：

```mcfunction
execute @s[score_t0.ggm=0] ~ ~ ~ scoreboard players set @s t2.ggm -1
execute @s[score_t0.ggm=0] ~ ~ ~ scoreboard players operation @s Ap.Amount *= @s t2.ggm
```

guard 不成立时 `t2.ggm` 保留上一轮的值，乘法用了脏数据。

实测数据包的 `rpg/monster/hurt/operation/light`（Defence=10, Amount=3）：
原库给 `Ap.Amount = 3`，-O0 给 3，**-O2 给 7**。

现在常量物化被提到 guard 外面——临时量在这个臂之外是死的，无条件填充总是安全的，
而且比包 guard 少一条命令。修复后 O0 与 O2 在三个用例上逐位一致。

### 修掉 `fn` 路径段不能是数字

数据包有 `rpg:skill/1/main`、`rpg:item/buy/cl/1` 这种命名，目录是槽位或档位。
拒绝数字开头的段让大约四分之三的文件无法表达。

```ggm
fn atk/1/main() { }
```

生成 `rpg/player/atk/1/main.mcfunction`。

### 编译器 panic 改成诊断

```ggm
if !(any<@e[tag=mob]> && A == 1) { X = 1 }
```

之前直接 panic。`!(e && c)` 是 `(没有实体匹配) OR (c 不成立)`，需要两条独立调用和一个
析取，`Predicate` 表示不了。只取补存在性那一半会**静默丢掉 c 那一半**——分支在作者
没写的输入上触发。

现在报错并给出能用的写法：`if !any<...> { } else if !(...) { }`。

### 失败的子降级不再发射畸形命令

`let a = str(1)` 正确报了 `E_STR_RUNTIME`，然后又发射 `scoreboard players operation @s l0 = `
（空操作数）。1.12.2 会拒收这一行，导致**整个函数文件加载失败**——一个不支持的 builtin
静默废掉周围所有代码。同时多出的那条 `E_IR_INVARIANT` 也没了。

### 一个永远不可能失败的测试

`TestTemporariesAreProgramWideUnique` 声称测「临时量不跨函数共享」，但它的源是局部变量
算术，降级后全是 `l0`/`l1` 槽位拷贝——**一个临时量都不产生**，循环走零次，断言永不执行。

变异验证：把 `Tmp` 改成恒返回 0，测试**仍然通过**。

现在用比较表达式（会产生临时量），找不到临时量就 `t.Fatal`，并且能抓住变异：

```
temp t0.ggm shared by ns:g and ns:f
```
## [0.26.0] - 2026-10-21

六路移植报告里剩下的缺口，逐条修。

### `fn` 名可以是路径

数据包按目录组织，目录是 id 的一部分：`rpg:player/atk/main_weapon`。
`fn` 名不接受 `/`，整个包的目录结构被压平，生成 `rpg:player/main_weapon`
——**与数据包其余部分引用的函数名对不上**。

```ggm
namespace rpg:player
fn atk/main_weapon() { }
```

生成 `rpg/player/atk/main_weapon.mcfunction`。

### 跨文件调用，以及空导出函数必须有文件

全限定名（含 `:`）能跨文件解析，裸名只在本文件 namespace 下找——数据包就是
这么写的。但**导出函数即使为空也必须生成文件**。

```ggm
// a.ggm
namespace rpg:item/buy
[export]
fn cl/main() { rpg:item/api/stat_start() }

// b.ggm
namespace rpg:item/api
[export]
fn stat_start() { temp1 = 0 }
```

之前 `main.mcfunction` **根本不生成**：`stat_start` 若为空，`call` 被 `silent()`
删掉，`main` 渲染后为空，`len(cmds)==0` 就不发文件。作者按提示接线一个不存在的
函数，1.12.2 当作静默 no-op。现在 `[export]` 函数一定生成文件（空文件是合法 no-op）。

### `Inventory()` 接受负槽位

副手在 1.12.2 是 `Slot:-106b`，护甲是 `100..103`。只解析无符号整数，整个
「副手道具」机制表达不出来。

```ggm
decode X from @s.Inventory(-106..-106)[f]
```

```mcfunction
scoreboard players add @s X 1 {Inventory:[{Slot:-106b,tag:{dat:{f:{0:1b}}}}]}
```

### `!cond` 可用

```ggm
if !(a >= 1) { X = 1 }
if !any<@e[tag=foo]> { X = 2 }
```

存在性测试有精确的 `unless` 形式，零开销：`function t:n/g$b1 unless @e[tag=foo]`。
其余条件物化成一个标志（set 1，内层成立时清 0，再测标志）。之前直接报错——
而 `if !any<...>` 正是作者写「没有任何东西匹配」的方式。

### `stats clear` 只要三个参数

`clear` 解绑统计、不指定接收分数。原来 set/clear 都强制四个，逼作者编一个假
objective，它会出现在生成物里。`set` 仍然要四个。

### `matches` 接受任意容器

`@s.ActiveEffects matches n"{Id:12b}"` 之前被拒。白名单里是 `decode` 关心的
三个容器，与 `matches` 无关。

### 明确不做的：objective 名里的 `:`

数据包用 `wp.passiveId1:1`，但 `:` 是 namespace 分隔符，放进标识符会污染 token 流
——每处 `a:b` 都要先判断是 namespace 还是名字的一部分。
「读位置」`@s."wp.passiveId1:1"` 已经能写且生成正确，声明用别名即可。
## [0.25.0] - 2026-10-21

六路并行移植参考数据包（`末法`，1963 文件 11348 行）后，按报告修掉的问题。

### 修掉 `boot` 从不调用 `init` —— 整个编译器不可用

后端的注释写着「发射这些声明是正确性要求，不是锦上添花」，然后把它们发进一个
没人调用的函数。`boot` 只调 `comptime` 和 `[load]`，`<ns>/init` 生成了却无人到达。

1.12.2 对不存在的 objective 写分数是**静默丢弃**。所以移植产物在运行期
「所有分数都写不进去，且没有任何报错」——而这正是 `obj` 存在的全部理由。

`boot` 现在第一件事就是 `function <ns>/init`。golden 同步更新（它此前把这个
静默失效固化成了期望值）。

### 修掉 `if/else` 的 else 守卫读到被污染的条件

```ggm
if arg1 == 1 { if arg1 == 5 { X = 1 } } else { Y = 2 }
```

两条命令共用同一个条件临时量，而 then 臂里的嵌套 `if` 会重算它：

```mcfunction
function t:f$b1 if @s[score_#t0=0]        // 先跑，覆盖 #t0
function t:f$b2 unless @s[score_#t0=0]    // 再读，已经是脏值
```

实测：arg1 = 1 / 5 / 7 全部执行 else 分支。
**else 从不依赖 then 跑过**，所以把它排到前面即可——条件仍是分支算出的那个。

### 接上 `&&` / `||`

规范 §4.2 写了降级规则，token 和优先级表都在，`lower` 里没接。而参考包
40+ 处 `@s[score_menu_min=1,score_menu=99]` **就是** `menu >= 1 && menu <= 99`。

```ggm
if menu >= 1 && menu <= 99 { X = 1 }
```

```mcfunction
execute @s[score_#t0_min=0,score_#t1=0] ~ ~ ~ scoreboard players set @s X 1
```

一条合取选择器，不是嵌套块。`||` 展开成每条 alt 一个受守卫调用。
边界实测：0→0、1→1、99→1、100→0。

### 修掉 `for e in entities<...>` 体内 `e.field` 写到假玩家

```ggm
for e in entities<@e[tag=target]> { e.money = 42 }
```

生成 `scoreboard players set e money 42`——**假玩家 `e`**，被扇出的实体没动。
`execute` 已经把 `@s` 绑到实体上了，但 `e` 走了 `holder.objective` 简写。
现在 `e.field` 映射到 `@s field`，读写两条路径都修了。

### objective 名长度按字符而非字节

`Count.残照花魄` 是 10 个字符、18 个字节，1.12.2 允许。三处检查都用了 `len()`，
于是所有含中文的 objective 名被拒——而报错还说「is 18 characters」。

### 临时槽改名 `#t0`，不再劫持作者的 `t1`

`isTempName` 认「t + 全数字」，而那也是普通的作者名字。`obj t1` 被当成临时量，
合并器改名后，赋值它的函数在 -O2 下**整个被删掉**。

临时量现在带 `#` 前缀，标识符不可能以它开头。

### `stats clear` 只要三个参数

`clear` 解绑统计、不指定接收分数。原来 set/clear 都强制四个参数，逼作者编一个
假 objective，它会出现在生成物里。`set` 仍然要四个——否则那条命令什么都不写。

### `matches` 接受任意容器

`@s.ActiveEffects matches n"{Id:12b}"` 之前被拒。白名单里是 `decode` 关心的
三个容器，与 `matches` 无关。

### 新增 `-ir`

打印优化后的 IR。没有它，上面几条「形状对了但语义错了」的 bug 只能靠猜。
## [0.24.0] - 2026-10-21

### 新增 `-ir`：打印优化后的 IR

从生成的 `.mcfunction` 反推 CFG 形状是猜。合并过的块和内联过的块产出的命令很像，
但两者决定了「这个优化是不是 bug」。

```
$ ggm -ir -O 2 x.ggm
program t:a: 1 funcs, 3 consts, 2 objs
  fn t:a/f [exported]
    b0:
      copy @s t0 = @s x
      arith @s t0 -= #1 ggm.c
      guarded [t0 == 0] set @s y = 1
      -> jump b1
```

每个块、每条 op、每个终结符都打印，`Raw` 连 `Args`/`Writes`/`Reads` 一起。

### 修掉 `if` 的代码膨胀

这是移植实测里最严重的问题。三个独立的单命令 `if`：

```ggm
if x == 1 { y = 1 }
if x == 2 { y = 2 }
if x == 3 { y = 3 }
```

**之前：7 个文件 23 行。现在：3 个文件 12 行。** 数据包同一段是 9 行。

三个原因叠在一起：

1. `inlineLeafArms` 只处理以 `Return` 结尾的臂。`if` 后面还有语句时臂是 `Jump`，
   所以一个都内联不了。
2. `canInline` 排除 `Raw`，而 `tellraw`、`scoreboard tag`、`kill` 全是 `Raw`——
   也就是作者真正会写的那些命令。排除的理由是「可能有副作用」，但 `Guarded`
   只在条件成立时执行，副作用发生的次数和之前完全一样。
3. 内联后留下 `branch c then=b else=b`——两臂指向同一个块。后端据此渲染出
   `function b if c` 和 `function b unless c`，**把同一个块调用了两次**，
   每个 `if` 多一行。

第 3 条是真正的 bug，不是膨胀：条件已经不选目的地了，却还在做条件调用。

实测：`rpg/projectile/player/cycle` 从 16 文件 46 行降到 **3 文件 26 行**
（数据包 1 文件 21 行）。


### 修掉嵌套 `if` 的答案反转

**这是移植实测里最严重的 bug：不崩溃、不报错、答案相反。**

```ggm
if a >= 1 {
    if a == 1 { b = c }
}
```

两个条件都编译到同一个临时量 `t0`，内联后变成：

```mcfunction
guarded [t0 >= 0] copy t0 = a
guarded [t0 >= 0] arith t0 -= 1
guarded [t0 >= 0] guarded [t0 == 0] copy b = c
```

第一条 guard 用外层的值通过，然后把 `t0` 覆盖成内层的值。后面的 op 声称受外层
guard 保护，实际测的是内层。运行时 `a = 0` 进了分支、`a = 1` 没进——**完全反了**。

修法：内联前检查臂是否写入条件读的 objective。写了就不内联。改名也能解决，
但那需要活跃区间分析，这个 pass 不做。

实测验证：a=0 → b 不变，a=1 → b=7，a=2 → b 不变。

### 修掉 `Guarded` 里的 objective 不声明

`usedObjectives` 里有 `case ir.Guarded: // handled below by recursion`——**没有那个递归**。
只出现在 guarded op 里的 objective 从不声明，1.12.2 静默丢弃写它的命令。

注释描述的是意图，代码没实现。现在真的递归了。
### `matches` 的容器不再限于三个

`@s.ActiveEffects matches n"{Id:12b}"` 之前报「expected a quoted objective name」——
白名单只有 `SelectedItem`/`Item`/`Inventory`，那三个是 `decode` 关心的容器，
不是 `matches` 该关心的。`matches` 只需要知道往哪指，模式本身说明了其余。
## [0.23.0] - 2026-10-21

### 新增：`<selector>."objective"` 读别的实体的分数

试表达 `rpg/player` 和 `rpg/monster` 时，卡在「读另一个实体的具名分数」上。
数据包里 `@p[tag=target] Ap.Amount` 这种写法到处都是（1438 条 `execute` 大半
在做这件事），而 GGM 只能扇出、读不到。

```ggm
Ap.Amount = @p[r=20,tag=target]."Ap.Amount"
```

```mcfunction
scoreboard players operation @s Ap.Amount = @p[r=20,tag=target] Ap.Amount
```

objective 名加引号，因为名字本身可以带点（`Ap.Amount`、`Time.day`），
`@p.Ap.Amount` 无法无歧义地切分。点和引号的形状沿用 `self.x` / `seed.x`。

**只能读。** 1.12.2 允许选择器在计分板运算的右边，不允许在左边；写成赋值会报错
并说明原因，而不是让解析器把那个点当成别的。要写别的实体，用 `for e in entities<...>`。

### 修掉两个错误答案

**1. 带点的 objective 名被拆开。** `Ap.Defence` 被拆成 holder `Ap` / objective
`Defence`，命令合法、能执行、写进一个没人读的分数。读写两条路径都有一段
`holder.objective` 简写回退，且都排在「已声明 objective」之前。

现在**声明优先**：数据包声明了几十个带点的 objective，而本仓库示例里只有两个
用简写。未声明的点号名仍然是简写，`seed.t = 9` 照常工作。

**2. 三个 `for` 循环体合并成同一个函数。** 体路径用 `len(Blocks)` 生成，而一个
体只含一条命令的循环不产生新块——于是同一个函数里的第二个、第三个循环拿到同样的
名字。`NewFunc` 同名是静默覆盖，所以最后写的那个体对所有三个循环生效。

现在用模块级单调计数器，路径是 `$e1`、`$e2`。

### 能表达什么

`rpg/player/hurt/main`（13 行）、`rpg/monster/hurt/operation/main`（15 行）、
`rpg/monster/hurt/death/main`（33 行）都逐条对得上：状态运算、条件分支、
`@p` 扇出、NBT 匹配、读别的实体的分数。
## [0.22.0] - 2026-10-21

### 新增 `decode … into`：多处累加

移植 `rpg/api/ApLoader/legacy` 时发现 `decode` 有个真实缺口。数据包的做法是：

```mcfunction
// reset 一次
scoreboard players reset @s legacy.AtkBase
// 然后 18 个文件各自累加
scoreboard players add @s legacy.AtkBase 1 {…Slot:9b…}
scoreboard players add @s legacy.AtkBase 2 {…Slot:9b…}
```

而每个 `decode` **都先清零**。三个槽位写成三条 `decode`，只有最后一条的贡献留下，
结果看起来「有值」——一个不报错、不崩溃、答案错误的 bug。

```ggm
[export]
fn reset() { legacy.AtkBase = 0 }

[export]
fn slot9()  { decode legacy.AtkBase from @s.Inventory(9..9)[AtkBase] into }
[export]
fn slot10() { decode legacy.AtkBase from @s.Inventory(10..10)[AtkBase] into }
```

`into` 在**句尾**。`decode a into b from c` 读起来像 `into` 引出了容器；
`decode <什么> from <哪里> into <怎么写>` 每个部分都在它该在的位置。

### 顺带修掉 `expectName` 的过度匹配

它的条件原本是 `Text != ""`，几乎接受任何 token——`@s`、`into`、`from`、任何
带文本的运算符。于是 `decode x into …` 把 `into` 当成 objective 名的一部分吞掉，
然后在三个 token 之后报「缺少 from」。

现在只接受 IDENT 或关键字，且关键字表是**遍历**出来的，不是第二份列表。

### 移植结果

`rpg/api/ApLoader/legacy`：数据包 19 个文件 126 行 → GGM 源 47 行 → 生成 119 行。

顺带发现数据包里 `Atkbase_9` 与 `AtkBase_9` 只差一个字母大小写，1.12.2 会把
函数名归一化成小写，所以它们是**同一个函数**；两个文件内容完全相同，也就是说
其中一个从未被调用过。这正是 `E_FN_COLLISION` 要抓的东西，手写时没人会发现。

### 实测

单槽位：`reset` + `load_slot9` 得到 3（位 0 + 位 1），正确。
多条目 Inventory：GoMcRuntime 的 NBT 匹配失效（`dataTag does not match`），
这是 runtime 的限制——同一条命令单独执行、或 Inventory 只有一个条目时都正常。
## [0.21.0] - 2026-10-21

### 新增 `detect` 和 `enable`

`detect` 探测一个方块，是参考数据包里**第三大**的用法（1093 行，占 9%）：

```ggm
if detect("~", "~-1", "~", "stone", "0", self) {
    can_stand = 1
}
```

`enable` 是 `trigger` 的另一半。1.12.2 对没 enable 过的 objective 会**静默**
拒绝 trigger 写入，所以这两条必须成对出现。

### `detect` 降级成标志位

1.12.2 没有布尔表达式，也**没有 `unless detect`**——探测只能写成「条件成立时
执行一条命令」。所以它降级成一个标志：

```mcfunction
scoreboard set @s t0 0
execute @s ~ ~ ~ detect ~ ~-1 ~ stone 0 scoreboard players set @s t0 1
function …$b1 if @s[score_t0_min=1]
```

标志位是**必要**的，不是浪费：它让补集可表达。没有它，`detect` 只能写
「成立时做 A」，写不出「不成立时做 B」。

### 修掉三个真实的优化器 bug

`detect` 一接进去就暴露出三个互相遮掩的缺陷，都是**在 -O0 正确、在 -O2 错误**：

**1. `ir.Raw` 对分析不可见。** 它是一段字符串，`Defs`/`Uses` 都返回空。
于是一条往 `t0` 写值的命令，在活跃性分析里**不存在**。修法是让 `Raw` 显式声明
它读写的位置，而不是去解析命令文本。

**2. `Raw` 的写被当成无条件 kill。** `execute … detect …` 是**条件写**：
条件不成立时旧值保留。当成无条件写会让死存储消除删掉初始化标志的 `set 0`，
标志于是保留上一 tick 的旧值，分支读到过期答案。这和 `Guarded` 的情况一样。

**3. 位置被写进命令文本，改名时改不到。** 探针把标志的 objective 名
`fmt.Sprintf` 进命令字符串，而 `CoalesceTemps` 只改名结构化的 `Loc`。
于是 op 说自己写 `t0`，命令里还写着 `t1`，分支读的那个恰好没人写。

修法是给 `Raw` 加 `Args`：位置以 `%s` 占位，**渲染时**才拼进文本。
这是唯一能让一条 Raw 命名槽位又扛过改名的办法。

> 三个 bug 都不报错、不崩溃，只产出错误答案，而且只在 -O2 出现。
> `detect` 是第一个把位置写进 Raw 文本的特性，所以它一次性踩中了三个。

### 实测

GoMcRuntime 上：脚下是空气时 `air_hit=1`，脚下不是石头时 `stone_hit` 未设置——
两者互斥，正是 `detect` 应有的行为。四个优化级别输出一致。
## [0.20.0] - 2026-10-21

### 补齐命令：20 条，一张表

上一版加了 5 条命令，每条一个 `case`。这一版补到 20 条，并把它们收进一张表：

```go
var commandBuiltins = map[string]commandSpec{
    "give":         {verb: "give", targetAt: 0, minArgs: 2},
    "playsound":    {verb: "playsound", targetAt: 2, minArgs: 3},
    "replaceitem":  {verb: "replaceitem", targetAt: 1, minArgs: 3},
    ...
}
```

表里只有三列：动词、**哪个参数是选择器**、最少参数个数。这些命令是同一个形状
——`<动词> <参数...>`——而编译器唯一需要知道的结构就是选择器在哪。

新增的 15 条：`give` `clear` `replaceitem` `spreadplayers` `spawnpoint`
`particle` `tellraw` `title` `playsound` `advancement` `trigger` `stats`
`tag_add` `tag_remove` `say`。加上原有的 `summon` `entitydata` `effect`
`tp` `kill` `cmd`，覆盖了参考数据包里 1964 个函数用到的绝大部分命令。

### 参数位置照实际写法核对，不是照命令手册

这一步连错了三次，每次都生成一条看着合理、1.12.2 却会在运行期拒掉的命令：

| 命令 | 猜的 | 实际 |
|---|---|---|
| `playsound` | 选择器第 0 位 | 第 2 位（**最后**） |
| `replaceitem` | 选择器第 0 位 | 第 1 位（`entity` 之后） |
| `advancement` | 选择器第 0 位 | 第 1 位（动作词之后） |

改法是照着参考数据包的实际用法核对，而不是照着命令名猜。这也是为什么现在
每一条都有一条断言**整行输出**的测试——只断言前缀的话，这三种错误全都通不过
眼。

### `stats` 不进表

它是唯一把同一个选择器写两遍的命令：

```mcfunction
stats entity @s set SuccessCount @s tmp
```

一次是「绑定谁的数据」，一次是「写进谁的分数」。塞进表里需要一条「重复这个
参数」的规则，那比一条命令该有的分量更重，所以单独处理。

### 仍不支持

世界编辑类命令（`setblock` `fill` `clone` `blockdata`）没有内置。数据包驱动
的是实体和分数——1964 个真实函数里没有一条写方块。要发它们用 `cmd`。
## [0.19.0] - 2026-10-21

### 破坏性：`emit` 改名 `score`，`emit_line` 改名 `cmd`

两个名字长得很像，做的却是完全不同的事，而且**最通用的那个名字被最专用的
那个占了**：`emit` 只能写一个常量分数，真正能发任意命令的叫 `emit_line`。

| 之前 | 之后 | 做什么 |
|---|---|---|
| `emit(holder, obj, value)` | `score(holder, obj, value)` | 写一个常量分数 |
| `emit_line(text)` | `cmd(text)` | 发一整条命令 |

`cmd` 现在是**一个名字两处使用**：编译期从常量拼命令，运行期是源码里的
字面量。「发一条任意命令」在语言里只有一个名字，读者不必先判断自己在哪个
上下文里。

`emit` 也因此不再是关键字，退回普通标识符。

### 新增：`summon` / `entitydata` / `effect` / `tp` / `cmd`

```ggm
summon("armor_stand", n"{Tags:[\"mob\"],Marker:1b}")
entitydata(self, n"{Health:1.0f}")
effect(self, "speed", 10, 1, true)
tp(self, "100", "64", "-20")
cmd("particle smoke ~ ~1 ~ 0.1 0.1 0.1 0 5")
```

**`summon` 是 `class` 缺的那一半。** 类说「一个实例就是一个带 tag 的实体」，
但在这之前语言里没有任何东西能创建那个实体——第一个实例只能靠程序外面的
命令方块造出来。

### `tp` 现在能写绝对坐标

旧写法替作者加 `~`，所以**只能**表达相对移动：

```ggm
tp(self, "-2.5", "0", "0")   // 旧：生成 tp @s ~-2.5 ~ ~
tp(self, "~-2.5", "~", "~")  // 新：同上，但波浪号是作者写的
tp(self, "100", "64", "-20") // 新：绝对坐标，以前写不出来
```

让波浪号可见，每个轴多一个字符，两种形式就都能说清自己是什么。这也正是
1.12.2 自己的区分方式，中间不再有可错的翻译。

### `n"..."` 变成普通表达式

以前它只在语法专门写死的地方合法（`matches` 条件、`nbt` 声明），于是 NBT
编解码器**够不到命令**：能解码一件已经存在的物品，却没法给出一件。

现在它是普通表达式节点，可以出现在任何参数位置；解析期仍然校验合法性。

### 参数只能是字面量，这是 1.12.2 的边界不是实现的边界

```ggm
summon("pig", hp)   // E_ARG
```

命令是文本，1.12.2 没有任何语法能把运行期的分数拼进文本里。接受一个 `int`
然后写它此刻的值，会产出一条看起来正确、却忽略此后每一次变化的命令。

所以 `E_ARG` 的措辞说的是边界而不是语法：作者该知道为什么这条路不存在。

### `cmd` 仍然过校验

它绕过内置表，但**不绕过** `mcfunction.Validate`：渲染出的那一行仍然要过
选择器参数白名单，所以 `score_x>=1` 这种 1.12.2 不认识的键还是会被抓出来。

### 实测

`summon` 在 GoMcRuntime 上跑通（三 tick 后 `born=3`）。带 NBT 的 `summon`
**在函数体内**被 runtime 当成数字解析而失败，直接执行同一条命令却成功——
这是 runtime 的限制，生成结果逐字正确。
## [0.18.0] - 2026-10-21

### 破坏性：`on` 家族删除，入口变成声明上的属性

```ggm
on load { seed.ready = 1 }
on every 5s { counter = counter + 1 }
[selector(@e[tag=mob])]
class Monster {
    on spawn { hp = 40 }
    @tick fn tick() { hp = hp + 1 }
}
```

现在是：

```ggm
[load]
fn on_load() { seed.ready = 1 }

[every("5s")]
fn heartbeat() { counter = counter + 1 }

[selector(@e[tag=mob])]
class Monster {
    var hp: int = 40

    [spawn]
    fn spawned() { hp = 40 }

    [every("1tick")]
    fn tick() { hp = hp + 1 }
}
```

改动有四处，每一处都在**减少**语言里的东西：

| 之前 | 之后 | 少了什么 |
|---|---|---|
| `on load` / `on tick` / `on every N` | `[load]` / `[every("N")]` | `on`、`load`、`tick`、`every` 四个关键字 |
| `#[wrapping]` 与 `[selector(...)]` 两套括号 | 一套 `[a, b(...)]` | 一个词法单位 `#` |
| 时长是词法单位 `DURATION` | 字符串字面量 `"5s"` | 整个 `DURATION` token、单位表和 `unitAhead()` |
| `@tick fn` 方法 | `[every("1tick")] fn` 方法 | `@tick`，以及方法专用的一套语法 |

属性是**遍历的**，不是内联的：编译期读列表，按名字分派。加一个属性不改语法。

### `[death]` 删除

它从来没能工作过：1.12.2 无法观察一个已经消失的实体，唯一正确的实现需要影子
实体。留着它意味着一个永远报错的属性——一个**问题**，不是功能。
现在写 `[death]` 报 `E_ATTR_UNKNOWN`，和拼错一样。

### 周期的身份是 tick 数，不是写法

```ggm
[every("5s")]   fn slow() { }
[every("100ticks")] fn also_slow() { }
```

两个声明，一个周期：一个计数器 `p.100`、一个调度器 `every/100t`，两个函数都
在里面。之前按写法命名，会生成两个计数器互相漂移——而各自单独看都正确。

调度器名从 `every/5s` 变成 `every/100t`，计数器从 `p.5s` 变成 `p.100`。

### 周期链在模块级计算

之前每个文件各自算链，所以 `a.ggm` 和 `b.ggm` 里都写 `[every("5s")]` 会各生成
一个 `p.100`，两个调度器抢同一个计数器。现在周期和 `boot` 一样是模块级事实：
收集所有文件的周期，按 tick 数合并，再统一生成时钟。

### `[load]` 不再占用 `<ns>/load`

入口函数保留作者写的名字。之前它被强制改名为 `<ns>/load`，于是同一模块里两个
`on load` 必然撞车（`E_FN_COLLISION`），而那个名字也正是用户函数可能想用的名字。
现在 `boot` 按名字调用每一个 `[load]` 函数。

### `1tick` 内联进 `on_cycle`

计数到 1 的计数器是两条废命令加一个恒真判断。`[every("1tick")]` 的函数体现在
直接出现在 `on_cycle` 里，没有 `p.1`，也没有 `every/1t`。

### 同时触发时取交集

`[load, every("5s")]` 的函数**没有** `@s`：两种触发上下文里有一种没有，交集就
是没有。这是纯局部规则，不需要调用图分析。

### 新增诊断

| 代码 | 触发 |
|---|---|
| `E_ATTR_UNKNOWN` | 属性不在表里（`[death]`、`[wraping]`） |
| `E_ATTR_DUP` | 同一属性写了两次（`[every("1s"), every("5s")]`） |
| `E_EVERY_ARG` | `every` 的参数不是时长字符串字面量 |
| `E_LOAD_PARAMS` | `[load]` 函数带参数 |

### 内部

- `syntax.OnDecl`、`syntax.ClassEvent`、`Method.Tick`、`FuncDecl.Wrapping`、
  `FuncDecl.Export` 全部删除，统一成 `[]Attr`。
- `PeriodEntry` / `SpawnEntry` 成为 `lower.Result` 的一部分，由 driver 合并。
- 时钟生成（`cycle` / `make_clock` / `on_cycle` / `every/<n>t`）从 `lower` 搬到
  `driver`，因为它需要看见整个模块。
- `syntax.ParseDuration` 是时长换算的唯一实现。
## [0.17.0] - 2026-10-21

### 破坏性：入口函数没有执行者，`on tick` 被删除

```ggm
on tick { counter = counter + 1 }
```

编译成：

```mcfunction
scoreboard players operation @s counter += #1 ggm.c
```

而 `gameLoopFunction` 调用函数时**没有执行实体**，`@s` 什么都不指。1.12.2 对此
不报错、不警告：命令语法合法，直接丢弃。跑 3 tick，`counter` 根本不存在。

这不是一处笔误，是整片失效。同一轮实测里：

| 写法 | 生成 | 结果 |
|---|---|---|
| `on load { counter = 7 }` | `set @s counter 7` | 静默丢弃 |
| `on every 5s { }` | `function … if @s[score_p.100=100]` | 周期永不触发 |
| `on every 1tick { if A < B { HIT = 1 } }` | `function … if @s[score_t0=-1]` | **分支永不进入** |
| `extern SEED = seed.ticks` + `SEED = SEED + 1` | `@s t0 = seed ticks` | 全局变量也归零 |

根因是 `ir/builder.go` 的 `TmpLoc()` 与 `lower/stmt.go` 的 `loc()` 都把
`Holder` 写死成 `@s`。**IR 的每一处临时值都假定执行者存在。**

### 现在：`cycle` 是唯一入口，`@s` 由时钟实体提供

```ggm
on every 1tick { counter = counter + 1 }
```

生成三件套：

```mcfunction
# <ns>/cycle —— gameLoopFunction 指向这里。@s 空，只做两件事
function <ns>/make_clock unless @e[tag=ggm.clock]
execute @e[tag=ggm.clock,c=1] ~ ~ ~ function <ns>/on_cycle

# <ns>/make_clock
summon armor_stand ~ ~ ~ {Tags:["ggm.clock"],Marker:1b,Invisible:1b,NoGravity:1b,Invulnerable:1b,PersistenceRequired:1b}

# <ns>/on_cycle —— @s = 时钟实体，正常代码在这里
scoreboard players add @s p.1 1
function <ns>/every/1tick if @s[score_p.1_min=1,score_p.1=1]
```

时钟实体就是参考包里的 `TickTimer`，不是新发明。

### `on tick` 删除

它不是改名。`on tick` 看起来像「有执行者」，实际是「没有」——这个错觉正是它
必须消失的原因。报错信息直接给出替代写法：

```
on tick was removed: it ran with no executor, so every @s write in it was
discarded. Write on every 1tick, which runs inside the clock's fan-out and
has an executor
```

### `on load` 里的实体分数是编译错误

```ggm
on load { counter = 7 }          // E_NO_SELF_CONTEXT
```

```ggm
extern SEED: int = seed.ticks
on load { SEED = 12345 }         // 可以：假玩家在任何上下文都存在
```

编译器自己的寄存器（`t0`、`l0`）搬到假玩家 `#ggm.reg` 上，所以入口里仍能
做算术——只是**作者的**数据不能假装在实体上。

### 新增 `boot`：模块级加载入口

```ggm
# app.ggm
on load { seed.app = 1 }
# stdlib/math.ggm
on load { seed.math = 0 }
```

生成：

```mcfunction
# t:app/boot
function t:app/comptime
function t:app/load
function std:math/comptime
function std:math/load
```

此前每个文件各生成一个 `<ns>/load`，而用户只会挂自己那一个——**库的初始化
永远不会跑**，且没有任何提示。`boot` 是唯一需要接线的地方。

### 顺带修复：常量池从未被调用

`NeedsConstInit` 在 `driver.go` 赋值后没有任何代码读它，golden 里
`function <ns>/comptime` 出现 0 次。`init.mcfunction` 里的
`scoreboard players set #1234 ggm.c 1234` 从来没执行过，**所有常量读出来都是 0**。
这个 bug 一直被 `@s` 那个 bug 掩盖着。现在 `boot` 是它的调用点。

### 实测

```
scores:
  app/#app.1 = 1        ← comptime 常量池跑过了
  counter/R1 = 3        ← on every 1tick 跑了 3 次
  tick/seed = 100       ← on load 写假玩家成功
  p.20/… = 3            ← 周期链计数正确
entities:
  entity-1 armor_stand … tags=ggm.clock
```

---
## [0.16.0] - 2026-10-21

### 修复：`on every N` 没有相位门（规范已声称有）

```ggm
on tick { }
on every 1s { beats = beats + 1 }
```

此前 `on every 1s` 生成的是**普通函数**：调用几次就跑几次。规范从第一版就写着
「相位分配 + 编译器生成归零」，而 `lowerOn` 对 `every` 没有任何特殊处理。
**规范与实现不符，且实现是错的那一方。**

现在由 `on tick` 驱动：

```mcfunction
# tick.mcfunction
scoreboard players add @s p.20 1
function t:p/every/1s if @s[score_p.20_min=20,score_p.20=20]

# every/1s.mcfunction
scoreboard players set @s p.20 0
scoreboard players operation @s beats += #1 ggm.c
```

真机实测：45 次 tick，`on every 1s` 执行 **2 次**（第 20 与第 40 tick），
`p.20` 归零后重新累计到 5。

三个细节：

- **归零在入口最前面**，不是最后。`lowerSeq` 把提前 `return` 报成
  `noFallThrough`，归零放末尾会让「body 里有 return 的周期」**静默丢掉归零**，
  于是那个周期每 tick 都触发。
- **计数器名是 `p.<ticks>`**，不是 `p.<duration text>`。`p.1000ticks` 会超出
  16 字符上限被截断，两个周期就会共用一个计数器。
- **时长单位只认 `ticks` / `s` / `m`**，其它直接报错。排不了期的入口比没有入口更糟。

### 测试 102 → 104

- `TestOnEveryFiresOncePerPeriod`：45 tick 触发 2 次（真机）
- `TestOnEveryResetsBeforeAnEarlyReturn`：body 提前 return 时归零仍在第一行
- `TestOnEveryRejectsUnknownUnits`

删掉了 `TestOnEveryFiresOncePerCall`——它钉的是「无相位门」这个**错误行为**。
和一个已被修复的 bug 绑定的测试，修复之后必须一起删，否则它会一直要求回到错的语义。

### 未做：tick 分段与多周期链

参考数据包的 `main/timer` 还有两层这一版没有：

| 能力 | 参考包 | GGM |
|---|---|---|
| 周期链（0.1s 驱动 0.2/0.3/0.5s，0.5s 驱动 1s） | ✅ | ❌ 各自独立计数 |
| tick 分段（`1-5`/`6-10`/`11-15`/`16-20` 分派） | ✅ | ❌ |

分段的价值是把一个 20 tick 的循环切成 4 段、**每 tick 只做 1/4 的活**，
用来分摊命令预算。这一版只做到「周期真的按期触发」。

---

## [0.15.0] - 2026-10-21

### 新增：`comptime` 能生成任意命令

```ggm
comptime {
    for bit in 0..30 {
        emit_line("tp @s ~-" + strdec2(30 - bit, 1000) + " ~ ~")
        emit_line("scoreboard players add @s[score_temp4_min=1] EasyCoord:x " + strpow2(30 - bit))
    }
}
```

此前 `comptime` 只能 `emit` 一条 `scoreboard players set`（常量池用）。
生成一张命令表——比如坐标二分的 88 条 `tp`——只能手写。

三个新能力：

| 新增 | 用途 |
|---|---|
| `emit_line(text)` | 发射一整条命令，原样 |
| `strpow2(bit)` | `2^bit` 的整数字面量 |
| `strdec2(bit, scale)` | `2^bit / scale` 的定点小数，如 `strdec2(31, 1000)` = `"2147483.648"` |

`Emitted` 结构加了 `Raw` 字段，`ToIR` 相应地输出 `ir.Raw`。

### 修复：`comptime` 里的 `if` 被静默丢弃

`if` 掉进 `evalStmt` 的 switch default 直接消失——被它包住的行一条都没生成，
**没有任何提示**，而输出看起来是完整的。

实测：写了 `if bit < 22 { … }` 的 y 轴，生成结果里 `EasyCoord:y` 的行数是 **0**。

现在不支持的语句报 `E_COMPTIME_STMT`。生成器里的静默 no-op 比缺功能更糟：
缺功能会被发现，静默 no-op 不会。

需要按条件生成时把范围拆成多个循环，而不是写 `if`。

### 新增：`tp` 与 `stats` 内置

```ggm
tp(@s, "1.5", "0", "0")            // 相对传送，偏移是命令文本（GGM 没有浮点）
stats(@s, "SuccessCount", temp4)   // 把下一条命令的成功数写进分数
```

### 新增：objective 名可以含冒号

`EasyCoord:x`、`wp.passiveId1:1` 这类名字在 1.12.2 里合法且常见。
不能拼出来的库，替换不了现有的手写文件。

### 新增：`stdlib/coord.ggm`（**未验证，已知跑不对**）

把实体坐标读成 int32 定点数（1 = 0.001 格）。整个二分表由 `comptime` 展开：
x/z 各 31 位、y 22 位，共 84 条 `tp` 加 84 条权重累加。

**在 GoMcRuntime 上跑不对，已实测确认，不是推测：**

| 依赖 | runtime 的实际行为 |
|---|---|
| 传送超出世界边界时坐标被钳制 | 不钳制：`tp` 到 `1e30` 照常落在请求坐标 |
| `stats` 把成功数写进分数 | 不写：绑定后分数保持 0 |

两个机制都不成立，算法没有信息源。保留这份实现是因为**生成方式**有价值——
等 runtime 补上这两件事，它应当直接可用。

### 测试 99 → 102

`TestComptimeEmitLine` / `TestComptimeDecimals` / `TestComptimeRejectsUnsupportedStatements`。

---

## [0.14.0] - 2026-10-20

### 修复：`for` 的循环体被当成调用者的一个块

```ggm
for e in entities<@e[tag=x]> {
    d = m - 5
}
```

循环体是通过 `execute <sel> ~ ~ ~ function <body>` 进入的——一次**文本调用**，
CFG 里没有这条边。把循环体降级成调用者的一个块，它就**没有任何前驱**：
可达性分析把它删掉，`E_IR_INVARIANT` 报 `block 1 is unreachable`。

在 `-O2` 上更隐蔽：`E_IR_INVARIANT` 不报，只是循环体静默消失，
生成的函数体只剩循环之后的那句。

**修法**：循环体是一个**独立函数**（`Exported`），和 `@tick` 入口同一性质——
按名字可达，不靠 CFG 边。

### 新增：扇出体内按实体写分数

修完上面那条，`for` 的循环体里 `d = m - 5` 会以**被扇出的那个实体**为 `@s`。
实测：两个实体 `m = 42` / `100`，各自得到 `d = 37` / `95`。

这是 `sort_asc` 缺的最后一块能力——「每个实体量一下自己，再挑出量中的那个」。

一个使用上的注意：扇出体里写**裸名字**得到的是执行者的局部槽，不是实体的 objective。
要写实体的分数，那个 objective 必须先用 `obj` 声明。

### 新增：`stdlib/math.ggm` 的 `sort_asc` / `sort_desc`

排序键 10 / 40 / 30 / 20 → 名次 **1 / 4 / 3 / 2**，真机验证。

实现要点：

- 循环状态（轮次、当前最小值）放在假玩家 `sort.rank` / `sort.key` 上，
  因为 `while` 的预算挂在执行者 `@s`，而每轮都要对一整组实体扇出
- 距离 `temp1 = math - sort.key` 用 `for` 逐实体算，不能用广播写
  （广播写会让每个实体读调用者的临时槽）
- `temp1` 必须 `obj` 声明，否则扇出体里写的是局部

### 测试 97 → 99

`TestFanOutBodyRunsPerEntity`（真机，两个实体各自算出不同的值）和
`TestFanOutBodyIsNotPruned`（循环体不被可达性分析删掉）。

---

## [0.13.0] - 2026-10-20

### 修复：六个静默误编译，全部由**真机跑产物**发现

这一轮想给标准库加 `sort`，结果一路上撞到六个既有 bug。每个都不报错、
命令都能加载、症状只是一个错数字。

| # | 缺陷 | 症状 |
|---|---|---|
| 1 | `expr` 不认 `obj` 声明的名字 | `ticks = ticks + 1` 编译成 `ticks = l0 + 1`，计数器恒为 1 |
| 2 | `lvalueName` 不认 `a.b` | `seed.t = 0` 解析通过、静默写到局部 |
| 3 | `any<sel>` 渲染成 `execute <sel> ~ ~ ~` | 存在性测试变成扇出，循环体每个匹配各跑一遍 |
| 4 | `reduce<min>` 拿选择器当操作数 | `operation @s t < @e[…]` 被 1.12.2 **静默忽略**，reduce 恒返回种子值 |
| 5 | `withBudget` 把预算折进 `@e` 选择器 | 预算在 `@s`、选择器选别人，循环第一轮就退出 |
| 6 | 广播写的 objective 不被收集 | 写不存在的 objective，静默失败 |

**1 和 2 是同一个错误的两个方向**：写路径认 `obj` / `holder.objective`，读路径不认。
**3 和 5 也是同一处**：`ExistTest` 的注释早就写着「`function F if <sel>` 是存在性测试，
`execute <sel> ~ ~ ~ function F` 是扇出，两者不可互换」——而后端正是用后者渲染前者。

### 修复方式

- `expr`：`obj` 声明的名字按自身读取；`a.b` 解析成 `holder.objective`
- `lvalueName`：接受 `a.b` 形式的目标
- `renderBranch`：存在性测试渲染成 `function F if <sel>` / `unless`，
  附带 `@s` 侧条件时包一层 `execute @s[<conds>] ~ ~ ~`
- `reduce`：种子放假玩家 `#ggm.reduce`，用 `execute` 扇出逐实体比较
- `withBudget`：不再往里是选择器的谓词里塞 `@s` 的条件
- `usedObjectives`：新增 `objectivesInRaw`，从广播写等原生命令文本里提取 objective

### 新增：`entities<…>` 可作为表达式

`for e in entities<@e[tag=x]> { … }` 以前解析不了——`entities` 是关键字，
`parsePrimary` 不接受它。现在接受。

### 测试 91 → 97

新增 `regress_test.go` 六条，每条钉住一个缺陷。已逐条验证**回退修复后测试立刻红**
（例如回退 `withBudget` 会得到
`function t:b/spin$b1 if @e[tag=go,score_l1_min=1]`）。

golden 重签并逐行读过：`reduce` 从无效的单条 `operation` 变成
`execute` 扇出，`#2147483647` 常量不再需要，循环回边从
`if @a[…,score_l14_min=1]` 变成 `execute @s[score_l14_min=1] ~ ~ ~ function …`。

### 未完成：`sort_asc` / `sort_desc`

标准库里的排序**没有落地**，从 `stdlib/math.ggm` 移除（该文件回到 8 个函数）。
卡点不是 bug，是语言能力：循环体里要对一整组实体扇出，并在扇出体内写
「这个实体的字段」——目前没有对应写法。`examples/luo-math-full.ggm` 里的
`sort_asc` 同样依赖这个缺口，它的 golden 因此仍是一份**无法正确运行**的产物。

---

## [0.12.0] - 2026-10-20

### 修复：读一个 `obj` 声明的 objective 会分配新的局部

```ggm
obj ticks
on tick { ticks = ticks + 1 }
```

生成的是：

```mcfunction
scoreboard players operation @s ticks = @s l0      ← 读的是 l0，不是 ticks
scoreboard players operation @s ticks += #1 ggm.c
```

**写路径改了、读路径漏了。** `targetLoc` 会认 `obj` 声明，`expr` 的标识符分支
只看 extern、常量、类字段——于是读 `ticks` 落到了 `ctx.slot()`，分配一个
从没人写过的 `l0`。计数器永远停在 1。

真机步进抓到的，不是代码审查。

### 修复：`seed.t = 0` 静默写到局部

`lvalueName` 只接受 `x` 和 `self.x`，所以 `holder.objective` 形式的赋值目标
**解析通过、静默落到局部**。现在 `a.b` 形式原样交给 `targetLoc`，生成
`scoreboard players set seed t 0`。

这和 `extern SEED: int = seed.math` 是同一个形状——绑定和赋值用同一套写法。

### 规范：修正 6 处与实现不符之处

自查时逐行读出来的：

| 位置 | 问题 |
|---|---|
| 公理 3 | 还写着「带 `cap` 的有界自递归」 |
| 公理 2 | 说「三种『多』语法」但只列了两种 |
| 词法 | `p"..."` 还写着「从实体根起算」 |
| 关键字表 | 缺 `obj` `nbt` `bits` `enum` `decode` `encode` `over` `budget`，多出 `cap` |
| 语句清单 | 缺 `for i in 0..64`；「没有」那句把 `return` 列进了没有的清单 |
| 3.1 / 3.2 | 代码块用单反引号而不是三反引号 |

### 规范：`on every N` 的相位门从未实现

规范 3.3 声称「相位分配 + 编译器生成归零，整除 20 检查」。
`lowerOn` 对 `every` **没有任何特殊处理**——它就是个普通函数，调用几次跑几次。

没有实现它，因为**至今零用例**：示例里没有，参考数据包里也搜不到对应的节拍代码。
按「不为还没写过的项目设计语法」的原则，改规范而不是补实现，并把它列进
`docs/roadmap.md`，复活条件是「出现第一个真实用例」。

测试 `TestOnEveryFiresOncePerCall` **钉住当前的真实行为**（21 次调用触发 21 次），
这样规范和实现不会再无声地分叉。

### 新增：真机步进测试（2 条）

| 测试 | 验证 |
|---|---|
| `TestOnTickCounterAdvancesOnRealRuntime` | 在同一个世界里连调三次 `tick`，读回 1 / 2 / 3 |
| `TestOnEveryFiresOncePerCall` | 钉住 `on every` 当前无相位门的行为 |

写第一条时踩了一个语言事实：**函数必须经由执行者进入**。每个表达式的临时槽都在
`@s` 上，所以即使只读写假玩家全局的函数，没有 `@s` 也会静默不动。

### 测试 89 → 91。golden 未变。

---

## [0.11.0] - 2026-10-20

### 破坏性：`#[budget(N)]` 换成 `, budget <n>`

```ggm
// 旧
while i < n #[budget(64)] { }

// 新
while i < n, budget 64 { }
loop  i > 0, budget 32 { }
```

**逗号是这句语法的要点**：循环头说两件事——测什么，和最多跑多久。逗号把它们分开。
写成 `while i < n budget 64` 就没有这个分界，上界会被读成条件的延续。

（中间曾试过 `cap 64`，但那是在 GGM 已有的两个记号之间又加了第三个：
`<…>` 是构造的参数，`#[…]` 是可选的修饰，而 `cap` 什么都不是。）

`#[…]` 在 GGM 里是**属性**语法（`[wrapping]`、`[selector]`），含义是「可选的修饰」。
而预算是循环的**操作数**——漏了直接报错。必填的东西打扮成可选的，读者会以为可以省；
而省掉的后果是无限递归，1.12.2 不会报错，只会卡死服务器。

给迁移的人留了专门的消息：

```
error: the loop bound is now written ", budget <n>"; #[budget(N)] was removed
```

**golden 未变**：只换写法，预算的数值没动。两次改写（`cap` → `budget`）都验证过这一点。

规范新增 4.0 节，把 `while` / `loop` 的区别（判在前 vs 判在后）
和「1.12.2 没有循环，靠自调用」写清楚——此前规范只在语句清单里带过一行。

### 新增：`for i in lo..hi` 计数循环

`for` 现在有**两种来源，一个写法**：

```ggm
for i in 0..64 { }               // 计数：范围即上界
for e in entities<@a[tag=x]> { } // 扇出：集合即上界
```

两者都是「对一组东西各做一次」，而且都**构造上有界**，所以都不需要 `budget`。
在此之前，计数循环只能写成 `while i < 10, budget 10`——**同一个数写两遍**。

- 含两端：`0..3` 是 4 次；`lo > hi` 是 0 次
- 上界可以是变量（`for i in 1..n`），**只在进入前求值一次**
- 降级成有界自调用，和 `while` 同一套机制

### 实现时自己踩了这个项目存在的理由

循环条件最初写成 `score_t0=0`——**只给了上界**，在 1.12.2 里是 `t0 <= 0`。
空范围（`for i in 5..1`）里 `i - lim` 已经是负数，测试成立，循环一直跑到计数器回绕。

改成 `score_t0_min=-2147483648,score_t0=0`。**这个坑正是 GGM 存在的理由**，
而我在写一个本该让作者碰不到它的构造时踩了。真机测试抓到了。

### 测试 87 → 89

`TestCountForOnRealRuntime` 用 `n(n+1)/2` 独立核对求和，并逐一验证空范围
（`5..1`、`2..1`）、单次（`1..1`）、负数下界（`-5..1` = 7 次）。
`TestCountForAtEveryLevel` 保证四个优化级别一致。

---

## [0.10.0] - 2026-10-20

### 破坏性：`extern` 换成 `持有者.objective`

```ggm
// 旧
extern var    arg1: int @ "arg1"
extern global SEED: int @ "math" @holder "seed"

// 新
extern arg1: int = @s.arg1
extern SEED: int = seed.math
```

旧写法有三个问题：

1. **`@` 一行里干两件不同的事**——`@ "math"` 引出 objective，`@holder "seed"` 引出持有者。
2. **`@holder` 冒充选择器**：GGM 里 `@` 后面永远跟 `s`/`a`/`e`/`r`/`p`，
   没有 `@holder` 这个东西。
3. **`global` 与 `@holder` 冗余**：`global` 的全部含义就是「持有者不是 `@s`」。

新写法把持有者写在前面，和 1.12.2 自己的
`scoreboard players set <持有者> <objective>` 一致，而且和编解码的
`@s.Item` / `@s.slot(0)` 是同一个形状。`extern var` / `extern global`
两个关键字随之删除——它们的全部信息就是持有者。

**golden 未变**：这是纯语法糖，生成结果一字未动。这正是它该有的验证。

### 测试 87 → 87

9 个测试文件、规范、两个示例、标准库同步移植。

---

## [0.9.0] - 2026-10-20

### 新增：跨文件模块与 `stdlib/math.ggm`

命令行上的多个文件现在**链接成一个模块**：

```
ggm -out data/functions  app.ggm  stdlib/math.ggm
```

此前每个文件独立编译，跨文件调用报 `E_UNKNOWN_CALLEE`，所以标准库无法存在。
现在各文件保留自己的命名空间（合并成一个会让它们的 objective 互相覆盖），
在 IR 上合并，签名表跨文件可见。

**跨命名空间调用写全名**，和函数 id 一致：

```ggm
fn run() -> int { std:math/sqrt(144) }
```

新增 `[export]`：库函数在库内部看不到调用者，没有这个属性
每个都会报 `W_UNREACHABLE_FN`。

**约定：调用方放第一个。** 合并后只有一份 `<ns>/init` 和一份常量池，
它们归属命令行上的第一个文件。库放前面的话要运行的是 `std:math/init`。

### `stdlib/math.ggm`

`lcg_step` / `lcg_next` / `seed` / `rand` / `chance` / `is_happen_fix100` /
`is_happen_fix1000` / `sqrt`。全部在 GoMcRuntime 上验证：
`sqrt` 对 14 个值逐一比对 Go 的整数平方根，`rand` 验证落在区间内**且真的在变**
（种子不推进的话八次会返回同一个数）。

### 顺带修掉参考实现里的两个 sqrt bug

移植时发现 `luo:math` 的 sqrt 形状**算错**，而且两处都是静默的：

| 输入 | 参考实现 | 正确值 | 根因 |
|---|---|---|---|
| 99 | **10** | 9 | 收敛判据 `abs(x - prev) >= 1` 在 9/10 之间振荡，跑到预算耗尽 |
| 2147483647 | **2147483647** | 46340 | 起始值 `(x + 1) / 2` 里 `x+1` 在 int32 回绕 |

改成「y 不再比 x 小」的判据（牛顿法单调下降，到不动点即止），
起始值写 `x / 2 + x % 2`（同样上取整但不溢出）。
这个形状还顺带处理了 `n = 0`——参考实现写的是 `if n < 4 { return 1 }`，**sqrt(0) 返回 1**。

`internal/driver/semantics_test.go:18` 的注释早就记下了振荡这件事，
但 `examples/luo-math*.ggm` 一直没改。

测试 83 → **86**。golden 未变。

---

## [0.8.0] - 2026-10-20

### 新增：NBT 位域编解码（`n"…"` / `p"…"` / `nbt` / `decode` / `encode`）

```ggm
namespace rpg:api

obj wp.damage1
obj Eq.Defence

nbt atk_base = p"dat.atk_base" : bits 11
nbt defence  = p"dat.defence"  : bits 11

on load {
  decode wp.damage1 from @s.Inventory(0)[atk_base]
  decode Eq.Defence from @s.Inventory(100..103)[defence]
}
```

**与手写文件逐行一致**（实测）：`weapon/damage/1.mcfunction` 12 行、
`equip/defence.mcfunction` 45 行（1 次清零 + 4 槽位 × 11 位），生成结果完全同序同文。

设计要点（依据是对 `末法` 数据包的实测）：

- **NBT 是谓词，不是数据源。** `scoreboard players add … {NBT}` 的语义是
  「如果匹配就加」，所以「读」是对 w 个谓词做加权折叠。
- **两个动词而不是一个 `=`。** `decode` 成本 `1+位宽`，`encode` 成本
  **值域大小**，实测两者差 200 倍（字段 11 位 vs 真实值域 11 条）。
- **一个读取原语**：位梯子、恒等枚举、映射枚举、哨兵位全是它的缩写。
  哨兵由作者在 `decode` 块里追加一条 `set` case。
- **禁止 `over full`**：值域必须写死数字，`bits 14` 就是 16384 条命令。
- **容器用 1.12.2 自己的键名**（`SelectedItem` / `Inventory(n)` / `Item`）。

### 修复：后端校验器误判 NBT 参数（`selectorArgsIn`）

`selectorArgsIn` 原先在 `@` 之后**向前搜索任意 `[`**，而选择器的参数表必须
**紧跟**在选择器后面。于是

```
scoreboard players add @s wp.damage1 1 {Inventory:[{Slot:0b,…}]}
```

里的 `{Inventory:[` 被当成 `@s` 的参数表，`{Slot:0b` 被当成一个非法选择器参数，
**每一条解码梯子都被自己的编译器拒收**。

这是个既有的假阳性：GGM 此前从不发射 NBT 参数，所以没人碰到。
`decode` 是第一个发射它的特性，把它暴露了出来。
修法是只在 `[` **紧跟** `@s`/`@a`/`@e`/`@r`/`@p` 时才解析参数表。

### 新增诊断（4 个，全部已实现）

| 码 | 检查 |
|---|---|
| `E_FIELD_UNKNOWN` | `decode`/`encode` 引用了未声明的 `nbt` 字段 |
| `E_FIELD_BITS` | 位宽不在 1..30（`bits 31` 的顶位 `2^30` 仍在 int32 内，`31` 是边界外） |
| `E_ENCODE_RANGE` | `over` 的值域超出字段容量——高位会被**静默截断** |
| `E_FIELD_DUP` | 同一个字段声明两次 |
| `E_OBJECTIVE_UNDECLARED` | `decode`/`encode` 的目标没被 `obj` 或 `extern` 声明过 |

### 补全：枚举两种形式 + `matches` 条件

**枚举两种形式的实际产物**（此前只实现了、没验证）：

```mcfunction
scoreboard players set @s temp1 1 {SelectedItem:{tag:{dat:{spec:1b}}}}      // enum 1..4
scoreboard players set @s temp3 1 {SelectedItem:{tag:{Tags:["残响"]}}}      // enum { "残响":1 }
```

途中修掉两个自己写错的地方：

1. **枚举叶子多了一层复合**：位梯子需要 `spec:{0:1b}`（一层复合放下标），
   枚举是 `spec:1b`（值直接贴键上）。我一开始两种都套了复合。
2. **路径起算点没定清**：容器包装又加了一次 `tag:`，和路径里的 `tag.` 撞成
   `{SelectedItem:{tag:{tag:{Tags:…}}}}`。定死口径：**路径从物品根起算**，
   和文件里的键写法一致。这样 `tag.dat.atk_base` 与 `Count` 用同一条规则
   （`Count` 与 `tag` 平级，隐式根表达不了它）。

**新增 `matches`**——设计时我写的是 `has`（键存在），数了之后改成 `matches`（模式匹配）：

| 包内用法 | 次数 |
|---|---|
| `scoreboard players tag @s add <标记> {NBT}` | **19** |
| `testfor {NBT}` | **0** |
| `clear … {Tags:…}` | 302（消耗物品，与本特性无关） |

那 19 处匹配的是**值**（`{dat:{qlt:0b}}`），不是键存在，所以 `has` 这个命名是错的。
降级走数据包自己的办法：私有标记标签 + `scoreboard players tag add {NBT}` +
`execute [tag=…]` 分支，标记每次探测前先移除。

### 测试 71 → 83

其中一条是**真机往返**：用 `encode` 把 0 / 1 / 500 / 511 写进物品实体，
清零后用 `decode` 读回，在 GoMcRuntime 上比对。
把 `packBits` 故意改坏后该测试立刻红（三个值全部往返成 0），已确认。

golden 未变：既有示例不用这些语法。

---

## [0.7.0] - 2026-10-20

### 修复：复合赋值把运算符丢了

`x += e` 一直编译成 `x = e` —— **五种复合赋值（`+=` `-=` `*=` `/=` `%=`）全部退化成普通赋值**。

解析器一直是对的：它把运算符记在 `AssignStmt.Op` 里（`parser.go:861`），
AST 也有这个字段（`ast.go:78`）。**降级时从来没读它**——`lower/stmt.go` 的
`AssignStmt` 分支一律按 `=` 处理。

修法是把这个运算符**折成它缩写的那个二元表达式**，走已有的
`assignBinaryInto` 路径，而不是给复合赋值开第二条降级路径：

```
x += e   →   x = x + e
```

两条路径合并成一条，它们就不可能分叉。实测在 `-O0`..`-O3` 四个级别下，
`x += e` 与 `x = x + e` 的产物**逐字节相同**（含 `-O0` 那条冗余的自拷贝）。

### 为什么能活到现在

**示例代码里没有一个地方用复合赋值**——`luo/math` 全写的 `x = x + 1`。
所以 golden 的 diff 是**空的**，63 条测试没有一条碰到这段代码。

这也说明这次修复**不需要重签 baseline**：所有既有程序的生成结果一字未变。

### 新增测试（3 条，真机执行）

| 测试 | 钉住什么 |
|---|---|
| `TestCompoundAssignmentComputes` | 五个运算符各自算出正确的值，含负数（`-7 / 3 = -2`，向零截断） |
| `TestCompoundAssignmentAtEveryLevel` | `-O0`..`-O3` 四个级别数值一致 |
| `TestCompoundAssignmentMatchesLonghand` | 两种写法**逐字节相同**，四个级别 × 五个运算符 |

第 3 条是真正的不变量：只要有人给复合赋值开一条独立路径，它立刻红。
撤掉修复重跑已确认三条测试都会失败（`add(7)` 得 3 而非 10）。

测试总数 63 → **66**。

### 新增：`obj` 声明（已实现）

```ggm
obj wp.damage1
obj Eq.Defence "防御"
obj post_damage stat.damageDealt "造成伤害"
```

生成对应的 `scoreboard objectives add <名字> <criteria> [display]`。

实现前的真实状况：后端**已经**为每个被写入的 objective 自动发射声明
（`backend.go:60-64`），display 一律硬编码成 `"ggm"`。所以 `obj` 的价值不是「让
objective 存在」，而是三件编译器无法替作者决定的事：

1. **criteria**——`stat.damageDealt` 这类不是 `dummy`，原先根本表达不了；
2. **display 名**——作者写的名字要出现在游戏里，不是 `ggm`；
3. **名字归作者**——赋值给一个未声明的名字会**静默新分配一个局部 `l0`**，
   声明的 objective 于是永远没人写。这条是 `obj` 存在的主要理由。

### 新增诊断（3 个，全部已实现）

| 码 | 检查 |
|---|---|
| `E_OBJECTIVE_NAME` | 名字超过 16 字符——1.12.2 会截断，截断后前缀相同的两个名字**静默撞车** |
| `E_OBJECTIVE_DUP` | 同一个 objective 声明两次——看起来像「更新」，实际第二次 `objectives add` 失败且什么都不改 |
| `E_OBJECTIVE_CONFLICT` | 同时用 `obj` 和 `extern var` 绑定同一个名字——两种「谁创建它」的信念不能同时成立 |

测试 66 → **71**（新增 5 条）。golden 未变：`obj` 是新增语法，既有程序不用它。

### 规范：写入 NBT 编解码与 objective 声明（设计，未实现）

按「改规范先于改代码」，把 `docs/NBT-SYNTAX.md` 的设计写进了语法规范：

| 位置 | 内容 |
|---|---|
| 1. 词法 | `n"…"`（NBT 值，SNBT）、`p"…"`（NBT 路径） |
| 3. 顶层声明 | 7 种 → **8 种**，新增 `obj` |
| 3.4 | `obj` 的三种形态与 criteria 放行范围 |
| 4. 语句 | 6 种 → **8 种**，新增 `decode` / `encode` |
| 4.3 | NBT 编解码的完整语义：一个读取原语 + 它的四种缩写 |
| 7. 检查清单 | 新增 9 个「计划中」的码 |

**这些都是设计，编译器一行都没实现**——规范里已标注实现状态。
语言规范版本仍是 1.0：规范一直包含「计划中」的条目（`clamp`、`count` 同理）。

设计要点（依据是对 `末法` 数据包的实测）：

- **一个读取原语**：`decode` 是「一串谓词 → 操作 → 值」。位梯子、恒等枚举、
  标签枚举、哨兵位**全是它的缩写**。
- **两个动词而不是一个 `=`**：`decode` 成本是 `1+位宽`，`encode` 成本是
  **值域大小**，实测两者差 200 倍。藏进同一个 `=` 会让人以为对称。
- **禁止 `over full`**：`bits 14` 就是 16384 条命令，会撞 `maxCommandChainLength`，
  超限后静默丢弃。值域必须写死数字。
- **容量名用 1.12.2 自己的键名**（`SelectedItem` / `Inventory(n)` / `Item`），不另造词。

---

## [0.6.0] - 2026-10-20

### 改名：CMCF → GGM，项目名 GoGoMcfunction

从语言名到生成结果里的标识符，整套统一。

| 层 | 旧 | 新 |
|---|---|---|
| 仓库 / Go 模块 | `CelesteaMcfunction` | `GoGoMcfunction` |
| 语言 | CMCF | **GGM** |
| 源文件扩展名 | `.cmcf` | **`.ggm`** |
| 命令 | `cmcf` | `ggm` |
| 常量池 objective | `cmcf.c` | **`ggm.c`** |
| spawn 标记标签 | `cmcf.spawn.<类>` | **`ggm.spawn.<类>`** |
| 覆盖运行时的环境变量 | `CMCF_GOMC` | `GGM_GOMC` |
| 规范文档 | `CMCF-语法规范-v1.0.md` | `GGM-语法规范-v1.0.md` |

### 破坏性：已有存档里的状态会失效

`cmcf.c` → `ggm.c` 和 `cmcf.spawn.*` → `ggm.spawn.*` 是**写进存档的数据**：

- 常量池换了 objective，旧世界里 `#<常量>` 假玩家的分数读不到，第一次运行会重新初始化；
- 实体上的 spawn 标记换了标签名，**所有已存在的实体会被当成没 spawn 过**，
  `on spawn` 会对它们再跑一次。

升级后需要重跑一次 `<ns>/init`，并清理旧标签（或换一个干净世界）。
0.x 期间的破坏性改名正是次版本号的用途。

### 未受影响

**生成结果的结构一条都没变。** golden 的 diff 只有标识符替换，
命令数、文件数、优化行为全部一致（`-O2` 仍是 15 文件 124 条命令）。

---

## [0.5.0] - 2026-10-20

### 新增：选择器参数白名单进入编译期自检

后端原先只检查命令是否以 `/` 开头。选择器里的 `score_x>=1` 这类写法**没人拦**——
而 1.12.2 的选择器没有比较运算符：

```
score_x=N       意思是 x <= N
score_x_min=N   意思是 x >= N
```

所以 `score_x>=1` 不是「更窄的条件」，而是 **key 叫 `score_x>`、命令根本不认识这个参数**。
在函数文件里，一个解析不了的命令会让**整份文件**加载失败。

现在 `target.Target.SelectorArgOK` 持有 1.12.2 的参数白名单（`r`/`rm`/`score_<目标>`/
`score_<目标>_min`/`tag`/`type`/… 共 20 个），后端 `Validate` 对每条渲染出的命令逐参数校验，
不合法即报 `E_BAD_COMMAND`——在写文件之前拦下，而不是等到服务器上。

值也要校验：`score_x==0` 会在**第一个**等号处切分，把 `=0` 留作值，只看 key 会放过它。

### 顺带

- 新增 `internal/target/selector_test.go` 与 `internal/backend/mcfunction/validate_test.go`：
  合法与非法参数各一组，非法那组包含六种比较运算符写法。
- 现有生成结果**一条都没被拦**，这本身就是证据：golden 逐字未变。

---

## [0.4.0] - 2026-10-20

### 新增

- **`!=` 可以用了。** 它此前根本不存在：`target.CondFor` 只能返回**一个** Condition，
  而 `d != 0` 的真值是两段区间（`d <= -1` 或 `d >= 1`），结构上就表达不了。
  解析通过，一旦真的使用就报 `unsupported comparison`。

  `CondFor` 换成 `CondAlts`：返回**分支列表**而不是单个 Condition。补集不再由它计算——
  那一直是 `ir.Negate` 的事，两处各算一遍正是当初分叉的原因。

### 优化：`if` / `unless`

1.12.2 的 `function` 支持 `/function <name> [if <selector>|unless <selector>]`
（游戏语言文件 `commands.function.usage` 的原文，反编译的 `CommandFunction.execute`
也确实是比对 `"if"` / `"unless"`）。`unless <sel>` 是「该选择器匹配不到」，也就是
**选择器条件的补集**；而选择器只能表达合取。

所以当分支任一侧是**单个合取式**时，两侧各一条命令就够：一侧 `if`，另一侧 `unless`。
原先无条件「每条分支一个 `execute`」，三选一的预判要三条命令，现在两条：

```mcfunction
# 之前
execute @s[score_t=-1] ~ ~ ~ function b3
execute @s[score_t_min=1] ~ ~ ~ function b3
execute @s[score_t_min=0,score_t=0] ~ ~ ~ function b4
# 之后
function b3 unless @s[score_t_min=0,score_t=0]
function b4 if @s[score_t_min=0,score_t=0]
```

纯渲染层改写，语义不变：`unless @s[c]` 就是「c 不成立」，而且在当前执行上下文里跑，
和守卫调用解析到的 `@s` 是同一个实体。

`examples/luo-math-full.ggm`：`-O0` 130→129，`-O1` 128→127，`-O2/O3` 125→124。

### 测试

新增 `comparison_test.go`：六种比较运算符 × 各有序组合，真机执行，并在四个优化级别上
全部重复一遍。此前**没有任何测试覆盖比较运算符本身**。

---

## [0.3.0] - 2026-10-20

`class` 从「规范里写着、编译器不认」变成能用。它仍然是**这个语言存在的理由**：
「跨 tick 的实体状态」只有这一种被声明的写法，而它此前完全不可用。

七个缺陷，每一个都遮住下一个，所以整个特性看起来只是「暂时没人用」：

| 症状 | 后果 |
|---|---|
| `@tick fn` 解析失败 | 规范 §3.2/§3.3 的写法根本编译不了 |
| 字段名落成新局部变量 | 初始值设进一个再也不会被读的 objective |
| 兄弟方法调用解析到 `<ns>/方法名` | 调用了不存在的函数；而那个方法又因无人调用被删掉——两个错误互相掩护 |
| 没有任何东西调用 `@tick` 方法 | 类永远不会跑 |
| 类选择器解析后从未使用 | 即使能跑也扇出不到实例 |
| 方法没进签名表 | 参数个数不检查、ABI 分析看不见、可达性判错 |
| `on spawn`/`on death` 不解析 | 实体生命周期接不上 |

### 新增

- **`@tick fn`**（也接受裸 `tick fn`）→ `execute <selector> ~ ~ ~ function <ns>/<类>/<方法>`，
  在 `<ns>/tick` 里扇出。`on tick` 块与类 tick 方法合并进同一个入口——两个同名函数会在磁盘上互相覆盖。
- **`on spawn`** → 扇出到「匹配选择器且没有标记标签」的实体，处理器**先打标签再执行函数体**。
  提前打标签是因为函数体里的 `return` 会跳过尾部，否则该实体每个 tick 都会再 spawn 一次。
  1.12.2 没有实体创建事件，这是手写库用的同一种做法。
- **字段解析**：方法里的裸字段名解析到 `<类>.<字段>` 这个 objective，而不是新局部变量。
- **方法解析**：类内未限定的调用先解析到同类方法，再退回模块级函数。
- **方法签名进表**：参数个数检查、ABI clobber 分析、可达性判定都能看见它们了。
- **`@tick`/`on spawn` 的名字按需保留**：`fn Tick` 之类与生成入口归一化冲突时会被报出来。
- **`FanOut` IR 原语**：调用图分析能看见扇出边。漏掉它会把每个类的生命周期处理器都当成不可达删掉。

### 新增诊断

- `E_FIELD_COLLISION`：objective 名截断到 16 字符，类名前 6 字符相同的两个类会落在同一个
  objective 上，静默共用一个分数。
- `E_HOOK_UNSUPPORTED`：`on death` 有非空体。1.12.2 无法观察已经消失的实体，正确实现需要
  影子实体；假装能做到只会产出一个永不触发的处理器。空体是占位符，接受。

### 修复

- **`tag` 命令不存在于 1.12.2**。`tag_add`/`tag_remove` 原先发 `tag @s add`（1.13 才有的写法），
  而未知命令会让**整份文件**加载失败——同文件里其它命令一起失效。
- `tag_add` 的目标硬编码成 `@s`、标签从第 1 个参数读，所以 `tag_add(@a, "x")` 丢掉选择器。
  参数缺失时原先静默返回空串，现在报 `E_ARG`。
- `tick`/`spawn`/`death`/`load`/`every` 现在可以在声明和调用位置当名字用：规范自己的例子
  就把方法命名为 `tick`，而它是关键字。

---

## [0.2.0] - 2026-10-20

把「只有全局视角才有的事实」变成诊断。这一批**不新增语法**，也没有改变任何生成结果
（golden 逐字未变）——新增的全是编译期能看出来、而单文件工具看不出来的东西。

### 新增

- **E_UNKNOWN_CALLEE**：调用了本模块里不存在的函数。1.12.2 对此既不报错也不执行——
  调用指令合法、文件加载成功、运行期什么都不发生。一个拼错的名字买到的就是一条静默
  死掉的代码路径。参考项目里一个漏写命名空间前缀的调用，让整个随机数例程返回调用者
  return 槽里的旧值。
- **E_BUILTIN_UNIMPLEMENTED**：abs（作值用）、clamp、count 是语言拥有的名字，但本构建
  没在语法允许的每个位置实现它们。说「未知函数」会让人去找一个不该存在的声明。
  abs 在比较式左侧是实现的（代数改写，不产生调用），那里不会误报。
- **W_UNREACHABLE_FN**：声明了但本模块无人调用。参考项目的审计列出了 11 个，其中两个是
  活函数的逐字节副本。是警告不是错误——命令方块和 gameLoopFunction 能从编译器看不见的
  地方调用它，所以消息里直接写了这个出口。
- **-comments**（默认关闭）：每个生成文件加一行注释，写明函数 id 与声明位置。
  1.12.2 会跳过 # 开头的行，运行期零成本。

### 变更

- lower.Lower 多一个源文件名参数，函数在 IR 里记录声明位置。

---

## [0.1.0] - 2026-10-20

第一个 baseline。冻结的是**生成结果**，不是功能列表：`testdata/golden/` 里
存了每个示例在 `-O0`..`-O3` 下的完整输出，任何改动代码生成的行为都会在
diff 里显形。详见 [docs/BASELINE.md](docs/BASELINE.md)。

### 新增

- 编译管线：`syntax` → `lower`(CFG) → `optimize` → `backend` → `.mcfunction`
- 目标 1.12.2；`-O0`..`-O3`，级别表示**证明强度**而不是开关数量
- `comptime` 常量池、`class`（谓词 + 字段 + `@tick` 入口）、`on load/tick/every`
- 与 GoMcRuntime 的语义测试通道：`internal/sim` 通过 CLI 驱动真实 1.12.2 语义

### 修复：三个静默误编译

三个都命中 `luo/math` 示例，都产生**能加载、不报错、但算错**的程序：

- **参数被嵌套调用覆盖。** `a0..a(n-1)` 是实参寄存器，任何嵌套调用写实参时都会
  覆盖它们。`chance(scale, p)` 调 `rand(1, scale)` 时把 `a1` 改成了 `scale`，
  随后的比较把 `a1` 当 `p` 读。而且这是**传递**的：`lcg_next` 自己没有参数，
  但它把 1 个实参传给 `lcg_step`，所以它写了 `a0`——`rand` 的 `lo` 就是这么丢的。
- **局部变量按函数编号。** 局部变量是 `@s` 上的 objective，而 `@s` 是整条调用栈
  共用的一个寄存器文件：`lcg_next` 的 `l0` 就是 `rand` 的 `l0`。
  名字现在在模块内唯一，和临时变量同一条规则。
- **`CoalesceTemps` 三重错误。** 每个函数都从 `t0` 重新染色（两个函数共用 `t0`）、
  干涉图是**有向**的（着色看不见已占位的邻居）、活跃性只看直接后继。
  在 `chance` 里三个同时存活的值塌成一个 objective，`rand <= p` 变成恒真。

### 修复：把静默错误变成诊断

- `E_INT_RANGE`：字面量与 `comptime` 的 `emit` 值超出 int32 一律拒收。
  1.12.2 的计分板按 32 位**静默回绕**，所以 `const BIG = 3000000000`
  原先编译通过、运行得到 `-1294967296`，没有任何提示。前导负号在检查前并入
  字面量，否则会误杀 `-2147483648`。
- `E_FN_COLLISION` / `E_FN_RESERVED`：1.12.2 用 `MinecraftKey` 标识函数，
  **命名空间和路径都转小写**。`fn Foo` 与 `fn foo` 因此是同一个函数
  （`Foo() + foo()` 曾返回 4），而 `fn init()` 会覆盖编译器生成的目标声明函数，
  让整个程序失去全部 objective。
- 未知属性不再被静默接受。`[wraping]`（拼错）原先照样通过，因为解析器只读
  `attrs["wrapping"]` 一项。

### 变更

- **`while` 按 `if cond { loop cond { body } }` 降级。** 两者是同一个程序，
  这是 AST 级等价改写，不需要值编号。原本留着做循环旋转的 `rotateLoops` /
  `rotatePostTest` 已删除——它们会读错值、把守卫叉积成 4 条、让回边指向错误的块。
- **预算折进循环条件**，而不是单独测。直觉上省 1 条命令，实际省 2 条：
  预算分支需要两侧各一条守卫，然后判据还要自己的。
- **补集只有一份实现。** 它曾经有三份，其中两份已经分叉（发出没有任何分数能满足的
  `score_x_min=2147483648`）。现在统一在 `ir.Negate` / `ir.Simplify`。
- **补集做化简。** 否定产生笛卡尔积，而叉积会重复条件：
  `|d|>=1 AND budget>=1` 的补集出来 4 条，其中一条把同一条件写了两遍、
  另两条被它覆盖。四条变两条。
- 表达式直接算进目标槽（`(a+b)*c` 三条命令而不是四条）、调用结果不再中转。
- 清理类 pass 在值变换后再跑一遍——值变换会制造它刚好要清理的形状。

### 实测

`examples/luo-math-full.ggm`，静态 `-O2` 15 文件 / 125 条命令
（原手写库 17 文件 / 247 条）。动态执行 `sqrt(40000) = 200`：

| | 动态命令 | 失败守卫 |
|---|---|---|
| 起点 | 1531 | 21 |
| 合并预算 + 补集化简 | **896** | 21 |

`rand` 序列与一份独立的 32 位参考实现在四个优化级别下逐位一致。

### 已知不做

- 值域分析（`W_WRAP_UNPROVEN`、`W_DIV_ZERO`、字面量之外的溢出）
- 跨函数复用 objective（需要过程间活跃范围；当前收益不足以抵消风险）
- 类方法的非 `@tick` 入口（不可达，会被死代码消除）

判断依据见 [docs/roadmap.md](docs/roadmap.md)。
