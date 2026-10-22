# Baseline：ggm 0.29.3

**这个 baseline 冻结的是生成结果，不是功能列表。**

编译器唯一的产物就是它的输出。所以「输出变小了」和「输出的含义悄悄变了」
在一个 diff 里长得一模一样——除非输出本身被签入并被逐字比对。

---

## 冻结了什么

| 产物 | 位置 | 怎么验证 |
|---|---|---|
| 生成结果 | `internal/driver/testdata/golden/*.txt` | `go test ./internal/driver -run GoldenBaseline` |
| 语义行为 | `internal/*/**_test.go`（165 个测试函数，其中语义测试跑在 GoMcRuntime 上） | `go test ./...` |
| 版本标识 | `internal/version/version.go` | `ggm -version` |
| 全局检查 | 见下节 | `go test ./internal/driver -run 'Callee\|Uncalled\|Builtin'` |
| 类语义 | `internal/driver/class_test.go`（6 条，含真机执行） | `go test ./internal/driver -run 'Class\|Tick\|Spawn'` |

`testdata/golden/` 里是 `examples/*.ggm` 在 `-O0`..`-O3` 下**完整**的输出，
按函数名排序、以 `=== <函数 id> ===` 分节。文件头一行是
`# files=N commands=M`。

## 怎么用

改动代码生成后：

```sh
go test ./...                                    # 语义测试
go test ./internal/driver -run GoldenBaseline    # 逐字比对
```

如果 golden 失败，**先读 diff 再决定**：

```sh
go test ./internal/driver -run GoldenBaseline -update
git diff internal/driver/testdata/golden
```

失败信息会指出第一处不同的行号与两侧内容。**不要不看就 `-update`**——
那样签入的只是「现在是这样」，而不是「我知道变成了这样」。

---

## 0.2.0 引入的全局检查

这三条不改变任何生成结果（golden 逐字未变），它们只是把**编译器看得见、单文件工具看不见**
的事实说出来。

| 代码 | 性质 | 为什么需要全局视角 |
|---|---|---|
| `E_UNKNOWN_CALLEE` | error | 1.12.2 对「调用不存在的函数」既不报错也不执行，拼错的名字 = 静默死掉的代码路径 |
| `E_BUILTIN_UNIMPLEMENTED` | error | `abs`（作值）、`clamp`、`count` 是语言的名字但未实现；说「未知函数」会误导 |
| `W_UNREACHABLE_FN` | **warning** | 无调用者的函数永不运行。警告而非错误：命令方块与 `gameLoopFunction` 能调用它 |

---

## 0.3.0：class 可用

规范 §3.2 的 `Monster` / `CastTimer` 例子现在能编译。扇出形如：

```mcfunction
# <ns>/tick
execute @e[type=zombie,tag=mob.monster] ~ ~ ~ function t:k/Monster/tick

# <ns>/spawn  —— @s 先打标记，函数体里的 return 就跳不过它
execute @e[type=zombie,tag=mob.monster,tag=!ggm.spawn.Monster] ~ ~ ~ function t:k/Monster/spawn
```

---

## 这个 baseline 的数字

`examples/luo-math-full.ggm`（原手写库：17 文件 / 247 条命令）：

| 级别 | 文件 | 命令 |
|---|---|---|
| `-O0` | 16 | 133 |
| `-O1` | 16 | 133 |
| `-O2` | 16 | 129 |
| `-O3` | 16 | 129 |

**这些数字的正本是 golden 文件的头一行**
（`internal/driver/testdata/golden/luo-math-full.o*.txt` 写着 `# files=N commands=M`）。
要更新这里的数字，改 golden、跑 `go test ./internal/driver -run GoldenBaseline
-update`、再把头行抄过来——不要手抄，手抄就会漂。

**静态命令数不是重点。** `luo:math/init` 里有 35 条（28 条 `scoreboard objectives add`
加 7 条常量），但它们只在加载时跑一次。热代码看动态：

