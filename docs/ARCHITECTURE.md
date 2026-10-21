# GGM 编译器架构

> 目标读者：**未来的我们**。本文回答「加一个特性该改哪里」「为什么这里要分这么多层」。
> 写于 v1.0 语法落地、`luo/math` 全量移植之后。

---

## 1. 为什么需要这份文档

编译器一开始是 1190 行的 `emit.go`，边写边验证。当语言只有 7 种声明、6 种语句时这样能跑通。
但语言会变复杂，而复杂度会以三种方式惩罚错误的架构：

| 症状 | 根因 |
|---|---|
| 加一个特性要改五个文件 | 职责没有边界 |
| 优化写了但不敢开 | 没有 analysis 支撑，改动无法证明安全 |
| bug 只在特定组合下出现 | IR 太弱，中间状态无法被单独检查 |

`luo/math` 移植过程中真实抓到的 4 个 bug（空 objective、补集只返回一半、德摩根分配错误、
临时变量跨函数冲突）**全都是分层之后才暴露的**。这不是巧合：IR 让每个阶段的输出可被检查。

---

## 2. 分层与依赖方向

```
                        cmd/ggm             CLI：参数、落盘
                            │
                        driver               唯一编排点
      ┌──────────┬──────────┼──────────┬──────────┐
      ▼          ▼          ▼          ▼          ▼
   syntax    comptime     lower     optimize   backend/mcfunction
      │          │          │          │          │
      └──────────┴────┬─────┴──────────┘          │
                      ▼                           │
                    ir ◄──────────────────────────┘
                      ▲
                    target         1.12.2 能力表（唯一版本事实来源）

        mc      命令模型（选择器区间 API 在此，但生产路径零引用，见 §10）
        diag    统一诊断（所有层共用，不依赖任何层）
```

**依赖规则（可机械检查）**：

| 包 | 可以依赖 | 绝不可以依赖 |
|---|---|---|
| `diag` | 无 | 任何东西 |
| `mc` | 无 | 任何东西 |
| `ir` | 无 | syntax、mc、target |
| `target` | `ir` | syntax、mc、backend |
| `syntax` | `diag` | ir、mc、target、lower |
| `comptime` | `syntax` `diag` `ir` | lower、backend、mc |
| `lower` | `syntax` `ir` `target` `diag` | **mc**（不许写命令文本）、backend |
| `analysis` | `ir` | 其他一切 |
| `optimize` | `ir` `analysis` | syntax、mc、target、backend |
| `backend` | `ir` `mc` `target` | syntax、lower、comptime |
| `driver` | 全部 | 无（它就是编排） |

两条最重要的禁令：

1. **`lower` 尽量不写命令文本。** 它只产生 IR。想知道 1.12.2 的选择器怎么写？问 `target`。
   **（现状：这是目标，不是事实。）** `lower/` 里目前有 **15 处** `ir.Raw{Text: ...}`
   直接拼 1.12.2 命令文本（`comptime/` 1 处、`driver/` 4 处），绕过 `target`
   的任何检查。详见 §10。
2. **`backend` 不认识语言。** 它只认 IR。加一门新前端语言，backend 一行不改。

---

## 3. IR 设计

### 3.1 为什么是 CFG 而不是嵌套树

早期 IR 是 `[]Op`，其中 `Guard{Then []Op}` 嵌套。这在三个地方撞墙：

- **活跃性分析**：不知道控制流怎么汇合，无法判断一个槽在某个点是否还活着
- **死代码消除**：无法表达「这个块不可达」
- **寄存器复用**：没有活跃区间，只能全程序唯一编号（当前就是这样，导致槽位膨胀）

所以 IR 是**控制流图**：函数 = 基本块列表，每块以终结符结束。

```go
type Function struct {
    Path     string
    Params   []string
    Blocks   []*Block   // Blocks[0] 是入口
    Exported bool
}

type Block struct {
    ID   BlockID
    Ops  []Op          // 只有非终结操作
    Term Terminator    // 唯一出口
}

type Terminator interface{ termNode() }

type Jump struct{ Target BlockID }
type Branch struct {                   // 条件跳转
    Cond Predicate
    Then BlockID
    Else BlockID
}
type Call struct{ Path string }        // 尾调用：调用后函数返回
type Return struct{}
```

