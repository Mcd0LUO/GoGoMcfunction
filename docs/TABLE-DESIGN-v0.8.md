# 数据表（方案 C）实现设计 v0.8

> 状态：**设计稿，未实现。** 基于 2026-10-20 对参考数据包 `末法` 的实测。
> 本文取代 `DESIGN-v0.7.md` 第一节的形状归纳；§1.5/1.6 的「不做的部分」与验收标准仍然有效，
> 但验收标准按本文第 8 节修正。

---

## 0. 结论

**该做，但设计稿的形状模型必须先改。**

`DESIGN-v0.7.md` §1.1 说 6 张表「全部是同一个模板的 6 次实例化」，`#[decode(slot, field, width)]`
一条属性就能覆盖。**实测不成立**：19 个文件里有 **4 种形状**，一条 `#[decode]` 只能覆盖其中 10 个。

---

## 0.1 设计立场：手写文件只提供需求，不提供实现

> 用户 2026-10-20 指正：**「看真实手写文件只取思想，有可能不是最优实现。」**

这条直接作废了设计稿 §1.6 第 2 条验收标准（「与现有手写文件逐字节相同」）。
那等于把一份未经优化的手写产物**冻结成编译器的规格**，再管它叫验收。

**从手写文件取的**：

- 需求：确实有 19 张表，且全是纯数据（无控制流、无运行期决策）
- 形状：4 种参数化模式
- 参数：槽位 / 字段 / 位宽 / objective
- 语义：把 NBT 里的一段位域解成 objective 上的整数

**不取、且有意允许不同的**：

| 项 | 手写 | 编译器可自行决定 |
|---|---|---|
| 文件划分 | 19 个函数文件，`main` 逐个调 | 合并 / 拆分 / 内联，属于优化器的决定 |
| 命令内联 | `defence` 把 4 个槽位塞进 1 个文件（44 条） | 按槽位拆开也行——只要语义等价 |
| objective 命名 | `wp.damage1`、`wp.passiveId1:1`（不规则） | 由 `into` 显式给出，编译器不猜 |
| 注释与 `tellraw` | 手写文本 | **完全不生成**，那是人的文本不是数据 |

**编码自由度有多少（诚实评估）**：给定「位域 → objective」这个语义，
`w` 条 `add` + 1 条 `set 0` 在 1.12.2 里**接近下界**——每个 bit 的 NBT 路径下标 `k` 不同，
一条命令只能测一个下标，且 `add` 的右操作数必须是记分板名字/常量。
所以真正的自由度不在「少发几条 bit 命令」，而在**文件划分、内联与否、命名**。
**编译器不许比手写差**（见 §8 第 4 条），但也不该被手写的排版绑住。

---

## 1. 实测

复现命令（在仓库根执行）：

```powershell
$p = 'D:\game\PCL2\.minecraft\versions\1.12.2\saves\末法\data\functions\rpg\api\ApLoader'
Get-ChildItem $p -Recurse -Filter *.mcfunction | Where-Object { $_.FullName -notmatch '\\legacy\\' }
```

`ApLoader` 下非 `legacy` 共 **19 个** `.mcfunction`（设计稿说 15 个）：

| 形状 | 文件 | 条数 | 命令形态 |
|---|---|---|---|
| **A 位梯子** | `weapon/damage/{1,2,3}`、`weapon/pen_base/{1,2,3}`、`weapon/cooldown/{1,2,3}`、`equip/defence` | 10 | `set 0` + `add 2^k {…{field:{k:1b}}}` |
| **B 嵌套位梯子** | `weapon/passive/{1,2,3}_{1,2}` | 6 | `add 2^k {…{passive:{idx:{k:1b}}}}` |
| **C 枚举** | `weapon/type/{1,2,3}` | 3 | `set v {…{spec:v b}}`（v = 1..4） |
| **D 调用清单** | `main` | 1 | 18 条 `function` + 手写注释 + `tellraw` |

### 1.1 实测参数表（逐文件）

| 文件 | 槽位 | 字段 | objective | 位宽 |
|---|---|---|---|---|
| `weapon/damage/N` | N-1 | `atk_base` | `wp.damageN` | 11 |
| `weapon/pen_base/N` | N-1 | `pen_base` | `wp.pen_baseN` | 11 |
| `weapon/cooldown/N` | N-1 | `cooldown` | `wp.cdN` | **9** |
| `weapon/passive/N_M` | N-1 | `passive` | `wp.passiveIdN:M` | 10（内层 idx = M-1） |
| `equip/defence` | **100..103** | `defence` | `Eq.Defence` | 11 × 4 段，**同一个文件** |
| `weapon/type/N` | N-1 | `spec` | `wp.typeN` | 枚举值 1..4 |