`sqrt(40000) = 200`，在 GoMcRuntime 上执行 **168** 条命令（`-O2`）。
四个优化级别的结果都是 200（`-O0`/`-O1` 是 172 条动态命令）。

> 旧版本这里写的是 `132 / 128` 和 `896` 条动态命令——都已过时。
> 现在这些数字是 2026-10-21 在 `ggm 0.27.0` 上实测的。

语义正确性由一份**独立的 32 位参考实现**守住：`rand(1,100)` 连续 8 次输出
`52, 94, 100, 42, 12, 54, 60, 2`，四个优化级别都必须一致。

---

## 这个 baseline **不**保证什么

写清楚边界比写清楚内容更重要，否则 baseline 会变成一张写着「一切都好」的纸。

- **`W_UNREACHABLE_FN` 会误报**：从命令方块或 `gameLoopFunction` 调用的函数会被标成无人调用。
  这是设计上的取舍，不是缺陷——消息里写明了这个出口。
- **`[death]` 不存在。** 1.12.2 无法观察已经消失的实体，正确实现需要影子实体。
  与其留一个永远报错的属性，不如不写——它现在就是一个未知属性，报 `E_PARSE`
  （`unknown attribute [death] on fn`）。
- **`[spawn]` 由 `on_cycle` 驱动**，所以世界必须每 tick 跑一次 `<ns>/cycle`，
  否则实体永远不会被处理。
- **入口只有两个，都要用户接线**：`<ns>/boot`（世界加载，命令方块）与 `<ns>/cycle`
  （`gamerule gameLoopFunction`）。1.12.2 没有 load 事件。
- **`[load]` 里没有 `@s`**，写实体分数报 `E_NO_SELF_CONTEXT`。用假玩家
  （`extern X: int = seed.x`）。
- **`[load]` 函数不能带参数**（`E_LOAD_PARAMS`）：世界加载时没人能给它。
- **同时触发取交集**：`[load, every("5s")]` 的函数没有 `@s`，因为两种上下文里
  有一种没有。
- **`on` 家族已删除**（`on load` / `on tick` / `on every` / `on spawn` / `on death`），
  全部改成声明上的属性：`[load]`、`[every("...")]`、`[spawn]`。
- **字段 objective 名截断到 16 字符**，类名前 6 字符相同的两个类会撞车（`E_FIELD_COLLISION`）。
- **类选择器只能是 1.12.2 选择器语法**，编译器按字面拼接，不校验。
- **规范第 7 节「计划中」那张表里的检查都不存在**（`W_WRAP_UNPROVEN`、
  `W_DIV_ZERO`、`E_TYPE_MISMATCH`、`E_NO_SHORT_CIRCUIT` 等）。
  规范 §7 现在列出了这个构建**全部 38 个**诊断码，已实现的和计划中的分得很清楚。
- **字面量之外的溢出仍然静默回绕。** `E_INT_RANGE` 只管字面量和 `score` 值。
- **`-O3` 与 `-O2` 输出相同。** 目前没有属于 O3 的 pass。
- **只有单一目标版本。** `-target` 只接受 `1.12.2`。

---

## 怎么定下一个 baseline

1. 改代码，跑 `go test ./...`（语义）与 `-run GoldenBaseline`（逐字）
2. 逐条读 golden 的 diff，确认每一处变化都是**想要的**
3. 更新 `internal/version/version.go` 的 `Version`
4. 在 `CHANGELOG.md` 顶部加条目，版本号必须与常量一致（有测试守着）
5. 更新本文的「数字」与「不保证什么」两节，以及 `docs/roadmap.md`
6. `git tag v<版本>`

**版本号的语义**（0.x 阶段）：

| 变更 | 版本位 |
|---|---|
| 生成结果的**含义**变了（语言语义、诊断行为） | minor |
| 生成结果优化了但语义不变 | patch |
| 新增用户可见的语法 | minor |
| 破坏性变更 | 0.x 阶段允许出现在 minor |

改动只影响 `internal/` 而不影响任何输出时，不必发版本——但 golden 会告诉你
到底影响没影响。