**关键约束**：`Block.Ops` 里不允许出现控制流。所有分支都是终结符 + 块边界。
这条约束让「一个块 = 一段直线命令」成为不变量，backend 可以直接把它渲染成一个 `.mcfunction`。

### 3.2 Predicate 是 DNF

1.12.2 选择器能表达合取（逗号分隔），不能表达析取。
所以条件表示为**析取范式**：

```go
type Predicate struct {
    Alts  [][]Cond   // 外层 OR，内层 AND
    Exist *ExistTest // 或：O(1) 存在性测试（any<sel>）
    Block *BlockTest // 或：方块探测（detect(...)）
}
```

`==` 的补集是两个不相交区间，所以取反必须产生**两个 alternative**。
这个不变量由 `predicate_test.go` 锁死——它是全项目最容易写错的地方。

**`Exist` 和 `Block` 是两种「不是分数」的测试。** 它们各自只能和**合取**
搭配（1.12.2 的 `function F if <sel>` 本身就是合取），所以 `&&` / `||` 遇到
它们一律编译期报 `E_COND`，而不是丢掉一个。

**`ir.Cond` 没有 holder 字段。** 它只有 `Obj`，所有条件都隐含作用在 `@s` 上——
这和 `ir.Loc`（有 `Holder` + `Obj`）不一致。`analysis.PredUses` 因此硬编码
`ir.Loc{Holder: "@s", ...}`。

### 3.3 Loc：把「在哪存」和「存什么」分开

```go
type Loc struct {
    Holder string  // "@s" / "#5" / "@a[...]"
    Obj    string  // objective 名
}
```

所有 op 用 `Loc` 而不是两个裸字符串。这样寄存器分配、别名分析都只处理一种类型。

---

## 4. 分析框架

优化不能靠猜。`analysis` 包提供可复用的数据流分析，每个分析都是纯函数：

```go
func LiveOut(fn *ir.Function) map[ir.BlockID]map[ir.Loc]bool
func LiveIn(fn *ir.Function) map[ir.BlockID]map[ir.Loc]bool
func Reachable(fn *ir.Function) map[ir.BlockID]bool
func Uses(op ir.Op) []ir.Loc
func Defs(op ir.Op) []ir.Loc
func PredUses(p ir.Predicate) []ir.Loc
```

实现方式是标准的**迭代到不动点**，因为 CFG 可能有环（`while` 就是自环）。

**当前已实现**：活跃性（`LiveIn` / `LiveOut`）、可达性（`Reachable`）、
逐 op 的 uses/defs。**没有**定义-使用分析——`ReachingDefs` 不存在。

**将来需要时再加**：常量传播、别名、区间。加新分析只改 `analysis`，不动 `optimize`。

> **注意**：`LiveIn`（`analysis.go:194`）和 `LiveOut`（`:117`）是**两份独立的
> 活分析实现**，各自重算了一遍转移函数。两条实现会漂移——这正是
> `optimize.go` 里注释警告过的同一个错误模式（"computing it a second time here
> is how the two copies drifted apart before"），只是换了个地方。

---

## 5. Pass 框架

```go
type Pass interface {
    Name() string
    Run(p *ir.Program) *ir.Program
}
```

- Pass **原地修改** `p` 并返回它。6 个 pass（`SimplifyCFG` `PruneUnreachable`
  `SelfAssign` `DeadStore` `CoalesceTemps` `CollectConsts`）**全部**如此。
  「不修改输入、返回新 Program」是**目标**，不是现状——见 §10。
- Pass 之间不互相调用；需要的信息各自重新分析（`analysis` 包提供纯函数）
- 每个 Pass 必须有对应的测试，且测试断言 **IR 结构**而不是输出字符串
- 管线里没有 `Analysis` 参数：`Analysis` 是各 pass 内部的工具，不是 pass 之间的接口

管线顺序固定，但**每一档跑哪些 pass 是按档位过滤的**——`optimize.Catalog()`
列的是有序 pass 清单，每一项带一个 `MinLevel`；`Pipeline(level)` 是这份清单按
`MinLevel <= level` 过滤的结果。所以「一个 pass 想被启用，就必须先有它的
证明」——这是 `PassInfo.Requires` 字段存在的理由。