### 1.2 与设计稿的三处出入

1. **宽度不是统一的 9**。设计稿的示例给 `damage` 标了 9 位并只列到 `k=8`；
   实测是 11 位（`k=0..10`，最大 `1024`）。cooldown 才是 9。
2. **`weapon/type` 不是位梯子**，是枚举解码：`spec:1b..4b` → `1..4`，
   用 `set` 而不是 `add`，值也不是 2 的幂。
3. **`equip/defence` 是「一个文件、4 个槽位、同一个 objective」**，
   所以槽位参数必须是**列表/区间**，不能是单值。

> 我自己的一个中间错误也记在这里：按「每文件 add 行数」统计时 `defence` 得到 `width=44`，
> 看上去会撞 int32 上限（`2^31` 越界）。按槽位分组后才发现是 4×11，**没有越界**。
> 结论：位宽必须**按槽位**统计，不能按文件。

---

## 2. objective 的归属：table 只写，不声明

实测 `scoreboard.mcfunction` 里有手写声明：

```mcfunction
scoreboard objectives add wp.damage1 dummy 武器伤害1
scoreboard objectives add wp.passiveId1:1 dummy 技能被动id1:1
```

所以 **table 不声明 objective，只写入**——与现有 `extern var … @ "objective"` 的分工一致。
这条决定了一个后果：`into` 写错时，1.12.2 会**静默什么都不做**（objective 不存在则写入无效），
所以必须有第 7 节的 `W_TABLE_OBJECTIVE_UNKNOWN`。
编译器不读数据包，**无法**验证 objective 真的存在，因此这条只能是警告。

---

## 3. 语法提案（按 §0.1 立场修正）

### 3.1 先否掉我自己上一版

我上一版把**手写的排版搬进了语言**：

```ggm
// ✗ 已否：声明名 = 手写文件路径
#[decode(slots = 0, field = "atk_base", width = 11, into = "wp.damage1")]
table ApLoader/weapon/damage/1          // 一个「表」= 一个手写文件
```

这错在：**「一个槽位-字段 = 一个文件」是排版的偶然，不是语义。**
语义只是「这些字段要解到这些 objective 上」。按这版写，作者仍要写 19 条声明，
再在别处手写 18 行 `function` 调用——**手写文件的形状原封不动地留在了语言里**，
只是把 `.mcfunction` 的后缀换成了 `table`。那不是抽象，那是转录。

### 3.2 修正版：声明描述解码，编译器负责排版与接线

```ggm
namespace rpg:api

table load_weapon_stats {
    // 槽位 0
    decode slot=0, field="atk_base", width=11, into="wp.damage1"
    decode slot=0, field="pen_base", width=11, into="wp.pen_base1"
    decode slot=0, field="passive",  width=10, index=0, into="wp.passiveId1:1"
    decode slot=0, field="passive",  width=10, index=1, into="wp.passiveId1:2"
    decode slot=0, field="cooldown", width=9,  into="wp.cd1"
    enum   slot=0, field="spec", values=1..4,  into="wp.type1"
    // 槽位 1、2 同理……
}

table load_armour {
    decode slots=100..103, field="defence", width=11, into="Eq.Defence"
}
```

一张 `table` = **一个生成的函数**；body 是**扁平的解码条目列表**。

这样换来什么：

| 手写 | 修正版 |
|---|---|
| 19 个函数文件 | **2 条声明** |
| `main` 里 18 行调用 + 手写注释 + `tellraw` | 作者调 `function rpg:api/load_weapon_stats` **一次** |
| 文件划分、调用顺序靠人维护 | 编译器决定，作者不参与 |

设计要点：

- **body 里没有控制流**：只有 `decode` / `enum` 两种纯参数条目。
  这与 `#[wrapping]` 的教训一致——**未知属性/条目必须拒绝**，不许静默忽略。
  （「没有 body」是我上一版的教条；body 装**声明**不等于把语句请回来，我把两者混为一谈了。）
- `slot` 与 `slots` 是同一条目的两种写法（单值 / 区间），不是两个关键字。
- `index` 只在 `passive` 这种嵌套形状上出现，是可选参数。
- 槽位后缀 `b`、objective 的 16 字符截断等版本事实**全部走 `internal/target`**（铁律第 1 条）。
- 不提供 `holder` 参数：实测全部是 `@s`。出现第二个 holder 再加（roadmap 规则）。
- 表格名仍要显式：它是作者**唯一**需要知道的名字（用于调用），不能让编译器随便取。