```
lower → optimize.Pipeline(level) → backend

O0: CollectConsts（常量池不是优化，是正确性——`#4` 没人初始化就读成 0）
O1: + SimplifyCFG, PruneUnreachable, SelfAssign
O2: + DeadStore, CoalesceTemps
O3: 与 O2 相同（O3 目前没有任何 pass）
```

「cleanup」pass（`SimplifyCFG` `PruneUnreachable` `SelfAssign` `DeadStore`）
跑**两遍**——一遍在值 pass 之前，一遍在之后。值 pass 会造出 cleanup 能消掉的形状。

---

## 6. 寄存器与调用约定

这是全项目最容易出错的地方，单独一节。

### 6.1 @s 是全局的

所有函数共享同一个 `@s`。这意味着：

- **调用者的临时槽会被被调用者覆盖。** 当前用「全程序唯一编号」规避，代价是槽位膨胀。
  正确的做法是活跃性分析 + 寄存器分配，但**保守版本必须先正确**。
- **参数是全局寄存器** `a0, a1, ...`，不是栈。递归需要显式模拟调用栈。

### 6.2 命名空间

| 前缀 | 用途 | 生命周期 |
|---|---|---|
| `a<N>` | 参数 / ABI 寄存器 | 调用约定 |
| `l<N>` | 源语言局部变量 | 函数 |
| `t<N>.ggm` | 编译器临时值 | 表达式 |
| `#<n>` | 常量 fake player | 全程序 |
| `Class.field` | 类字段 | 实体 |

**目标名上限 16 字符**，由 `target.FitObjective` 强制。

**临时量的名字是 `t<n>.ggm`，不是 `#t<n>`，也不是裸 `t<n>`。** 两个原因：

1. **带 `.ggm` 后缀**——作者写不出一个以 `.ggm` 结尾的标识符，所以临时量和
   作者的 objective **不可能撞车**。裸 `t0` 可以：作者的 `obj t1` 曾被当成
   临时槽改名合并，写入无声消失。
2. **不带 `#` 前缀**——objective 名也会出现在**选择器**里：
   `execute @s[score_t0.ggm_min=1]`。`#` 在 1.12.2 本身合法（常量池的假玩家
   就用它），但 GoMcRuntime 的选择器解析器拒绝它，于是每一条带守卫的命令
   都静默不执行——而带守卫的命令正是 `if` 的发射方式。

这三条约束写在 `ir.TempPrefix` / `ir.TempSuffix` 的注释里，不要在别处硬编码。

### 6.3 早退没有 goto

1.12.2 没有跳转。`return` 的正确降级是：

> 把「if 之后的剩余语句」移入一个**补集守卫的块**。

这要求 `lowerSeq` 返回「是否发散」，并让 `lowerIf` 接收 `rest`。这是 `lower` 里最不直观的部分，
改它之前先读 `stmt.go` 顶部注释。

---

## 7. 版本抽象

所有版本相关事实只在 `target`：

```go
type Target struct {
    Name                  string
    MaxCommandChainLength int
    ObjectiveNameLimit    int
    HasFunctionTag        bool
    ScoreMin              int   // 计分板值域下界，含
    ScoreMax              int   // 计分板值域上界，含
}
func (t Target) Condition(obj string, c ir.Cond) string
func (t Target) FitsScore(v int) bool
func (t Target) SelectorArgOK(arg string) bool
func (t Target) FitObjective(name string) string
```

`Condition` 是整个编译器价值最高的函数：它把区间语义封在一处。
1.13+ 移植时写一个新的 `Target` 值 + 一个新的 backend，`lower` 不动。

**一个例外：`CondAlts` 不是方法。**

```go
func CondAlts(op string) ([]ir.Cond, error)   // 包级函数，不接收 Target
```

它决定 `==` 是否发双界、`!=` 是否拆成两个 alternative——这是 1.12.2 区间语义
最核心的一条，却挂在包级函数上，**换版本时改不了**。其余 4 个版本相关函数
都是 `(t Target)` 方法。调用点只有一处（`lower/expr.go`），改成方法是一行的事。

---

## 8. 加特性该改哪里（速查）