> **我上一条风险写错了，纠正**：我原写「18 行调用与 `legacy/main`、`tellraw` **交错**」。
> 回查 `main.mcfunction` 原文，18 行调用是**连续的一块**（3 组 × 6 行），
> 之后才是 `legacy/main` → `equip/defence` → `tellraw`。**没有交错。**
> 所以合成一次调用**不丢插入点**，那个风险是我凭空放大的。
>
> 仍然成立、且需要你确认的**只有一个**：手写的 3 组调用是**按槽位分组**的，
> 如果作者的真实意图是「只看槽位 0 时只跑那 6 条」（条件加载 / 增量刷新），
> 那么「一张表一个函数」就太粗，得允许**一张表对应多个函数**（按槽位分组）。
> 从文件里看不出这一点——`main` 是无条件全量加载的。**这条只能你来答。**

---

## 4. 展开规则（逐命令精确）

规则对**每个 `decode`/`enum` 条目**生效；**文件划分与条目顺序由编译器决定**（§3.2）。
逐命令文本必须精确——它要过后端 `Validate`，也要能让 §8 第 4 条「不劣于手写」可判定。

**A `decode`（无 `index`）** — `slots = s0..sN`、`field = f`、`width = w`、`into = o`：

```mcfunction
scoreboard players set @s <o> 0
// 对每个 slot s（按 s 升序）、每个 k = 0..w-1：
scoreboard players add @s <o> <2^k> {Inventory:[{Slot:<s>b,tag:{dat:{<f>:{<k>:1b}}}}]}
```

**B `decode`（带 `index = i`）** — 末段变成 `{dat:{<f>:{<i>:{<k>:1b}}}}`。

**C `enum`** — `values = v0..vN`：

```mcfunction
scoreboard players set @s <o> 0
// 对每个 slot s、每个 v ∈ values：
scoreboard players set @s <o> <v> {Inventory:[{Slot:<s>b,tag:{dat:{<f>:<v>b}}}]}
```

实测的字节形态（`equip/defence` 首尾）：

```mcfunction
scoreboard players set @s Eq.Defence 0
scoreboard players add @s Eq.Defence 1 {Inventory:[{Slot:100b,tag:{dat:{defence:{0:1b}}}}]}
…
scoreboard players add @s Eq.Defence 1024 {Inventory:[{Slot:103b,tag:{dat:{defence:{10:1b}}}}]}
```

**只有一个空格分隔 token，NBT 段内无空格**；`set 0` 只发一次，位置在所有 `add` 之前。
渲染走 `ir.Raw{Text: …}`，因此仍然经过后端 `Validate`（`E_BAD_COMMAND` 那道闸）。

---

## 5. 落点

| 层 | 改动 |
|---|---|
| `syntax/token.go` | 加关键字 `table` |
| `syntax/ast.go` | 新 `TableDecl`；`File.Tables []TableDecl` |
| `syntax/parser.go` | 解析 `table` 声明 + body 里的 `decode` / `enum` 条目（见 5.1） |
| `lower/table.go`（新） | `lowerTables`：算出命令文本，写进一个新函数 |
| `driver/driver.go` | 在 `comptime` 池之后调用（见下） |

### 5.1 属性参数解析器不存在（§3.1 旧提案的隐藏成本，修正版已绕开）

**先给结论**：§3.2 的修正版把参数放进 `table` 的 **body**，所以
**不需要**给属性加参数支持——`parseTable` 自己解析 body 条目即可，
与 `parseClass` 手写解析 `[selector(...)]` 同构（parser.go:380-411）。
属性参数**不再是前置条件**。这是修正版的额外收益。

但这条发现对**任何再想用属性形式的人**都是硬事实，记在这里：

`parseAttr`（parser.go:228-258）读完属性名之后，对 `(...)` 里的 token 是**逐个跳过并丢弃**的：

```go
depth := 0
for p.cur().Kind != RBRACK || depth > 0 { … p.next() }   // 只为跳过
```

它**只返回 `(string, Pos)`**，没有参数。所谓「`#[decode(slot = 0, field = "atk_base")]`」的
参数**目前根本解析不了**。

那 `#[selector(@e[...])]` 和 `#[budget(4)]` 是怎么工作的？**它们都不走 `parseAttr`**：

| 属性 | 实际路径 |
|---|---|
| `#[selector(...)]` | `parseClass` 在 `CLASS` 关键字之前**手写专用解析**（parser.go:380-411），括号配平后把原始文本拼成 `Selector` |
| `#[budget(N)]` | `parseWhile`/`parseLoop` 在条件之后**手写专用解析**（parser.go:732-754），期望恰好一个 `INT` |
| `#[wrapping]` | 无参数，`parseAttr` 够用（parser.go:323） |

所以 **table 的两种属性都要新写专用解析**，与 `selector` 同构：

```go
// 与 parseClass 的 selector 同一形状：parseTable 在 TABLE 关键字之前
// 自己读 #[decode(...)] / #[enum(...)]，括号配平后解析键值对。
```