| 想加 | 改哪 |
|---|---|
| 新语法糖 | `syntax/parser.go` + `lower` |
| 新语句类型 | `syntax/ast.go` → `lower/stmt.go` → 可能新增 IR 终结符 |
| 新内置函数 | `lower/call.go` 的 `builtin()` |
| 新优化 | `analysis/` 加分析 + `optimize/` 加 pass + 测试 |
| 新命令 | `lower/call.go` 的 `commandBuiltins` 表加一行（动词 + 哪个参数是选择器 + 最少参数个数）。**不是** `mc/command.go` |
| 新版本 | `target/` 加 Target 值 + `backend` 加渲染器 |
| 新诊断 | 用 `diag.Bag`，在对应层报 |

---

## 9. 测试策略

三层，缺一不可：

1. **单元测试**：`mc`（选择器区间——但这套 API 在生产路径上零引用，见 §10）、
   `lower`（predicate 取反）、`analysis`（数据流）
2. **语义测试**：断言 **IR 结构**。字符串断言抓不到「守卫条件被丢弃」这类 bug——
   曾经真的漏过：内联后 `renderGuard` 丢弃条件，生成的命令语法完全合法、能加载、逻辑全错。
3. **黄金测试**：固定源码 → 固定 `.mcfunction` 输出，逐字节比对

**不变量断言**（应当在 CI 里跑）：

- 任何 `Predicate` 里的 `Cond` 必须有非空 `Obj`
- 任何 `Loc` 必须有非空 `Holder` 和 `Obj`
- 每个函数的块 ID 必须都在 `Blocks` 范围内
- 除入口块外，每个块必须至少有一个前驱（否则不可达）
- 所有 objective 名长度 ≤ `target.ObjectiveNameLimit`

---

## 10. 已知的架构债

诚实记录，避免下次重新发现：

| 债 | 影响 | 状态 |
|---|---|---|
| `lower` 直接拼命令文本 | **15 处** `ir.Raw{Text: ...}`（另 `comptime/` 1 处、`driver/` 4 处）。拼出来的命令**不过 `target` 的任何检查**——`target.SelectorArgOK` 只在渲染后扫选择器参数，扫不到命令结构 | **未修。** 长期方案：给 `ir` 加结构化 op（如 `ir.ScoreboardRaw{Verb, Target, Objective, Value, NBT}`），由 `backend` 渲染。NBT 编解码（`lower/nbt.go` 6 处）最规整，适合先做 |
| `mc` 的选择器区间 API 是死代码 | `ScoreRange` / `Exact` / `LessThan` / `AtMost` / `GreaterThan` / `AtLeast` / `Parts` / `Selector.Score` 在生产代码里**零引用**，只被 `mc` 自己的测试调用。真正的区间语义在 `target.Condition` + `ir.Cond` | **未修。** 留着会让人以为「改 `mc/selector.go` 就能改选择器语义」——改了不会有任何效果。应删除 |
| Pass 原地修改输入 | 6 个 pass 全部 `Run(p *ir.Program) *ir.Program` 并**原地改** `p`。文档原本写的是「不修改输入、返回新 Program（值语义）」 | **未修。** 值语义要每个 pass 复制整棵 CFG，代价不小；更实际的做法是把本文改成描述现状 |
| `backend.Validate` 写死 `V1_12_2` | **已修。** `ValidateFor(t target.Target, line string)` 已存在，`driver.go:259` 传的是 `opt.Target`。`Validate(line)` 只剩一个写死 1.12.2 的便捷包装 | **已修** |
| 临时槽全程序唯一 | 槽位膨胀，产物比手写多 10% | **已修。** `optimize.CoalesceTemps`（O2）做活跃区间合并 |
| 常量池在 lower 时累积 | 未引用的常量也被生成 | **已修。** `optimize.CollectConsts`（O0 起）从**最终 IR** 重建常量表 |
| 无预算分析 | `[budget]` 是信任而非验证 | 未修。IR 上的成本模型 |
| 深层嵌套文件数增长 | 每个分支一个文件 | 未修。块合并 + 尾调用内联 |

---

*本文与代码同步演进。任何改变分层或 IR 形态的改动，必须先改本文。*