代价：这是**第三个**专用属性解析器。更彻底的做法是给 `parseAttr` 加真正的参数支持、
把 selector/budget/decode/enum 统一过去——但那是独立重构，**不该和数据表捆在一起**。
建议 v1 按专用解析器写（与现状一致），把统一重构记进 roadmap。

实现模板**已经存在**：`driver.go:87-104` 的常量池就是同一个形状——`prog.NewFunc(ns + "/…")`、
`claimGenerated` 占名、`fb.Emit(0, …)` 写 IR。table 照抄这条路。

**不需要新增 IR 原语**（`ir.Raw` 已有），所以不触发「新增 IR 原语必须同时更新所有调用图分析」
那条铁律（`DESIGN` 里 `FanOut` 漏掉调用图的教训）。

---

## 6. 诊断码

| 码 | 检查 | 为什么 |
|---|---|---|
| `E_TABLE_ENTRY` | body 里出现 `decode`/`enum` 之外的条目，或参数缺失/类型错 | 未知条目必须拒绝（`#[wrapping]` 静默无效的教训） |
| `E_TABLE_WIDTH` | `width < 1` 或 `width > 31` | `width = 32` 时 `2^31` 超出 int32（`E_INT_RANGE` 的同类） |
| `E_TABLE_OBJECTIVE` | `into` 长于 16 字符 | 1.12.2 objective 名上限 |
| `E_FN_COLLISION` | table 名与 `fn` 撞在同一个函数 id 上（复用） | 1.12.2 归一化后同名 = 同一个函数 |
| `W_TABLE_OBJECTIVE_UNKNOWN` | `into` 与本模块任何 `extern` 的 objective 都不匹配 | 写错 objective 会静默无效（第 2 节） |
| `W_UNREACHABLE_FN` | 复用：table 生成的函数无人调用 | 已有机制 |

---

## 7. 落地步骤

1. 改 `docs/GGM-语法规范-v1.0.md`：加第 8 种声明 + 检查清单两张表（**先于改代码**，硬规矩）
2. `syntax`：token / AST / parser（含 §5.1 的专用属性参数解析器）
3. `lower/table.go` + `driver` 接线
4. 诊断码 + 各码一条反例测试
5. 跑 golden，**读 diff**，确认生成结果未被影响（预期：新增声明不影响既有示例 → golden 不变）
6. 升次版本号（0.6.0 → 0.7.0，生成结果的含义变了）

---

## 8. 验收标准（修正 §1.6）

**设计稿 §1.6 第 2 条（逐字节相同）作废**，理由见 §0.1。
验收改为**行为等价**，与项目既有方法论一致（`docs/VERIFICATION.md`：语义测试跑在真运行时上）。

| # | 标准 | 判定方式 |
|---|---|---|
| 1 | 19 个解码点能由声明生成 | 2 条 `table`、19 条 `decode`/`enum` 条目，`-check` 零诊断 |
| 2 | 解码**行为正确** | 喂给 `gomc.exe`：造一件带该 NBT 的装备，跑生成函数，比对 objective 值与 Go 里独立算的参考值 |
| 3 | 位宽边界正确 | 全 0 / 全 1 / 每个 k 单独置位，共 w+2 个用例 |
| 4 | **不劣于手写** | 命令数 ≤ 手写的 `w+1` 条/（槽位·字段）；若多于它，必须能解释多在哪 |
| 5 | 命名 / 文件划分 / 注释**允许不同** | 明确不比对 |

第 4 条是真正的验收：**生成器产出的命令比手写还多，这个特性就没有价值**。

关于 `main` 的 18 行调用清单：它按槽位分组、夹着手写注释（`# 载入0栏位武器`）、末尾一句 `tellraw`。
生成器只该产那 18 行，注释与 tellraw 留给作者。建议 v1 **不做**调用清单生成——
它省下的是一份「名字的列表」，而 `main` 每加一个槽位本来就要动手写注释和 tellraw，省不掉。
等出现第三个同构调用清单再说（roadmap 规则）。

---

## 9. 未验证

- `slots = 100..103` 这种区间写法：既然属性参数要新写解析器（§5.1），区间**由我们自己实现**，
  不存在「能不能接受」的问题；但 `..` 的**词法**是否已有独立 token、还是只在 `for` 里特判，
  **未验证**（需查 `syntax/token.go` 与 `parseFor`）。
- 19 个文件是否覆盖了 `ApLoader` 之外的同形状表**未统计**（本次只数了 `ApLoader`）。
- 把 19 个手写文件作为 golden 固定件存进 `internal/driver/testdata/` 需要作者同意
  （测试**不许**在运行时读 `D:\game\…`，那样测试就绑死在作者机器上）。
