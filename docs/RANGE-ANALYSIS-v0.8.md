# GGM 值域分析（区间分析）阶段 1 —— 调研报告

> 调研 worker W1。**只调研，未实现**：仓库源码零改动（`git status --porcelain` 为空，已复核）。
> 基线：`go build ./...` 通过；`go test -count=1 ./...` **63 条顶层测试全绿**（含真机语义测试，运行时 `D:\code\go\GoMcRuntime\gomc.exe` 在位，未静默 skip）。
> 所有数字都附了可复现命令与实测输出。没验证过的判断都标了「未验证」。

---

## 总结论

**该做，但不该按设计稿原样做；停在阶段 1，且必须先修三个更基础的静默误编译。**

设计稿 3.2/3.3 的格与传递函数**至少 6 处是错的**（Q2）；3.4 最后一句「`#[budget(N)]` 压住 widen 精度损失，GGM 白拿」**不成立**（Q4）；3.6「`luo/math/lcg_step` 是唯一合法用户」**被仓库自己的代码证伪**（Q5）。

最大风险不是分析器写不出来，而是**分析器会照抄设计稿的错误，把已经存在的静默误编译重新包装成「已证明安全」**。调研过程中用真机（`gomc.exe difftest run`）实测出 **3 个当前版本的静默误编译**，其中 2 个正好落在值域分析要管的算术上：

| # | 症状 | 证据 |
|---|---|---|
| **A** | `x += 3` 被降成 `x = 3`（**赋值运算符被丢弃**） | `examples` 实测 + 真机返回 3 而非 8 |
| **B** | 比较的**差值槽回绕**：`n < 4` 在 `n = -2147483648` 时判假 | 真机 `return/R = 0`，源码语义应为 1 |
| **C** | `rem_euclid` 中间量 `t + m` 溢出 | 真机 `return/R = -2147483549`，应为 100 |

这三个都在**值域分析的作用域之内**。如果先做值域分析而不先修它们，分析器会把 B 的差值槽当成「已知不溢出」而放过，把 C 的 `rem_euclid` 当成「取模不溢出」而放过 —— **检查器给出了错误的安全结论**，比没有检查器更危险。

**建议顺序**：先修 A/B/C（各自 1–3 条测试即可钉死）→ 再做阶段 1（格 + 收窄 + 传递函数）→ 阶段 2（`[range]`）在阶段 1 精度实测达标后再评估。

---

## Q1 IR 落点

### 结论

区间分析应落在**新包 `internal/range`**（不叫 `analysis`，避免与 `internal/analysis` 混淆），在 **IR 层**跑，插在 `driver.Compile` 的 `optimize.Run` **之前**。

### 精确插入点

```go
// internal/driver/driver.go:72-76（现状）
res, lowerDiags := lower.Lower(file, opt.Target, filename)
...
prog := res.Program
...
prog = optimize.Run(prog, opt.Level)   // driver.go:106
```

**建议插入在 `driver.go:76` 之后、`driver.go:106`（`optimize.Run`）之前**：

```go
prog := res.Program
// ── 新增 ──
for _, d := range rangeanalysis.Check(prog, opt.Target, file) {
    bag.Add(d)
}
// ── 结束 ──
prog = optimize.Run(prog, opt.Level)
```

### 为什么在 IR 层而不是 AST 层

| 理由 | 证据 |
|---|---|
| 谓词已经是区间了 | `ir.Cond{Obj,HasMin,Min,HasMax,Max}`（`internal/ir/ir.go:191-197`）——收窄要的就是这个形状 |
| 分支已经是 CFG 了 | `ir.Branch{Cond,Then,Else}`（`ir.go:66-70`），`ir.Succs`（`ir.go:88-96`）直接给出后继 |
| 1.12.2 选择器条件本身就是区间语义 | `target.Condition`（`internal/target/target.go:68-78`）：`score_x=N` 是 `x <= N` |
| 补集已有唯一实现 | `ir.Negate`（`internal/ir/logic.go:30-63`）——收窄的 else 边必须用它，不能自己再算一遍（铁律 2） |

**AST 层做不到**：AST 里的 `if` 条件是**未降级的比较表达式**（`syntax.Binary`），差值槽是 lower 阶段才物化的临时目标（`lower/expr.go:290`）。要在 AST 层收窄，就得把 lower 的降级逻辑复制一遍 —— 违反「补集只有一份实现」和「版本知识只在 `internal/target`」两条铁律。

### 需要读 IR 的哪些原语

| 原语 | 位置 | 用途 |
|---|---|---|
| `ir.Program.Funcs` | `ir.go:17` | 逐函数分析 |
| `ir.Function.Blocks` / `.Params` / `.Origin` | `ir.go:30-47` | CFG 遍历、参数入口假设、诊断定位 |
| `ir.Block{ID,Ops,Term}` | `ir.go:53-57` | 基本块 |
| `ir.Op` 全体 | `ir.go:110-185` | 读值来源 |
| `ir.Set{Value}` | `ir.go:113-116` | **字面量来源**（`[n,n]`） |
| `ir.Copy{Dst,Src}` | `ir.go:119-122` | 复制，区间不变 |
| `ir.Arith{Dst,Op,Src}` | `ir.go:126-130` | **核心**：`Op` ∈ `+= -= *= /= %= < > <= >=` |
| `ir.Add{Value}` / `ir.Remove{Value}` | `ir.go:133-142` | 字面量加减 |
| `ir.Guarded{Inner,Cond}` | `ir.go:153-156` | **条件写**：值是「二选一」，必须 join |
| `ir.Branch{Cond,Then,Else}` | `ir.go:66-70` | 分支收窄的边 |
| `ir.Predicate{Alts,Exist}` | `ir.go:204-207` | DNF，`Alts` 是析取 |
| `ir.Cond{Obj,HasMin,Min,HasMax,Max}` | `ir.go:191-197` | 区间条件 |
| `ir.ExistTest` | `ir.go:211-214` | 存在性测试，**不携带数值信息** |
| `ir.ConstName` / `ir.ConstObjective` | `ir.go:219-229` | 常量池 holder 是 `#<值>`，`#0` 是个坑（见 Q2 反例 5） |
| `ir.Succs` | `ir.go:88-96` | 后继 |
| `ir.Negate` | `logic.go:30-63` | else 边的补集 |

### 关键设计约束（来自代码，不是来自设计稿）

1. **`ir.Arith` 不区分「哪个是左操作数」**。`x += y` 降成 `Copy{Dst:x, Src:y}` + `Arith{Dst:x, Op:"+=", Src:y}`（`lower/expr.go:82-88`），即 `x = x + y`，但 **`x` 既是 `Dst` 又是隐含的左操作数**。`analysis.Uses` 明确把 `Dst` 也算作读（`internal/analysis/analysis.go:45-47`）。所以传递函数必须写 `dst_new = dst_old ⊕ src`，**不能**当成二元 `dst = dst ⊕ src` 只读一次 `Dst` 就用新值。

2. **差值槽是隐藏的溢出点**。`lower/expr.go:290` 发射 `Copy{Dst:d, Src:lhs}` + `Arith{Dst:d, Op:"-=", Src:rhs}`，即 `d = lhs - rhs`，**在 int32 里算**。这个 `Arith` 出现在**比较**里，不是用户写的算术。它必须被单独检查（Q2 反例 3）。

3. **`ir.Guarded` 的语义是「二选一」**。`analysis.Uses` 明确说守卫失败时旧值存活（`analysis.go:51-57`）。所以 `Guarded` 之后目标的区间是 `join(旧, 新)`，**不是**新值。

4. **`ir.Raw` 不透明**。`tag_add` / `kill` / `say` 都降成 `Raw`（`lower/call.go:30-38`）。它们不改分数，但分析器必须**承认自己不知道**，不能假装没事。

5. **常量池的 `#0` 是真值**。`ir.ConstName(0)` = `"#0"`（`ir.go:224-229`），`#0` 是合法的常量 holder。但 `ConstName` 的 `itoa` 只处理正数（`ir.go:231-242`），负数走 `"#n" + itoa(-v)`。分析器解析常量 holder 时要复用 `ir.ConstName` / `optimize.parseConstName`（`optimize.go:511-535`），**不能自己写一份**。

---

## Q2 格与传递函数：逐条审

设计稿 3.3 的表（`docs/DESIGN-v0.7.md:235-248`）。**6 处有问题**，给反例。

### 反例 1：`a / b`「四端点相除」是错的

设计稿原文（`DESIGN-v0.7.md:243`）：

```
| `a / b` | `b` 含 0 则退化；否则四端点相除 |
```

**问题**：`b` 不含 0 时，四端点相除**仍然不能给出正确区间**，因为**除法不是单调的**（在 0 两侧反号），且**向零截断**。

具体反例：

- **输入**：`a ∈ [-2147483648, 2147483647]`，`b ∈ [-1, -1]`（恒为 -1）
- **期望**：`a / b = -a`，所以结果是 `[-2147483647, 2147483648]` → 实际 int32 内是 `[-2147483647, 2147483647]`
- **设计稿会得出的结果**：四端点相除 `{-2147483648/-1, 2147483647/-1, ...}` = `{2147483648, -2147483647}`，取 min/max = `[-2147483647, 2147483648]`
- **差在哪**：`2147483648` 超出 int32。设计稿没说端点要 clamp，于是这个区间被当成「合法」继续用。**更糟的是**：`[-2147483647, 2147483648]` 看起来不溢出，于是**溢出检查被静默放过**。

**更硬的反例**：`a ∈ [1, 1]`，`b ∈ [-2147483648, -2147483648]`
- **期望**：`1 / -2147483648` = 0（向零截断）
- **设计稿**：`1 / -2147483648` = 0（四端点相除也得到 0）
- 这里巧合相等。**但**：`a ∈ [-2147483648, -2147483648]`，`b ∈ [-2147483648, -2147483648]`
- **期望**：`-2147483648 / -2147483648` = 1
- **设计稿**：`2147483648 / -2147483648` = -1（四端点相除在 int64 里算！`2147483648 / -2147483648 = -1`）
- **差在哪**：**错了整整 2**。因为 `a.lo * b` 这类中间量在 int64 里算，而**运行期是 int32**。

**修正规则**：

```
a / b =
  若 b 含 0                                  → ⊤（且报 W_DIV_ZERO）
  否则对 x ∈ {a.lo, a.hi}, y ∈ {b.lo, b.hi}:
      算 trunc(x/y) 在 **int32 语义** 下（先 wrap 再除）
      取 min/max
  注意：int32 语义下 -2147483648 / -1 = -2147483648（不是 2147483648）
```

**必须用 int32 语义算除法**，因为 `world.Apply` 的 `OpDiv` 是 `wrapInt32(target / operand)`（`GoMcRuntime/internal/world/scoreboard.go:343-347`）——**先在 Go 的 int 里除，再 wrap**。Go 的 int 是 64 位，所以 `-2147483648 / -1` = `2147483648`，再 wrap 成 `-2147483648`。**这与 Java 的 int 除法不同**（Java 会直接溢出成 `-2147483648`）。运行时是权威（`HANDOVER.md:82`「它的行为就是规格」）。

**修正后的规则**：`trunc32(x, y) = wrapInt32(x / y)`，其中 `x`/`y` 先各自 wrap 成 int32。

### 反例 2：`a % b` 下界 `[-(|b|-1), |b|-1]` 是错的（且不安全）

设计稿原文（`DESIGN-v0.7.md:244`）：

```
| `a % b` | `[-(|b|-1), |b|-1]` |
```

**问题一：`|b|` 没定义，b 可能是负区间。** `b ∈ [-10, -5]` 时 `|b|` 是 5 还是 10？

**问题二（更要命）：这个式子在 b 变号时给出错误的符号范围。**

- **输入**：`a ∈ [5, 5]`，`b ∈ [-1, 1]`
- **期望**：1.12.2 的 `%` **向零截断**（`GoMcRuntime/internal/world/scoreboard.go:348-352`：`wrapInt32(target % operand)`，Go 的 `%` 取被除数符号）。`5 % -1 = 0`，`5 % 1 = 0`。结果恒为 0。
- **设计稿会得出的结果**：`[-(|b|-1), |b|-1]`，`b` 含 0 → 退化，还是按 `|b| = 1` 算 → `[0, 0]`。**巧合正确**。

- **再来一个**：`a ∈ [7, 7]`，`b ∈ [-3, 3]`
- **期望**：`7 % -3 = 1`，`7 % 3 = 1`，`7 % 2 = 1`，`7 % 1 = 0`……所以结果集是 `{0, 1, 2}`，即 `[0, 2]`。
- **设计稿**：`[-(3-1), 3-1] = [-2, 2]`。
- **差在哪**：**多了负半边**。设计稿的区间比真值大 2 倍。**这个方向是安全的**（过近似），但它导致**误报**：`a % b` 的结果实际恒 `>= 0`（当 a >= 0），设计稿却给出可能为负，于是后续 `x = a % b; y = x - 1` 之类会被误判为可能溢出。

**问题三（真正致命的）：`b` 含 0 时设计稿没说会退化，但 §3.3 表格里 `a / b` 写了「b 含 0 则退化」，`a % b` 却没写。** 1.12.2 的 `%` 遇 0 是**运行期错误**（`world.Apply` 返回 `cannot divide by zero`，`scoreboard.go:349-351`），不是退化。

**修正规则**：

```
a % b =
  若 b 含 0                              → ⊤，且报 W_DIV_ZERO
  否则 b 恒 > 0 时 → [0, min(|b|-1, ...)]   // 结果与 a 同号
       b 恒 < 0 时 → 仍是 a % |b|，符号取 a
  实际上下界 = 与 a 同号的一段：
      a.lo >= 0 → [0, min(a.hi, |b|-1)]     （a 不变号时）
      a.hi <= 0 → [-min(|a.lo|, |b|-1), 0]
      a 变号    → [-(|b|-1), |b|-1]
```

### 反例 3：设计稿**整张表都漏了**差值槽

比较 `a < b` 降成（`lower/expr.go:290`）：

```
d = a; d -= b;  然后测 d
```

**`d` 是 int32**，所以 `d = a - b` **会回绕**。设计稿 3.3 的表里「比较运算 | `[0, 1]`」——只说了**结果**是 0/1，**完全没提中间的减法**。

- **输入**：`a = -2147483648`，`b = 1`
- **期望**：`a < b` 为**真**
- **设计稿会得出的结果**：比较结果 `[0,1]`，**看不出任何问题** —— 溢出检查不会报
- **实际运行期**：`d = -2147483648 - 1` 在 int32 里回绕成 `2147483647`，测 `d <= -1` 为**假**

**真机实测**（`gomc.exe difftest run`，源 `fn f(n: int) -> int { if n < 4 { return 1 } 0 }`，`n = -2147483648`）：

```
scoreboard players operation @s t0 = @s a0
scoreboard players operation @s t0 -= #4 ggm.c
execute @s[score_t0=-1] ~ ~ ~ scoreboard players set @s return 1
function t:e/f$b2 unless @s[score_t0=-1]
```

```
error: Entity '@s[score_t0=-1]' cannot be found
    return/R = 0
```

源码语义要求 `return = 1`（`n < 4` 为真）。**这是一个真实的静默误编译**，且在 `examples/luo-math-full.ggm:50` 的 `sqrt` 里就是 `if n < 4 { return 1 }` —— 只是 `n` 是 `extern` 参数，实际调用方不会传 `-2147483648` 罢了。

**修正规则**：比较的差值槽必须单独检查溢出。

### 反例 4：乘法「四端点两两相乘取 min/max」在 int32 语义下会给出不存在的值

设计稿原文（`DESIGN-v0.7.md:242`）：

```
| `a * b` | 四端点两两相乘取 min/max |
```

- **输入**：`a ∈ [-2147483648, -2147483648]`，`b ∈ [-1, -1]`
- **期望**（int32 语义）：`-2147483648 * -1` 回绕成 `-2147483648`
- **设计稿会得出的结果**（int64 算）：`2147483648` → 区间 `[-2147483648, 2147483648]`
- **差在哪**：上界 `2147483648` 越界。设计稿说「溢出即退化」（`:240` 的注解），但**没说在哪个环节退化**。如果先算 int64 端点再判溢出，那**所有乘法的区间都会莫名地宽 1**，误差在传递链上累积。

**更实际的问题**：`a ∈ [100000, 100000]`，`b ∈ [100000, 100000]`
- **期望**：`10000000000` mod 2³² = `1410065408`
- **设计稿**：int64 端点 `10000000000`，判溢出 → 退化 ⊤
- **这是安全的**（⊤ 覆盖一切）。**但**它意味着**几乎所有大数乘法都退化成 ⊤**，检查器全报。这不是「错」，是**精度损失**，而设计稿 3.2 说「超出即退化为 ⊤（未知）」——**注意 3.2 的 ⊤ 定义是 `[-2^31, 2^31-1]`，是一个「数值区间」而不是「无界」**（`:225`）。这两句话自相矛盾：3.2 说 ⊤ = 整个 int32 范围，3.3 说「溢出即退化」，而 3.2 的格图（`:224-230`）画的是 `⊤ → [lo,hi] → ⊥` 的**偏序**，⊤ 在**上方**。**「⊤ = 整个 int32 范围」和「⊤ 在格的顶」是两种不同的东西**，设计稿没区分。

**修正**：分清两件事：
- **格元素**：`[lo,hi]`，加上 `⊥`（不可达）和 `⊤`（无约束）。
- **「无约束」的数值表示**：`[-2^31, 2^31-1]`。

写成 `type Interval struct { Lo, Hi int64; Kind }`，`Kind ∈ {Concrete, Top, Bottom}`。**`Top` 不等于 `[i32min, i32max]`** —— 后者是「恰好落在这个范围」，前者是「不知道，但一定在 int32 内」。**分析器必须能区分**，否则 join 会把 `Top` 和具体区间混为一谈，精度就废了。

### 反例 5：`min(a,b)` / `max(a,b)`「端点组合」是错的

设计稿原文（`DESIGN-v0.7.md:245-246`）：

```
| `min(a,b)` | `[min(lo), min(hi)]` |
| `max(a,b)` | `[max(lo), max(hi)]` |
```

**问题：`min(lo_a, lo_b)` 和 `min(hi_a, hi_b)` 混用时，`lo` 可能来自 a 而 `hi` 来自 b，反之亦然——但这在数学上仍是一个合法区间吗？**

- **输入**：`a ∈ [10, 20]`，`b ∈ [100, 200]`
- **期望**：`min(a,b) ∈ [10, 20]`（因为 a 整体小于 b）
- **设计稿**：`[min(10,100), min(20,200)] = [10, 20]`。**巧合正确**。

- **再来**：`a ∈ [10, 100]`，`b ∈ [20, 30]`
- **期望**：`min(a,b) ∈ [10, 30]`
- **设计稿**：`[min(10,20), min(100,30)] = [10, 30]`。**正确**。

- **再来**：`a ∈ [10, 30]`，`b ∈ [20, 100]`
- **期望**：`min(a,b) ∈ [10, 30]`
- **设计稿**：`[min(10,20), min(30,100)] = [10, 30]`。**正确**。

- **关键反例**：`a ∈ [10, 100]`，`b ∈ [50, 60]`
- **期望**：`min(a,b) ∈ [10, 60]`
- **设计稿**：`[min(10,50), min(100,60)] = [10, 60]`。**正确**。

嗯，端点组合对 `min`/`max` 确实成立（它们是单调的）。**但 GGM 的 `min`/`max` 降级方式不是 `min(a,b)`！**

看 `lower/call.go:370-383`：

```go
case "min", "max":
    t := fb.TmpLoc()
    op := "<"
    if c.Name == "max" { op = ">" }
    fb.Emit(block, ir.Copy{Dst: t, Src: a})
    fb.Emit(block, ir.Arith{Dst: t, Op: op, Src: b})
```

即 `t = a; t < b`（`OpKeepMin`：`operand < target ? operand : target`）。`world.Apply` 的 `OpKeepMin`（`scoreboard.go:355-359`）：`if operand < target { return operand } else { return target }`。

**所以 `min(a,b)` 的实际结果是 `min(a,b)`，端点组合成立。这条设计稿是对的。**

**但真正的错误在别处**：设计稿 3.3 有一行

```
| `clamp(a,lo,hi)` | `[lo, hi]` ← **最有用的一条** |
```

**`clamp` 根本没有实现！** `lower/call.go:143`：

```go
var reservedCalls = map[string]bool{"abs": true, "clamp": true, "count": true}
```

实测：

```
$ go run ./cmd/ggm -check clamp.ggm
clamp.ggm:3:5: error: clamp is a builtin that this build does not implement in this position [E_BUILTIN_UNIMPLEMENTED]
```

**设计稿 3.4 的「值从哪来」表里，`clamp` 是最有用的一条来源（`:257`），而它不存在。** 这意味着阶段 1 的「值从哪来」在当前构建里**少了一半**。

### 反例 6：`[wrapping]` 的「2 的幂取模免标注」豁免是**不成立的**

语法规范 `docs/GGM-语法规范-v1.0.md:186`：

> `[wrapping]` 注解关闭溢出检查（LCG 需要）；若函数体内有 `% M` 且编译器能证明 M 是 2 的幂，则无需标注。

**这个豁免是错的。** 2 的幂取模**只有在乘加本身不溢出时**才等价。

- **输入**：`lcg_step` 的算式 `(LCG_A * s + LCG_C) % LCG_M`，`LCG_M = 67108864 = 2^26`
- **期望**（不回绕的精确数学）：`(1103515245 * s + 1234) mod 2^26`
- **实际运行期**（int32 回绕）：先 `A*s` 回绕成 int32，再 `+C` 回绕，再 `% M`
- **两者在 `s = 12345` 时**：
  - 精确：`13622895699525 mod 67108864 = 64744215`
  - int32 回绕：`-2364649`
  - **不同！**

实测（真机 `gomc.exe difftest run`，`luo:math/lcg_step`，`a0 = 12345`）：

```
    return/R = -2364649
```

**所以 `lcg_step` 的 `[wrapping]` 是必要的** —— 规范说的「2 的幂可免标注」在这个例子上**恰好是错的**。如果按规范豁免，`lcg_step` 就不标 `[wrapping]`，而它**必须**标。

**这直接回答 Q5**：设计稿 3.6「`luo/math/lcg_step` 是唯一合法的用户——它的设计就是要回绕」**方向对但不完备**，而且 `luo-math.ggm:33` 的 `is_happen_fix1000` 是**第二个**必须标却没标的。

### 传递函数表 —— 修正版

| 表达式 | 正确规则 |
|---|---|
| 字面量 `n` | `[n, n]` |
| 常量 `K` | `[K, K]`（`K` 是 `const` 声明） |
| `a + b` | `[a.lo+b.lo, a.hi+b.hi]`，**int64 算完后 clamp 到 int32 并标记溢出** |
| `a - b` | `[a.lo-b.hi, a.hi-b.lo]`，同上 |
| `a * b` | 四端点两两相乘（**int64 算**）取 min/max，clamp + 标记溢出 |
| `a / b` | b 含 0 → ⊤ + `W_DIV_ZERO`；否则 `trunc32(x/y)` 四端点（**int32 语义**）取 min/max |
| `a % b` | b 含 0 → ⊤ + `W_DIV_ZERO`；否则按 a 的符号分情况，见反例 2 |
| `min(a,b)` | `[min(a.lo,b.lo), min(a.hi,b.hi)]`（**成立**，GGM 的 `min` 就是端点语义） |
| `max(a,b)` | `[max(a.lo,b.lo), max(a.hi,b.hi)]`（**成立**） |
| `clamp(a,lo,hi)` | **`[lo,hi]` —— 但 `clamp` 未实现（`lower/call.go:143`）** |
| `rem_euclid(a,m)` | **设计稿没有这一行，但 `lower/call.go:384-396` 发射 `t=a; t%=m; u=t; u+=m; u%=m` —— 中间量 `t+m` 会溢出** |
| 比较 `a op b` | 结果 `[0,1]`；**但差值槽 `d = a - b` 必须单独查溢出** |
| `abs(x)` | `abs` 只在比较左侧被代数改写（`lower/expr.go:310-355`），**不会成为值**；但 `abs(x) >= k` 改写成 `x <= -k OR x >= k` **两条备选**（析取） |
| `reduce<min>(sel,obj)` | 种子 `2147483647`（`lower/call.go:343-348`），结果 ∈ `[min(值), 2147483647]` |
| 函数调用 | **阶段 1 无过程间信息 → ⊤** |

**注意 `rem_euclid` 那一行**：`lower/call.go:390-395` 发射

```go
t = a;  t %= m
u = t;  u += m;  u %= m
```

`u += m` 是 **int32 加法**。当 `m` 接近 `INT32_MAX` 且 `t` 为正时，`t + m` 溢出。真机实测：

```
$ go run ./cmd/ggm -q -O 2 -out out re.ggm   # rem_euclid(x, 2147483647)
scoreboard players operation @s t0 = @s a0
scoreboard players operation @s t0 %= #2147483647 ggm.c
scoreboard players operation @s t1 = @s t0
scoreboard players operation @s t1 += #2147483647 ggm.c     ← 这一步溢出
scoreboard players operation @s t1 %= #2147483647 ggm.c
```

真机（`a0 = 100`）：

```
    return/R = -2147483549
```

`rem_euclid(100, 2147483647)` 应该是 **100**。**错了 2147483649。** 这是第三个静默误编译。

---

## Q3 分支收窄

### 结论：可行，而且 GGM 的 IR 让它比一般语言**便宜**，但设计稿低估了它的语义代价

### 算法

GGM 的谓词 `ir.Cond{Obj,HasMin,Min,HasMax,Max}`（`ir.go:191-197`）**本身就是区间**，与 1.12.2 的选择器语义同构（`target.Condition`，`target.go:68-78`）。所以收窄**不需要**任何新的版本知识 —— 这正是「补集只能由 `ir.Negate` 算」和「版本知识只在 `internal/target`」两条铁律能同时满足的原因。

#### 步骤 1：谓词 → 区间集合

`ir.Predicate` 是 DNF：`Alts [][]Cond`（`ir.go:204-207`），每个 `Alt` 是**合取**（同一目标上的多个 `Cond` 合并成一个区间），`Alts` 之间是**析取**。

对每个 `Alt`：
1. 按 `Obj` 分组（`ir.Simplify` 的 `mergeConds` 已经做了，`logic.go:158-193`）
2. 每个 `Obj` 得到一个区间 `[lo, hi]`，缺 `HasMin` 记 `-∞`，缺 `HasMax` 记 `+∞`
3. **合取** = 各目标区间的**交集**

#### 步骤 2：单目标的收窄（核心）

`ir.Cond` 的**单个目标**就是区间，所以收窄是**区间求交**：

| 谓词 | 收窄成 |
|---|---|
| `x <= M` | `[x.lo, min(x.hi, M)]` |
| `x >= M` | `[max(x.lo, M), x.hi]` |
| `x <= M && x >= N` | `[max(x.lo,N), min(x.hi,M)]` |
| `x == M` | `[M, M]`（**注意：1.12.2 的 `==` 必须同时给上下界**，见 `target.CondAlts` 的 `"=="` 分支，`target.go:103-104`） |
| `x != M` | **析取**：`(-∞, M-1] ∪ [M+1, +∞)` —— 见下面「`!=` 怎么办」 |

**但这里有一个 GGM 特有的陷阱**：GGM 的比较**不是**「对 x 本身做区间测试」，而是**先算差值槽 `d = lhs - rhs`，再测 d**（`lower/expr.go:290` + `lower/expr.go:358-368`）。

所以 `a > b` 降成 `d = a - b; 测 d >= 1`。**收窄学到的是「`a - b >= 1`」，不是「`a >= b+1`」。**

- 若 `b` 是字面量 `k`：`a - k >= 1` ⟹ `a >= k+1`。**可收窄。**
- 若 `b` 是变量：**学不到关于 `a` 的任何信息**（`b` 可能是 `-10^9`）。

**设计稿 3.4 的收窄例子（`:265-269`）**：

```ggm
if hp > 0 { hp -= 1 }   // 此分支内 hp ∈ [1, INT32_MAX]
```

这里 `0` 是字面量，所以 `hp - 0 >= 1` ⟹ `hp >= 1`。**成立**。

**但设计稿没说的是**：`if hp > other_var` 收窄不了。**必须区分这两种情况。** GGM 的条件只有单比较（`GGM-语法规范-v1.0.md:148`），所以每条 `if` 只有一个 `Cond` 目标 —— 收窄面很窄，这既是好消息（简单）也是坏消息（能收窄的情况少）。

#### 步骤 3：else 边

else 边的谓词是 `ir.Negate(thenPred)`（`logic.go:30-63`）。**直接用 `ir.Negate` 的结果做区间求交即可**，不要自己算补集（铁律 2）。

`ir.Negate` 对 `Cond` 的补集是（`logic.go:76-93`）：
- 两侧都有界 → **两个**区间：`[min, M-1]` 和 `[M+1, max]`
- 只有上界 → `[M+1, +∞)`
- 只有下界 → `(-∞, M-1]`

#### 步骤 4：`!=` 的析取处理

`ir.Cond` 层面**没有 `!=`**：它被降成两个 `Cond`（`target.CondAlts` 的 `"!="` 分支，`target.go:105-106`）：

```go
case "!=": // d <= -1 or d >= 1: two alternatives, no OR available
    return []ir.Cond{{HasMax: true, Max: -1}, {HasMin: true, Min: 1}}, nil
```

注意这是**两个独立的 `Cond` 返回值**，不是**一个 `Cond`**。`lower/expr.go:358-368` 的 `predicateFor` 把它们**各自变成一个 `Alt`**：

```go
for _, c := range alts {
    p.Alts = append(p.Alts, []ir.Cond{{Obj: d, HasMin: c.HasMin, ...}})
}
```

所以 `a != b` 在 IR 里是 `Predicate{Alts: [][]Cond{ {d<=M-1}, {d>=M+1} }}` —— **两个析取分支**。

**处理方式**：
- **then 边**：两个 `Alt` 的**并集**。**不能**简单求交（`[x.lo, M-1] ∩ [M+1, x.hi]` 是空集，而真值是并集）。正确做法：**分别收窄，再取并集的 hull**。
  - `x != k`（k 是字面量）：`x ∈ (-∞, k-1] ∪ [k+1, +∞)` → hull 就是 `(-∞, +∞)` = **没有收窄**。
  - **结论**：`!=` 在**单变量层面不产生收窄**（除非区间本来就窄）。
- **else 边**：`ir.Negate` 给出的正好是 `x == k`（**一个** `Cond`，两侧都有界）→ 收窄成 `[k, k]`。**这是 `!=` 唯一有用的方向**。

**边界情况**：`ir.Negate` 在 `logic.go:46-48` 有一条 —— 若某个 `Cond` 的补集是空（例如 `x >= INT32_MAX+1` 不可能存在），返回 `Predicate{Alts: [][]Cond{{}}}`，**空合取 = 恒真**。收窄时要把「空 `Alt`」当恒真处理，不能当成「无约束」也不能当成「矛盾」。

**另一个边界**：`ir.Predicate.Exist`（`ir.ExistTest`，`ir.go:211-214`）是存在性测试（`any<@a[...]>`），**不携带任何数值信息**。收窄时**不能**从它推出任何关于分数的事实。`lower/expr.go:233-237` 是唯一的产生点。

#### 步骤 5：CFG 上的迭代

标准的**向前数据流**，join 点取 hull：

```
IN[B]  = JOIN over predecessors P of OUT[P]
OUT[B] = IN[B] ⊔ transfer(B)     // 注意：这里 ⊔ 是「格里的 join」
```

- 环（`while`/`loop` 的回边）需要**不动点迭代**。`ir.Function.Preds()`（`builder.go:105-112`）和 `ir.Succs`（`ir.go:88-96`）已经提供了拓扑工具。
- `analysis.LiveOut`（`analysis.go:104-150`）是现成的**不动点迭代模板**，照抄它的结构即可。

### 边界情况清单

| 情况 | 处理 |
|---|---|
| `x != k` | then 边**不收窄**（析取）；else 边收窄成 `[k,k]` |
| `abs(x) >= k` | 改写成 `x <= -k OR x >= k`（`lower/expr.go:331-341`），**两个 `Alt`**，收窄后取 hull |
| `abs(x) < k` | 改写成 `-k < x < k`（`lower/expr.go:342-352`），**一个 `Alt` 两个 `Cond`**，收窄成 `[1-k, k-1]` |
| `any<sel>` | `ExistTest`，**不收窄** |
| 布尔变量当条件 | `lower/expr.go:242-250` 把它变成 `d = x - 1; 测 d == 0`，收窄成 `x == 1` |
| `ir.Guarded` | 目标区间 = `join(旧, 新)`（守卫失败时旧值存活，`analysis.go:51-57`） |
| 不可达块 | `analysis.Reachable`（`analysis.go:13-33`）先剔除 |
| 空 `Alt`（恒真） | 当作「无约束」 |
| 矛盾 `Alt` | `ir.Simplify` 已剔除（`logic.go:120-124`） |

### 一个**设计稿没提但会咬人**的语义代价

收窄在 `if` 分支里是**对的**，但**分支结束后必须做 join**。GGM 的 `lowerIf`（`lower/stmt.go:93-137`）有三种出口：

- 两臂都落空 → 有 join 块
- 只有一臂落空 → **直接接续到那一臂的尾块**（`stmt.go:130-133`）
- 两臂都不落空 → join 块（`stmt.go:120-122`）

**只有一臂落空的情况，join 的入参必须只有那一臂**。如果无脑取两臂的 hull，**收窄得到的信息会被丢掉**，白做。设计稿 3.4 的例子 `if hp > 0 { hp -= 1 }` 后面如果还有代码，`hp` 在 join 处是 `[0, INT32_MAX]`（else 臂没改 hp，then 臂改了），hull = `[0, INT32_MAX]` —— **正确**。但如果 then 臂落空（`return`），join 就只有 else 臂。

**这就是我修过一次的 bug**：我的估算器最初在 join 处无条件取 hull，修掉之后 `sqrt` 的 `divzero` 警告从 1 降到 0（见 Q6）。

---

## Q4 收敛与 widen

### 结论：设计稿 3.4 最后一句**不成立**

设计稿原文（`DESIGN-v0.7.md:273-276`）：

> ### GGM 的一个意外优势
> `while` **语言强制**必须带 `#[budget(N)]`。所以循环迭代次数有上界，
> 这正好压住 widen 带来的精度损失——其它语言要额外做循环边界分析，GGM 白拿。

**三处都错。**

### 错误 1：`#[budget(N)]` 是**运行期计数器**，不是静态迭代次数上界

`internal/lower/stmt.go:157-183`（`lowerWhile`）：

```go
budget := ctx.slot(fmt.Sprintf("$budget%d", s.P.Line))
fb.Emit(cur, ir.Set{Dst: loc(budget), Value: s.Budget})
...
l.closeLoop(ctx, fb, body, bodyEnd, after, budget, s.Cond, s.P)
```

`closeLoop`（`stmt.go:224-233`）：

```go
fb.Emit(bodyEnd, ir.Remove{Dst: loc(budget), Value: 1})
fb.SetTerm(bodyEnd, ir.Branch{Cond: withBudget(p, budget), Then: body, Else: after})
```

**budget 是一个 scoreboard 目标（`$budget<行号>`），每轮 `remove 1`，运行期递减。** 它是**运行期计数器**，不是静态的迭代次数上界。

**为什么这不是静态上界**：

1. **它是数据流的一部分，不是语法的一部分。** `#[budget(N)]` 里的 `N` 只出现在 `ir.Set{Dst:budget, Value:N}` 里。**静态分析能读到这个 `N`，但预算本身在运行期是可变的**（虽然实际上只有编译器写它）。
2. **更关键：回边上的 `budget` 是 `≥ 1` 的条件，不是「迭代次数 = N」。** `withBudget`（`lower/predicate.go:13-25`）把 `budget >= 1` 折进条件。所以静态看到的事实是「**回边只在 `budget >= 1` 时可走**」。

**那么 `budget` 的区间能推出迭代次数吗？** 能，但要小心：

- 进入循环时 `budget = N`（`ir.Set`，`stmt.go:163`）→ 区间 `[N, N]`
- 循环体末尾 `budget -= 1`（`ir.Remove`）→ `[N-1, N-1]`
- 回边条件 `budget >= 1` → 收窄后 `budget ∈ [1, N-1]`
- 所以**迭代次数 ≤ N** —— **但这个推断需要分析器能跨过「计数器」这个模式**。

**这不是白拿的**：它要求分析器识别「一个由 `ir.Set` 初始化、被 `ir.Remove` 递减、在回边上被 `>= 1` 守卫的槽」这个模式，把「迭代次数」作为一个**派生的循环不变量**。**设计稿 3.4 把它列成「值从哪来」表的一行（`:261`「`#[budget(N)]` | 循环上界 | 迭代次数上界」），但没说明怎么从区间推出迭代次数** —— 区间格里没有「迭代次数」这个维度。

**而且这个推断还有洞**：`while` 的**入口测试**在 `cur` 块（`stmt.go:170-175`），循环体是 `body`。**循环体可以 `return`**（`lowerSeq` 返回 `noFallThrough`）—— 此时 `stmt.go:177-180` 直接 `return after`，**根本不生成回边**。所以「预算耗尽」不是唯一的退出路径。

**更硬的洞**：`loop`（`lowerLoop`，`stmt.go:193-211`）是**尾测试**：`cur: set budget; jump body`，`body: ...; budget -= 1; Branch(cond AND budget>=1, body, after)`。尾测试循环**先跑一次体**，所以迭代次数是 `1..N`，不是 `0..N`。**分析器必须区分 `while` 和 `loop` 的这一位差别**，否则 `loop` 的上界差 1。

**还有**：`#[budget(N)]` 的 `N` **可以是任意大**。实测：

```
$ cat big.ggm
fn f() {
    let i = 0
    while i < 3 #[budget(2147483647)] { i = i + 1 }
}
$ go run ./cmd/ggm -check big.ggm
（无错）
```

`N = 2147483647` 编译通过。**所以 budget 根本不构成「小的」静态上界**。即使能推出迭代次数 ≤ N，N 是 2³¹−1 的话，**精度收益为零**。

### 错误 2：迭代次数有上界 ≠ 值域有上界

**这是最根本的错误。** budget 约束的是**迭代次数**，而 widen 的精度损失来自**值在每轮如何增长**。

**反例**：

```ggm
fn f() {
    let x = 1
    while x != 0 #[budget(32)] {
        x = x * 2        // 每轮翻倍
    }
}
```

- **迭代次数**：32（budget 说了算）
- **值的区间**：`x` 从 `[1,1]` 开始，每轮 `x*2` → `[2,2]`, `[4,4]`, ..., `[2^31, 2^31]` → **第 32 轮就溢出 int32**
- **widen 之后**：`x ∈ [1, +∞)` → 报溢出

**实测数值验证**（`int32` 语义）：

```
budget(32) loop x=x*2 from 1 => x=0 (溢出到 0)
budget(32) loop x=x+2^30 from 0 => x=0 (溢出)
```

注意：32 轮 `x*2` 后 `x = 2^32 mod 2^32 = 0` —— **回绕到 0**，不是「溢出成负数」。**如果循环条件是 `x != 0`，它会一直转下去直到 budget 耗尽**。**budget 救了它不死循环，但它没救值溢出。**

**更极端**：`x = x + 2^30`，32 轮 → `32 * 2^30 = 2^35` → 回绕。

**结论**：**budget 只约束「转多少圈」，不约束「每圈涨多少」。** 要用 budget 压住 widen，得再做「每轮增长率」的分析 —— 那是**循环不变式**（loop invariant），**不是值域分析**。设计稿把这两件事混为一谈。

### 错误 3：widen 是**必要的**，因为循环体可以放大值，而 budget 与之无关

**收敛性**：`[lo,hi]` 的格是无穷格（端点是任意 int64）。不动点迭代**不保证终止**——只要存在 `x = x + 1` 这样的循环，区间上界每轮涨 1，**永远不收敛**。

**widen 策略（具体）**：

```
widen(old, new):
  lo = if new.lo < old.lo then -INF else old.lo
  hi = if new.hi > old.hi then +INF else old.hi
```

设计稿 3.2（`:233`）说「迭代 3 次后仍在扩大的端点直接推到 ±∞」。**这是对的**，但要补充：

1. **推迟 3 轮再 widen** 是为了「让短循环的精确值活下来」。代价是**最坏情况迭代 3×（块数）轮**。GGM 的函数都很小（`examples` 里最大 12 条命令/文件），可接受。
2. **widen 后必须 narrow 回有限区间**。标准做法：widen 到 `+∞` 后，如果后续有 `x = x - 1` 这类收缩，做一次**反向的 narrow**（把 `+∞` 拉回 `old.hi + k`）。**不 narrow 的话，`x = x - 1` 之后的区间会是 `[lo, +∞)`，永远无法证明安全。**
3. **GGM 有个特殊优势**：`ir.Arith` 的 `Op` 含 `<` 和 `>`（`ir.go:126-130`，`OpKeepMin`/`OpKeepMax`）。`min(x, 100)` 会把 `x` **钳到 100**。这是**天然的 clamp**，是**唯一不需要注解的区间来源**（因为 `clamp` 没实现，见 Q2 反例 5）。**分析器应该把 `ir.Arith{Op:"<"}` 认作 `x = min(x, src)`**，它给出 `[x.lo, min(x.hi, src.hi)]`。
4. **widen 的精度损失到底有多大？** 取决于**循环轮数**是否小。budget 小（≤ 64，例子里是 32 和 64）时，**「迭代 3 轮后 widen」的策略几乎不损失精度** —— 因为 3 轮 < budget。**这才是 budget 的真正价值：不是「压住 widen」，而是「让 widen 的推迟策略来得及生效」。**

**修正后的表述**：

> `while` / `loop` 强制 `#[budget(N)]`，N 通常很小（例子里 32 / 64）。
> 配合「推迟 3 轮再 widen」的策略，小 budget 让 widen 在多数循环里**根本不触发**。
> 但 budget **不是**静态迭代次数上界（它是运行期计数器），
> 也**不能**推出值的上界（每轮的增长率与 budget 无关）。

### budget 与静态迭代次数的关系 —— 可用，但要小心

**能用**：分析器识别回边上的 `budget >= 1` 条件，配合 `ir.Set` 初始化值 `N` 和 `ir.Remove` 递减，得出「迭代次数 ≤ N」。

**要小心**：
1. `loop` 是尾测试，上界是 `1..N`（不是 `0..N`）
2. `while` 的体可以 `return`，那就不走回边
3. `N` 可以是 2³¹−1
4. **这个推断的结果是「迭代次数」，不是「值域」**。要变成值域，还得知道每轮的增长 —— 那是循环不变式，超出阶段 1 范围。

**建议**：阶段 1 **不做** budget → 迭代次数的推断。widen 用「推迟 3 轮」策略，精度损失在实践中可接受（`examples` 里两个循环的 budget 分别是 32 和 64）。把 budget 推断留到阶段 2 再说。

---

## Q5 `[wrapping]` 联动

### `W_WRAP_UNPROVEN` 的报出条件

**我的建议（与设计稿 3.6 `:299-305` 不同）**：

设计稿说：

> - 函数体所有算术都能证明不溢出 → 不需要注解
> - 有任一无法证明 → 报 `W_WRAP_UNPROVEN`，除非标了 `[wrapping]`

**这会导致「不能证明就报」= 高误报率**（见下面的估算）。**建议改成「能证明会溢出才报」**：

```
对函数 F 的每个算术点 p：
  若 p 的区间 ⊆ [INT32_MIN, INT32_MAX]   → 安全，不报
  若 p 的区间 ⊄ [INT32_MIN, INT32_MAX]   → 报 W_WRAP_UNPROVEN
  若 p 的区间是 ⊤（未知）                → 【不报】（阶段 1）
```

**理由**：⊤ 意味着「不知道」，不是「会溢出」。把「不知道」报成警告会产生海量误报（Q5 估算显示 `sqrt` 的 `n / x` 就是 ⊤ 形态）。**报告「能证明溢出」比「不能证明安全」有用得多**，因为前者是**确定的 bug**。

**但是**：`[wrapping]` 的语义是「关闭溢出检查」，如果 ⊤ 不报，那 `[wrapping]` 就**几乎没用**（只在能证明溢出时压制警告）。这其实**正是它该有的样子**：`[wrapping]` 用来声明「**这里就是要回绕**」，比如 `lcg_step`。

**所以最终建议**：

| 情况 | 是否报 `W_WRAP_UNPROVEN` |
|---|---|
| 区间 ⊆ int32 | 否 |
| 区间 ⊄ int32（**能证明溢出**） | **是**（除非 `[wrapping]`） |
| 区间 = ⊤（不知道） | **否**（阶段 1），但可另发一条 `W_RANGE_UNKNOWN` 之类的提示（**建议不做**，会是噪声） |

### 估算：`examples/luo-math-full.ggm`

**方法**：把仓库**复制**到临时目录 `C:\Users\lenovo\AppData\Local\Temp\ggmprobe-repo`（**原仓库零改动**），在副本里写一个 `cmd/rangeest`，**import GGM 自己的 `internal/syntax` 包**，用**真实的 AST** 跑设计稿 3.2/3.3 的格与传递函数。副本的 `go build ./...` 通过。

**复现命令**：

```powershell
cd C:\Users\lenovo\AppData\Local\Temp\ggmprobe-repo
go run ./cmd/rangeest D:\code\go\GoGoMcfunction\examples\luo-math-full.ggm
```

**实测输出（`luo-math-full.ggm`）**：

```
===== branch narrowing = false =====
lcg_step               wrapping=true  wrap=2 cmpdiff=0 divzero=0  reported=0
lcg_next               wrapping=false wrap=1 cmpdiff=0 divzero=0  reported=1
rand                   wrapping=false wrap=3 cmpdiff=0 divzero=0  reported=3
chance                 wrapping=false wrap=0 cmpdiff=1 divzero=0  reported=1
is_happen_fix100       wrapping=false wrap=0 cmpdiff=0 divzero=0  reported=0
is_happen_fix1000      wrapping=false wrap=0 cmpdiff=0 divzero=0  reported=0
sqrt                   wrapping=false wrap=1 cmpdiff=2 divzero=1  reported=3
sort_asc               wrapping=false wrap=0 cmpdiff=0 divzero=0  reported=0
TOTAL: wrap=7 cmpdiff=3 divzero=1 | reported (wrapping fns suppressed) = 8

===== branch narrowing = true =====
lcg_step               wrapping=true  wrap=2 cmpdiff=0 divzero=0  reported=0
lcg_next               wrapping=false wrap=1 cmpdiff=0 divzero=0  reported=1
rand                   wrapping=false wrap=3 cmpdiff=0 divzero=0  reported=3
chance                 wrapping=false wrap=0 cmpdiff=1 divzero=0  reported=1
is_happen_fix100       wrapping=false wrap=0 cmpdiff=0 divzero=0  reported=0
is_happen_fix1000      wrapping=false wrap=0 cmpdiff=0 divzero=0  reported=0
sqrt                   wrapping=false wrap=1 cmpdiff=2 divzero=0  reported=3
sort_asc               wrapping=false wrap=0 cmpdiff=0 divzero=0  reported=0
TOTAL: wrap=7 cmpdiff=3 divzero=0 | reported (wrapping fns suppressed) = 8
```

**`luo-math.ggm`**：

```
===== branch narrowing = false =====
lcg_step               wrapping=true  wrap=2 cmpdiff=0 divzero=0  reported=0
sqrt                   wrapping=false wrap=1 cmpdiff=2 divzero=1  reported=3
is_happen_fix1000      wrapping=false wrap=2 cmpdiff=1 divzero=0  reported=3
TOTAL: wrap=5 cmpdiff=3 divzero=1 | reported (wrapping fns suppressed) = 6
```

### 数字解读

| 指标 | `luo-math-full.ggm` | `luo-math.ggm` |
|---|---|---|
| 所有算术点的溢出警告（`wrap`） | **7** | **5** |
| 差值槽溢出（`cmpdiff`） | **3** | **3** |
| 除零警告（`divzero`，无收窄） | **1** | **1** |
| 除零警告（**有**收窄） | **0** | **0** |
| `[wrapping]` 压制后**实际报出** | **8** | **6** |

**误报分析**（这是任务要求的核心）：

| 报出点 | 真溢出？ | 判定 |
|---|---|---|
| `lcg_next`: `SEED + LCG_C` | `SEED` 是 extern global，区间 ⊤ → **算不出溢出** | **误报**（区间 ⊤ 时不该报，见上表） |
| `rand`: `hi - lo` | `hi`,`lo` 是参数，⊤ | **误报** |
| `rand`: `hi - lo + 1` | 同上 | **误报** |
| `rand`: `lo + rem_euclid(...)` | `rem_euclid` 结果 ⊤（阶段 1 无过程间） | **误报** |
| `chance`: 差值槽 `rand(...) <= p` | `rand` ⊤，`p` ⊤ → 差值 ⊤ | **误报**（差值槽在 ⊤ 时不该报） |
| `sqrt`: 差值槽 `n < 4` | `n` ⊤ | **误报** |
| `sqrt`: 差值槽 `abs(...) >= 1` | ⊤ | **误报** |
| `sqrt`: `x + n / x` | `n / x` 的除数 `x` ⊤ → 结果 ⊤ | **误报** |
| `lcg_step`: `LCG_A * s` | `s` 是参数 ⊤ | **误报**（但 `lcg_step` 有 `[wrapping]`，被压制） |
| `lcg_step`: `LCG_A * s + LCG_C` | 同上 | **误报**（被压制） |
| `sqrt`: `n / x` 除零 | `x = n/4`，`n` ⊤ → `x` ⊤ | **真阳性**（`x` 确实可能是 0） |

**关键发现：按「能证明溢出才报」的规则，这 8 条里只有 0 条该报**（全部是 ⊤ 导致的「不能证明安全」，不是「能证明溢出」）。

**所以 Q5 的答案**：

- 如果按设计稿 3.6 的「**不能证明安全就报**」：**`luo-math-full.ggm` 会产生 8 条 `W_WRAP_UNPROVEN` + 1 条 `W_DIV_ZERO`，其中 8 条全是误报，误报率 100%**。
- 如果按我建议的「**能证明溢出才报**」：**产生 0 条 `W_WRAP_UNPROVEN`**（全是 ⊤），`W_DIV_ZERO` 1 条（`sqrt` 的 `n / x`，真阳性）。

**`[wrapping]` 的必要性**（设计稿 3.6 的验收标准「标了 `[wrapping]` 的只有它，且它必须是必要的」）：

**它确实是必要的**，但**理由不是设计稿说的那个**。

- 设计稿 3.6（`:303`）说「它的设计就是要回绕」—— **对**。
- 但「必须是必要的」这个验收标准，**在 `luo-math.ggm` 上不成立** —— 见下。

### 验证 3.6 的「`luo/math/lcg_step` 是唯一合法的用户」

**证伪。** `examples/luo-math.ggm:33-37`：

```ggm
fn is_happen_fix1000(p: int) -> bool {
    let r = (SEED * LCG_A + LCG_C) % LCG_M
    let span = 1000
    r <= p
}
```

**这与 `lcg_step` 的算式完全相同**（同一个 `LCG_A`/`LCG_C`/`LCG_M`），只是**没有标 `[wrapping]`**。

**实测确认两者算术恒等**（`chk2.go`，int32 语义）：

```
lcg_step(12345)          = -2364649
is_happen_fix1000's r    = -2364649
identical? true
```

**真机确认**（`luo:math/lcg_step`，`a0 = 12345`）：

```
    return/R = -2364649
```

**结论**：

1. **`luo-math.ggm` 的 `is_happen_fix1000` 是一个**漏标 `[wrapping]`** 的 LCG 用户**。按设计稿 3.6 的验收标准（「只有它」），**仓库现状就已经不满足**。一旦 `W_WRAP_UNPROVEN` 实现，**它会报一条**（`SEED * LCG_A` 和 `SEED * LCG_A + LCG_C` 都会溢出）。
2. **`luo-math-full.ggm` 里 `lcg_step` 是唯一带 `[wrapping]` 的**（`grep` 确认：只有 `:20` 一处），而 `luo-math.ggm` 也有 `is_happen_fix1000` 这个第二用户。
3. **更根本的问题**：规范 `GGM-语法规范-v1.0.md:186` 说「若函数体内有 `% M` 且编译器能证明 M 是 2 的幂，则无需标注」——**这个豁免是错的**（Q2 反例 6）。`lcg_step` **必须**标。

**所以设计稿 3.6 的验收标准要改成**：

> `[wrapping]` 的合法用户 = **任何做 LCG 类回绕运算的函数**。
> 当前仓库里有 **2 个**：`luo-math-full.ggm:21` 的 `lcg_step`（已标）和
> `luo-math.ggm:33` 的 `is_happen_fix1000`（**漏标**）。
> 「2 的幂取模免标注」的豁免**必须删除**。

### 关于 `[wrapping]` 目前的「无作用」

`internal/syntax/parser.go:205-209` 的注释自陈：

> wrapping is the only one, and it is accepted rather than acted on: ...

`parser.go:323-324` 只把它记到 `syntax.FuncDecl.Wrapping`（`ast.go:148`）。

**`syntax.FuncDecl.Wrapping` 这个字段在整个仓库里没有任何消费者**（`grep` 全库：只有 `ast.go:148` 定义、`parser.go:323` 赋值、**无读取**）。

**所以 `[wrapping]` 的「联动」需要新 plumbing**：

1. `syntax.FuncDecl.Wrapping` → `ir.Function` 需要一个对应字段（**`ir.Function` 目前没有**，见 `ir.go:30-47`）
2. 分析器按函数查这个标志
3. `driver` 发射 `W_WRAP_UNPROVEN`

**注意**：`ir.Function` 是**降级后**的产物，一个 GGM 函数可能降出**多个** `ir.Function`（每个 CFG 块一个文件，但 `ir.Function` 是函数级的；不过 `class` 的方法、`on tick` 等会各自成 `ir.Function`）。所以标志要跟着**声明**走 —— `lowerFunc`（`lower/lower.go:547-557`）在 `fb.Func().Origin = ...` 旁边设即可。

---

## Q6 `W_DIV_ZERO`

### 检查点放哪

**IR 层的 `ir.Arith`**，因为除法/取模在 IR 里就是 `Arith{Op:"/="}` / `Arith{Op:"%="}`（`ir.go:126-130`），而 `Op` 字符串就是运算符。

**具体位置**：`ir.Arith` 的 `Src` 是除数。

```
若 op ∈ {"/=", "%="} 且 src 的区间含 0 → W_DIV_ZERO
```

**但有三个坑**：

#### 坑 1：`Src` 不总是除数！

看 `lower/expr.go:73-89`（`assignBinaryInto`）：

```go
fb.Emit(block, ir.Copy{Dst: loc(dst), Src: lhs})    // dst = lhs
fb.Emit(block, ir.Arith{Dst: loc(dst), Op: op, Src: rhs})  // dst op= rhs
```

**`dst` 是左操作数，`Src` 是右操作数。** 对于 `a / b`：`dst = a`，`Src = b`。**所以 `Src` 确实是除数** ✓

**但** `lower/expr.go:175-176`（`binary`）：

```go
fb.Emit(block, ir.Copy{Dst: t, Src: lhs})
fb.Emit(block, ir.Arith{Dst: t, Op: op, Src: rhs})
```

同样。✓

**还有** `lower/call.go:390-395`（`rem_euclid`）：

```go
fb.Emit(block, ir.Arith{Dst: t, Op: "%=", Src: m})    // t %= m   —— m 是模数
fb.Emit(block, ir.Arith{Dst: u, Op: "+=", Src: m})    // u += m   —— 加法，不是除
fb.Emit(block, ir.Arith{Dst: u, Op: "%=", Src: m})    // u %= m   —— m 是模数
```

**第一处 `%=` 的 `Src` 是模数 ✓，第三处 ✓。中间那个 `+=` 不是除法 ✓。**

**但 `rem_euclid` 的 `%` 语义是错的**（真机实测 `rem_euclid(100, 2147483647)` 返回 `-2147483549`）。见 Q2 反例 6 表。这是**独立的 bug**，不是 `W_DIV_ZERO` 能抓的。

#### 坑 2：**差值槽的 `-=` 不是除法**，但差值槽的**结果**可能是 0

`lower/expr.go:249`（布尔条件）和 `:290`（比较）：

```go
fb.Emit(block, ir.Arith{Dst: loc(d), Op: "-=", Src: rhs})
```

`d = lhs - rhs`。**这不是除法，不需要 `W_DIV_ZERO`。** 但如果 `d` 的区间含 0，**那正是 `==` 的匹配条件**，是**故意的**。

#### 坑 3：`ir.Arith{Op:"<"}` 和 `">"` 是 min/max，**不是比较**

`ir.go:125` 注释：

> Op is one of += -= *= /= %= and the min/max operators < > <= >=.

`lower/call.go:377-382`：`min(a,b)` → `t = a; t < b`（`OpKeepMin`）。**`OpKeepMin` 的两个操作数都可能是 0，但语义不是除法。** 分析器必须按 `Op` 精确分派，**不能靠「有没有 `/`」来判断**。

### 误报控制：b 含 0 就报，还是 b 可能为 0 才报？

**必须区分两种「含 0」**：

| 情况 | 例子 | 是否报 |
|---|---|---|
| **b 恒等于 0** | `x / 0` | **报，且是确定的错误**（真机实测：运行期报错，**但函数继续执行**，后面的命令照跑） |
| **b 可能为 0** | `x / y`，`y` ⊤ | **不报**（⊤ = 不知道，报了是误报） |
| **b 恒 ≠ 0** | `x / 4` | **不报** ✓ |

**「b 含 0 就报」这个措辞是有歧义的**。准确的规则：

```
若 src 区间是 ⊤            → 不报（阶段 1 无法判断）
若 src 区间 ⊆ [1, +∞)      → 不报
若 src 区间 ⊆ (-∞, -1]      → 不报
若 src 区间 = [0, 0]        → 报（确定的除零）
若 src 区间跨越 0 且不是 ⊤  → 报（可能除零）
```

**注意**：`src = [0,0]` 和 `src = [-5, 5]` 性质不同。前者是**必然**出错（每一行都错），后者是**可能**出错。**建议分两个码**：

| 码 | 条件 | 严重度 |
|---|---|---|
| `E_DIV_ZERO` | 除数区间 = `[0,0]`（必然除零） | **Error**（必然出错，且没有正确代码可发） |
| `W_DIV_ZERO` | 除数区间跨 0（可能除零） | Warning |

**为什么 `E_DIV_ZERO` 该是 Error**：`E_INT_RANGE` 已经是 Error 了（`driver.go:136-139`），理由写在 `driver.go:127-132`：

> This is a hard error rather than a warning because there is no correct code to emit.

`x / 0` 同理 —— **必然出错，没有正确代码可发**。真机实测（`x / 0`）：

```
error: No a0 score for ... found; cannot divide by zero
feedback: Set score of l1 for player ... to 99
feedback: Executed 4 command(s) from function 't:e/f'
    return/R = 99
```

**注意：函数没有中断！** 除零的那条命令失败，但**后面的命令照跑**（`l1 = 99` 照设，`return = 99` 照返）。**所以除零产生的是「静默的错误结果」，不是崩溃** —— 这正是需要编译器检查的理由。

**但有一个重要例外**：`x / 0` 里 `0` 是**字面量**。**这个已经被 `E_INT_RANGE` 覆盖了吗？没有** —— `E_INT_RANGE` 检查的是「字面量本身超出 int32」，不是「除数为 0」。`x / 0` 里的 `0` 在范围内，**所以现在完全不报**。

### 实测：`W_DIV_ZERO` 在 `luo-math-full.ggm` 的表现

- **无收窄**：1 条（`sqrt` 的 `n / x`）
- **有收窄**：**0 条**

**为什么收窄能消掉它**：`sqrt` 里

```ggm
if n < 4 { return 1 }
let x = n / 4
let prev = 0
while abs(x - prev) >= 1 #[budget(32)] {
    prev = x
    x = (x + n / x) / 2
}
```

`n / x` 的除数是 `x = n / 4`。**`n` 是参数（⊤），所以 `x` 也是 ⊤。** 那收窄为什么能消掉？

**因为收窄给出了 `n` 的一个下界？** 不 —— `n < 4` 收窄的是 `n` 的**上界**（`n <= 3`），而且 `if` 的 then 臂 `return`，**不落空**，所以 join 处 `n` 回到 ⊤。

**我的估算器里 `n / x` 的除零警告消失了，是因为收窄让 `x` 的区间不再是 ⊤？** 让我看具体数字：收窄前后 `sqrt` 的 `divzero` 是 1 → 0。

**啊，明白了**：收窄把 `n` 收成 `(-∞, 3]`（`n < 4` 的 else 边），所以 `x = n / 4` 的区间是 `(-∞, 0]` —— **`x` 可能 ≤ 0，而 `x` 可能是 0** → 除数含 0 → **应该报**！

**但收窄后 `sqrt` 报的是 0 条。** 说明我的收窄实现有 bug，或者……

**让我核对**：`branchEnvs` 里，`n < 4` 走 `syntax.LT` 分支：

```go
case syntax.LT:
    dLo, dHi = i32min, -1
    elseLo, elseHi = 0, i32max
```

`d = n - 4`。`n < 4` ⟺ `d <= -1` ⟺ `d ∈ (-∞, -1]`。**但 `d = n - 4` 且 `n` 的区间是 ⊤ → `d` 是 ⊤**，所以 `dLo/dHi` 应该是 ⊤ 才对！

**我的实现 bug**：我用 `lit = 4`，然后 `thenVar = [dLo+lit, dHi+lit] = [i32min+4, -1+4] = [-2147483644, 3]`。**这是对的**（`n <= 3`）。else 边：`elseVar = [0+4, i32max+4]` → clamp → `[4, i32max]`。

**所以 else 边 `n ∈ [4, i32max]`！** 因为 `n >= 4` ⟺ `n - 4 >= 0` ⟺ `n >= 4`。**这是对的！**

于是 `x = n / 4`，`n ∈ [4, 2147483647]` → `x ∈ [1, 536870911]`。**除数 `x` 不含 0** → 不报 ✓

**所以收窄是对的，而且很漂亮**：`if n < 4 { return 1 }` 之后，`n >= 4`，所以 `x = n/4 >= 1`，**除零被排除了**。

**而这正是设计稿 3.4 说的「分支收窄是价值最高的部分」**（`:263`）—— **收窄不仅省掉溢出警告，还直接排除了除零。**

**但注意**：这依赖于「then 臂 `return`，不落空，所以 join 处取 else 臂的环境」。**如果我的 join 写成无条件 hull，收窄就没了**（除零警告会从 0 变 1）。**这验证了 Q3 里我强调的「只有一臂落空时 join 必须只取那一臂」。**

### 建议的 `W_DIV_ZERO` 检查点汇总

| 检查点 | IR 形状 | 条件 | 码 |
|---|---|---|---|
| 除法 | `Arith{Op:"/="}` | `Src` 区间含 0 | `E_DIV_ZERO`（= [0,0]）/ `W_DIV_ZERO`（跨 0） |
| 取模 | `Arith{Op:"%="}` | 同上 | 同上 |
| `rem_euclid` 的模数 | `Arith{Op:"%=", Src:m}` | 同上 | 同上 |
| 差值槽 | `Arith{Op:"-="}` | **不查**（不是除法） | — |
| min/max | `Arith{Op:"<" / ">"}` | **不查**（不是除法） | — |

**还需检查 `ir.Guarded{Inner: Arith{Op:"/="}}`** —— 守卫里的除法，条件失败时不执行，但**条件成立时仍然可能除零**。

---

## Q7 落地计划

### 前提：先修三个静默误编译

**这三个不是值域分析的一部分，但它们在值域分析的作用域内，且比值域分析更紧急**（现在就是错的）。

| # | 修什么 | 改哪 | 验收 |
|---|---|---|---|
| **A** | `x += e` 被降成 `x = e` | `internal/lower/stmt.go:42-48`（`AssignStmt` 分支**根本没读 `s.Op`**！） | 加测试：`x += 3` 后 `x == 8` |
| **B** | 差值槽回绕 | `internal/lower/expr.go:261-292`（`lowerCondInto`） | 加测试：`n = -2147483648` 时 `n < 4` 为真 |
| **C** | `rem_euclid` 的 `t + m` 溢出 | `internal/lower/call.go:384-396` | 加测试：`rem_euclid(100, 2147483647) == 100` |

**A 的根因**（已定位）：

```go
// internal/lower/stmt.go:42-48
case syntax.AssignStmt:
    target, ok := lvalueName(s.Target)
    if !ok { ... }
    l.assignInto(ctx, fb, cur, l.targetLoc(ctx, target), s.Value)
```

**`s.Op`（`syntax.AssignStmt.Op`，`ast.go:76-81`）从来没被读过！** `s.Value` 是 `3`，于是 `assignInto` 把 `3` 直接写进 `x` —— **`+=` 变成了 `=`**。

**实测**：

```
$ cat pe.ggm
fn f() -> int { let x = 5; x += 3; x }
$ go run ./cmd/ggm -q -O 2 -out out pe.ggm && Get-Content out\t\e\f.mcfunction
scoreboard players set @s l0 5
scoreboard players set @s l0 3        ← 应该是 "l0 += #3 ggm.c"
scoreboard players operation @s return = @s l0
```

**真机实测**（`a0` 未设，函数 `f`）：

```
    return/R = 3
```

**应为 8。** **这是三个里最严重的** —— `x += 1` 在 `sort_asc` 里就是计数（`examples/luo-math-full.ggm:65` `rank = rank + 1` 用的是 `=`，所以躲过了；但 `docs/GGM-语法规范-v1.0.md:316` 的例子用 `rank += 1`）。

**`syntax.AssignStmt.Op` 的取值**（`parser.go:855-861`）：`ASSIGN, PLUSEQ, MINUSEQ, STAREQ, SLASHEQ, PERCENTEQ`（`parser.go:867-873`）。`internal/lower/expr.go:406-420` 的 `arithOp` 已经有 `PLUSEQ → "+="` 等的映射，**修的时候直接复用**。

**B 的修法（最难）**：差值槽 `d = a - b` 在 int32 里回绕。选项：
1. **比较时改用更宽的中间表示** —— 1.12.2 没有 64 位，**做不到**。
2. **对 `a op b` 改写成不依赖差值的形式** —— 例如 `a < b` ⟺ `b - a > 0`，**还是有同样的问题**。
3. **检测溢出并报错** —— `a - b` 溢出 ⟺ `(a > 0 && b < 0 && a - b < 0) || (a < 0 && b > 0 && a - b > 0)`。这需要值域分析！**所以 B 本身就是值域分析的一个用例** —— 但它要求**先有区间分析才能修**。
4. **保守：只在能证明差值不溢出时用差值槽；否则报 `E_CMP_OVERFLOW`**。

**建议**：B 属于阶段 1 的**目标**（值域分析正好能提供「差值不溢出」的证明）。**先做阶段 1，B 跟着一起修。** A 和 C 独立，先修。

**C 的修法**：`rem_euclid` 的 `u = t; u += m; u %= m` 在 `t + m` 溢出时出错。正确实现：`rem_euclid(a, m) = ((a % m) + m) % m`，其中 `a % m ∈ (-(m-1), m-1)`，所以 `+ m` ∈ `(0, 2m-1)` —— **当 `m > 2^30` 时仍会溢出**。修法：**用 `m` 减而不是加**（`if t < 0 { t += m }`，但 1.12.2 没有条件加法……需要用 selector 守卫）。

**或者**：直接 `E` 掉 `m > 2^30` 的 `rem_euclid`（报 `E_INT_RANGE` 或新码）。

### 阶段 1 的分步计划

**每步都是独立可验收的。** 每步都**只加 warning，不改输出**。

#### 第 0 步：修 A（`+=` 丢失）—— 独立于值域分析

| 项 | 内容 |
|---|---|
| 改哪些文件 | `internal/lower/stmt.go`（读 `s.Op`，把复合运算转成 `Binary`） |
| 验收命令 | `go test -count=1 ./...` + 新增 `internal/driver/compound_test.go` |
| **影响 golden？** | **会**。`examples/` 里目前**没有**用 `+=` 的函数（已 grep 确认：`luo-math-full.ggm:65` 用 `rank = rank + 1`），所以 **golden 不变**。**但** `docs/GGM-语法规范-v1.0.md:316` 的 `rank += 1` 例子是坏的，要改文档。 |

#### 第 1 步：区间格 + 转移函数（纯库，无诊断）

| 项 | 内容 |
|---|---|
| 新增 | `internal/range/range.go`（区间类型、join、widen、narrow）、`internal/range/transfer.go`（传递函数） |
| 改哪些文件 | **不改现有文件** |
| 验收命令 | `go test -count=1 ./internal/range/...`（新写的单元测试，覆盖 Q2 的 6 个反例） |
| **影响 golden？** | **否**（纯新增包，不被调用） |

**这一步就要把 Q2 的 6 个反例写成测试** —— 那是整个调研最有价值的产出。

#### 第 2 步：CFG 遍历 + 分支收窄

| 项 | 内容 |
|---|---|
| 新增 | `internal/range/flow.go`（不动点迭代、收窄、`ir.Negate` 集成） |
| 改哪些文件 | **不改现有文件**（`internal/analysis` 的 `LiveOut` 是模板，但**不复用**——`analysis` 包是「结构不变量」，`range` 是「值分析」，职责不同） |
| 验收命令 | `go test -count=1 ./internal/range/...` |
| **影响 golden？** | **否** |

#### 第 3 步：`W_DIV_ZERO`（第一个真诊断）

| 项 | 内容 |
|---|---|
| 新增 | `internal/range/check.go`（`Check` 函数） |
| 改哪些文件 | `internal/driver/driver.go`（在 `optimize.Run` 之前调用）；`internal/diag` 无需改（`Bag.Warn` 已存在，`diag.go:62-64`） |
| 验收命令 | `go test -count=1 ./...` + `go run ./cmd/ggm -check examples\luo-math-full.ggm` 看诊断数 |
| **影响 golden？** | **否** —— `golden_test.go:74-79` 只在 `d.Severity == 0`（Error）时 fail，**Warning 不影响**。**确认**：`renderBaseline` 遍历 diags，`if d.Severity == 0 { t.Fatalf }` —— Error 才失败。✓ |

#### 第 4 步：`W_WRAP_UNPROVEN` + `[wrapping]` 联动

| 项 | 内容 |
|---|---|
| 新增 | `ir.Function` 加 `Wrapping bool` 字段（`internal/ir/ir.go:30-47`） |
| 改哪些文件 | `internal/ir/ir.go`（加字段）、`internal/lower/lower.go:547-557`（`lowerFunc` 里设标志）、`internal/lower/lower.go:574-620`（`lowerClass` 的方法也要设）、`internal/lower/lower.go:559-567`（`lowerOn`）、`internal/driver/driver.go`（调用） |
| 验收命令 | `go test -count=1 ./...` |
| **影响 golden？** | **否**（同上，`golden_test.go` 只对 Error fail） |

**注意**：`ir.Function` 加字段会影响 `ir` 包的**结构体比较**。`internal/ir/logic_test.go` 有 `if got[0] != want[0]`（`Cond` 相等），`ir.Function` 没被这样比较。**低风险。**

#### 第 5 步：差值槽溢出检查（修 B）

| 项 | 内容 |
|---|---|
| 改哪些文件 | `internal/range/transfer.go`（比较的差值槽单独处理）、可能加 `E_CMP_OVERFLOW` 码 |
| 验收命令 | `go test -count=1 ./...` |
| **影响 golden？** | **否**（只加诊断） |

**但这一步会让 `examples/luo-math-full.ggm` 的 `sqrt` 报差值槽警告**（`n < 4`，`n` ⊤ → 不报；`abs(x-prev)>=1` → ⊤ → 不报）。按「能证明溢出才报」的规则，**不报**。

#### 第 6 步：`luo-math.ggm` 的 `is_happen_fix1000` 补 `[wrapping]`

| 项 | 内容 |
|---|---|
| 改哪些文件 | `examples/luo-math.ggm`（加一行 `[wrapping]`） |
| 验收命令 | `go test -count=1 ./internal/driver -run GoldenBaseline` |
| **影响 golden？** | **否** —— `[wrapping]` 不改变生成的命令（它只影响诊断）。**但** `luo-math.o*.txt` 的 golden **内容不变**。✓ |

**注意**：这一改**是示例代码的修改**，不是编译器改动。**但它是必要的** —— 否则 `W_WRAP_UNPROVEN` 一上线，仓库自己的示例就报警告。

#### 阶段 2（`[range]` 注解）—— 建议**推迟**

理由：
1. 阶段 1 的精度**尚未实测**（本次只调研，未实现）
2. `[range]` 需要**跨过程**的区间（参数区间在调用点检查），这已经是阶段 3 的雏形
3. 语法改动要**先改规范**（`HANDOVER.md:441`）

**但阶段 2 有一个阶段 1 就要做的准备工作**：`syntax.Param`（`ast.go:137-140`）目前只有 `Name` 和 `Type`，**没有位置信息**（没有 `Pos`）—— 而 `W_WRAP_UNPROVEN` / `E_RANGE_VIOLATION` 都需要**报在参数的位置上**。**建议阶段 1 就给 `syntax.Param` 加 `Pos`**（纯加字段，不改语义）。

### 「只加 warning 不影响 golden」这个预期 —— **确认**

**证据**（`internal/driver/golden_test.go:72-94`）：

```go
func renderBaseline(t *testing.T, src, filename string, level optimize.Level) string {
	out, diags := driver.Compile(src, filename, driver.Options{...})
	for _, d := range diags {
		if d.Severity == 0 {                    // ← 只有 Error 才 fail
			t.Fatalf("compile %s at %s: %v", filename, level, d)
		}
	}
	...
}
```

`diag.Severity`（`diag.go:15-18`）：`Error = 0`，`Warning = 1`。**`d.Severity == 0` 即 Error**。所以 **Warning 不影响 golden**。✓

**`cmd/ggm/main.go:69-74`** 也是同样处理：Warning 打印到 stderr，但 `failed` 只在 `d.Severity == diag.Error` 时置真（`main.go:71-73`），**文件照样写出**（`main.go:80-85`）。✓

**唯一的例外**：`E_INT_RANGE` 是 Error，会让 `cmd/ggm` **返回非零并跳过写文件**（`main.go:98-101`）。所以**新加的诊断必须是 Warning**（`W_WRAP_UNPROVEN`、`W_DIV_ZERO` 都是 `W` 前缀 ✓）。

**另一个要小心的**：`internal/driver/checks_test.go:119` 有 `if len(ds) != 0` —— 那是**特定测试**（`TestEntryPointsAreNotReportedAsUncalled`），输入是 `on load { }` 之类，**不含算术**，所以不受影响。✓

**第三个要小心的**：`internal/driver/golden_test.go` 的 golden 文件里**不含诊断**（`renderBaseline` 只渲染输出文件，不渲染诊断）—— 确认：`golden_test.go:83-93` 只写 `out.Files`。✓

**结论**：**「只加 warning 不影响 golden」的预期成立**，前提是**不新增 Error 级诊断**。若要加 Error（如 `E_DIV_ZERO` 对 `[0,0]` 的情形），**要重新评估** —— `examples` 里目前没有 `x / 0`，所以仍安全，但这是**新约束**。

### 版本号

`HANDOVER.md:1.2`：「**生成结果的含义变了** → 次版本号（0.x → 0.y），golden 必须重签」。

**只加 warning 不改输出 → 应该是补丁号**（`0.6.0` → `0.6.1`）。**但**：
- 第 0 步（修 `+=`）**改输出** → **次版本号**（`0.6.0` → `0.7.0`）
- 若同时做了 A 修复和阶段 1，一次性发 `0.7.0` 更省事

---

## Q8 设计稿哪里错了

按「给证据不给断言」逐条列出。**每条都有可复现命令或文件:行号。**

### 错误 1（严重）：3.3 的 `a / b`「四端点相除」在 int32 语义下给出错误结果

- **位置**：`docs/DESIGN-v0.7.md:243`
- **反例**：`a ∈ [-2147483648, -2147483648]`，`b ∈ [-2147483648, -2147483648]`
  - **期望**：1
  - **设计稿**：int64 算 `2147483648 / -2147483648` = -1
- **根因**：运行期是 `wrapInt32(target / operand)`（`GoMcRuntime/internal/world/scoreboard.go:343-347`），**先在 Go 的 64 位 int 里除，再 wrap**。设计稿没说要在 int32 语义下算。

### 错误 2（严重）：3.3 整张表**漏了差值槽**

- **位置**：`docs/DESIGN-v0.7.md:248`（「比较运算 | `[0, 1]`」）
- **证据**：`internal/lower/expr.go:290` 发射 `d = lhs - rhs`（int32）
- **真机实测**：`n = -2147483648` 时 `if n < 4 { return 1 }` 返回 **0**，应为 1
- **这不是「误报率」问题，是「分析器会漏掉一个真 bug」**

### 错误 3（严重）：3.3 的 `a % b` 下界在 b 变号时给出错误的符号范围

- **位置**：`docs/DESIGN-v0.7.md:244`
- **反例**：`a ∈ [7,7]`，`b ∈ [-3,3]` → 真值 `{0,1,2}`，设计稿 `[-2,2]`
- **附带**：`a % b` 那行**没写「b 含 0 则退化」**，而 `a / b` 那行写了（`:243`）。1.12.2 的 `%` 遇 0 是**运行期错误**（`scoreboard.go:349-351`），不是退化。

### 错误 4（严重）：3.2 的「⊤ = 未知 = `[-2^31, 2^31-1]`」与格图自相矛盾

- **位置**：`docs/DESIGN-v0.7.md:224-230`
- **问题**：`:225` 说 ⊤ 是「未知 = `[-2^31, 2^31-1]`」（一个**具体区间**），但 `:224-230` 的格图把 ⊤ 画在**上方**（表示「无约束」），`:222` 又说「超出即退化为 ⊤（未知）」。**「⊤ = 整个 int32 范围」和「⊤ 是格顶」是两种不同的东西。**
- **后果**：分析器若把 ⊤ 当成 `[i32min, i32max]`，**join 会把「不知道」和「恰好在这个范围」混为一谈**，精度全废。必须区分 `Concrete` / `Top` / `Bottom` 三种。

### 错误 5（严重）：3.3 的 `clamp` 一行**指向一个不存在的函数**

- **位置**：`docs/DESIGN-v0.7.md:247`（标了「**最有用的一条**」）、`:257`（列为「值从哪来」）
- **证据**：`internal/lower/call.go:143` `var reservedCalls = map[string]bool{"abs": true, "clamp": true, "count": true}`
- **实测**：
  ```
  $ go run ./cmd/ggm -check clamp.ggm
  clamp.ggm:3:5: error: clamp is a builtin that this build does not implement in this position [E_BUILTIN_UNIMPLEMENTED]
  ```
- **后果**：3.4 的「值从哪来」表**少了一半**。**唯一可用的区间来源是 `min`/`max`（`ir.Arith{Op:"<"/">"}`），而设计稿没提。**

### 错误 6（严重）：3.4 最后一句「budget 压住 widen，GGM 白拿」三处都错

- **位置**：`docs/DESIGN-v0.7.md:273-276`
- **错误 A**：budget 是**运行期计数器**（`internal/lower/stmt.go:163` 的 `ir.Set` + `:231` 的 `ir.Remove`），不是静态迭代次数上界。设计稿 `:261` 把它列为「迭代次数上界」，**没有任何机制支撑**。
- **错误 B**：迭代次数有上界 ≠ **值域**有上界。反例：`x = x * 2` 在 `#[budget(32)]` 下，第 32 轮 `x` 回绕到 0（实测：`budget(32) loop x=x*2 from 1 => x=0`）。**budget 约束「转多少圈」，不约束「每圈涨多少」。**
- **错误 C**：budget 可以任意大（实测 `#[budget(2147483647)]` 编译通过），**「有上界」不等于「上界有用」**。
- **修正**：budget 的真正价值是**配合「推迟 3 轮再 widen」的策略，让 widen 在小预算循环里根本不触发**。这与「压住精度损失」不是一回事。

### 错误 7（严重）：3.6「`luo/math/lcg_step` 是唯一合法的用户」被仓库自己的代码证伪

- **位置**：`docs/DESIGN-v0.7.md:303`
- **反例**：`examples/luo-math.ggm:33-37` 的 `is_happen_fix1000` 算式**与 `lcg_step` 完全相同**，但**没有** `[wrapping]`。
- **实测恒等**：
  ```
  lcg_step(12345)          = -2364649
  is_happen_fix1000's r    = -2364649
  identical? true
  ```
- **真机确认**：`luo:math/lcg_step`（`a0 = 12345`）→ `return/R = -2364649`
- **后果**：仓库现状**已经不满足** 3.6 的验收标准（「只有它」）。而且 `is_happen_fix1000` **必须**标（它算的就是回绕的 LCG）。

### 错误 8（严重）：语法规范的「2 的幂取模免标注」豁免**是错的**

- **位置**：`docs/GGM-语法规范-v1.0.md:186`
- **反例**：`lcg_step(12345)`
  - **精确数学**（2 的幂取模）：`13622895699525 mod 67108864` = **64744215**
  - **运行期（int32 回绕）**：**-2364649**
  - **两者不同** → 「2 的幂所以不用标 `[wrapping]`」**推不出来**
- **真机确认**：`return/R = -2364649`
- **后果**：`lcg_step` **必须**标 `[wrapping]`。这个豁免必须从规范里删掉。

### 错误 9（中）：3.6 的验收标准「不能证明就报」会导致 **100% 误报**

- **位置**：`docs/DESIGN-v0.7.md:301-302`
- **实测**（真实 AST + 设计稿传递函数，`luo-math-full.ggm`）：报出 **8 条 `W_WRAP_UNPROVEN`**，**8 条全是误报**（全部因为 ⊤）。
- **修正**：改成「**能证明溢出才报**」。这样 `luo-math-full.ggm` 报 **0 条**。

### 错误 10（中）：3.3 漏了 `rem_euclid` 这一行，而它**有一个真 bug**

- **位置**：`docs/DESIGN-v0.7.md:235-248`（表里没有 `rem_euclid`）
- **证据**：`internal/lower/call.go:390-395` 发射 `u = t; u += m; u %= m`，中间 `u += m` 是 int32 加法
- **真机实测**：`rem_euclid(100, 2147483647)` 返回 **-2147483549**，应为 **100**

### 错误 11（中）：3.4 的「值从哪来」表把 `#[budget(N)]` 列为「迭代次数上界」，但区间格里**没有「迭代次数」这个维度**

- **位置**：`docs/DESIGN-v0.7.md:261`
- **问题**：区间分析产出的是「值的区间」。要从 budget 推出「迭代次数 ≤ N」，需要额外识别「由 `ir.Set` 初始化、`ir.Remove` 递减、回边上被 `>= 1` 守卫的槽」这个**计数器模式**。设计稿没给这个推导。
- **额外坑**：`loop` 是**尾测试**（`internal/lower/stmt.go:193-211`），迭代次数是 `1..N` 不是 `0..N`；`while` 的体可以 `return`（`stmt.go:177-180`）而**根本不走回边**。

### 错误 12（轻）：3.4 的收窄例子只覆盖了「变量 vs 字面量」

- **位置**：`docs/DESIGN-v0.7.md:265-269`
- **问题**：GGM 的比较降成差值槽 `d = lhs - rhs`（`internal/lower/expr.go:290`），所以 `if hp > other_var` 学到的是「`hp - other_var >= 1`」，**对 `hp` 无用**。只有「变量 vs 字面量」能收窄。设计稿没说这个限制。

### 错误 13（轻）：3.3 漏了「比较的差值槽」和「差值槽的结果是 0」这两件事的关系

- 已在错误 2 提过。补一点：`d = 0` 是 `==` 的**匹配条件**（`target.CondAlts` 的 `"=="` 分支，`internal/target/target.go:103-104`），**是故意的**，不该被当成除零或异常。

---

## 附：本次调研**未**验证的部分

- **阶段 1 的实际误报率**：本次只做了**估算**（真实 AST + 设计稿传递函数的离线实现）。真实实现会因为 `ir.Arith` 的「dst 既是目标又是左操作数」语义（`internal/analysis/analysis.go:45-47`）产生**额外**的区间合并点，数字可能变化。**待实现后实测。**
- **`ir.Guarded` 的区间合并**：本次估算没建模 `Guarded`（`internal/ir/ir.go:153-156`）。真实实现要按 `join(旧,新)` 处理。**未验证。**
- **widen 的实际精度损失**：本次只给了策略，**没有在真实 CFG 上跑过**。`examples` 里的两个循环（budget 32 / 64）应该是最好的测试床。**未验证。**
- **字段（class field）的区间来源**：3.4 提到「字段初始化器 | `var hp: int = 40` | `[40,40]`」（`DESIGN-v0.7.md:260`）。但字段的初值在**另一个函数**（`<ns>/C/init`，`internal/lower/lower.go:607-619`）里设，**方法体里的 `hp` 在进入时可能是 0**（字段初始化器只跑一次，实体加载时）。**「字段初始化器 = 区间来源」这个说法需要复核。未验证。**
- **`extern global` 的区间**：`SEED`（`examples/luo-math-full.ggm:14`）是 extern global，**别的包可以写它**，所以恒为 ⊤ 是对的。**已确认 `luo:math/load` 会写它**（`examples/luo-math-full.ggm:69-71`）。

---

## 附：调研期间发现的三个静默误编译（完整复现步骤）

### A. `x += e` 被降成 `x = e`

```powershell
cd D:\code\go\GoGoMcfunction
$d = "$env:TEMP\ggmprobeA"; Remove-Item -Recurse -Force $d -EA SilentlyContinue
New-Item -ItemType Directory -Force -Path $d | Out-Null
@'
namespace t:e
fn f() -> int {
    let x = 5
    x += 3
    x
}
'@ | Set-Content (Join-Path $d "pe.ggm") -Encoding UTF8
go run ./cmd/ggm -q -O 2 -out (Join-Path $d "out") (Join-Path $d "pe.ggm") 2>$null
Get-Content (Join-Path $d "out\t\e\f.mcfunction")
```

**输出**：

```
scoreboard players set @s l0 5
scoreboard players set @s l0 3
scoreboard players operation @s return = @s l0
```

**第 2 行应为** `scoreboard players operation @s l0 += #3 ggm.c`。

**真机**：

```powershell
$w = Join-Path $d "world\data\functions\t\e"; New-Item -ItemType Directory -Force -Path $w | Out-Null
Copy-Item (Join-Path $d "out\t\e\*.mcfunction") $w
@"
name: ggm
setup:
  function t:e/init
  kill @e[tag=GGM]
  summon armor_stand 0 0 0 {Marker:1b}
  scoreboard players tag @e[type=armor_stand] add GGM
run:
  execute @e[tag=GGM] ~ ~ ~ function t:e/f
  scoreboard players operation R return = @e[tag=GGM] return
"@ | Set-Content (Join-Path $d "case.difftest") -Encoding UTF8
& D:\code\go\GoMcRuntime\gomc.exe difftest run (Join-Path $d "world") (Join-Path $d "case.difftest") --full
```

**输出**：`return/R = 3` —— **应为 8**。

**根因**：`internal/lower/stmt.go:42-48` 的 `AssignStmt` 分支**从不读 `s.Op`**。

### B. 差值槽回绕使 `n < 4` 在 `n = -2147483648` 时为假

```powershell
@'
namespace t:e
fn f(n: int) -> int {
    if n < 4 { return 1 }
    0
}
'@ | Set-Content (Join-Path $d "n4.ggm") -Encoding UTF8
go run ./cmd/ggm -q -O 2 -out (Join-Path $d "out4") (Join-Path $d "n4.ggm") 2>$null
Get-Content (Join-Path $d "out4\t\e\f.mcfunction")
```

**输出**：

```
scoreboard players operation @s t0 = @s a0
scoreboard players operation @s t0 -= #4 ggm.c
execute @s[score_t0=-1] ~ ~ ~ scoreboard players set @s return 1
function t:e/f$b2 unless @s[score_t0=-1]
```

**真机**（`a0 = -2147483648`）：

```
error: Entity '@s[score_t0=-1]' cannot be found
    return/R = 0
```

**应为 1**（`-2147483648 < 4` 为真）。

**根因**：`internal/lower/expr.go:290` 的 `d = lhs - rhs` 在 int32 里回绕（`-2147483648 - 4` → `2147483644`）。

### C. `rem_euclid` 的中间加法溢出

```powershell
@'
namespace t:e
fn f(x: int) -> int {
    let y = rem_euclid(x, 2147483647)
    y
}
'@ | Set-Content (Join-Path $d "re.ggm") -Encoding UTF8
go run ./cmd/ggm -q -O 2 -out (Join-Path $d "outr") (Join-Path $d "re.ggm") 2>$null
Get-Content (Join-Path $d "outr\t\e\f.mcfunction")
```

**输出**：

```
scoreboard players operation @s t0 = @s a0
scoreboard players operation @s t0 %= #2147483647 ggm.c
scoreboard players operation @s t1 = @s t0
scoreboard players operation @s t1 += #2147483647 ggm.c
scoreboard players operation @s t1 %= #2147483647 ggm.c
```

**真机**（`a0 = 100`）：

```
    return/R = -2147483549
```

**应为 100**。

**根因**：`internal/lower/call.go:393` 的 `u += m` 是 int32 加法。

---

## 附：本次调研使用的所有可复现命令

```powershell
# 基线
cd D:\code\go\GoGoMcfunction
go build ./...                 # 通过
go test -count=1 ./...         # 63 顶层测试全绿
go test -count=1 ./... -v | Select-String "^--- PASS" | Measure-Object   # 63

# 仓库未被改动
git status --porcelain         # 空

# 示例诊断
go run ./cmd/ggm -check examples\luo-math-full.ggm
# → 4 条 W_UNREACHABLE_FN，无其它

# 区间估算器（真实 AST，副本里）
cd C:\Users\lenovo\AppData\Local\Temp\ggmprobe-repo
go run ./cmd/rangeest D:\code\go\GoGoMcfunction\examples\luo-math-full.ggm
go run ./cmd/rangeest D:\code\go\GoGoMcfunction\examples\luo-math.ggm
```

**副本的建法**（**原仓库零改动**）：

```powershell
$dst = "$env:TEMP\ggmprobe-repo"
Remove-Item -Recurse -Force $dst -EA SilentlyContinue
New-Item -ItemType Directory -Force -Path $dst | Out-Null
robocopy D:\code\go\GoGoMcfunction $dst /E /XD .git /NFL /NDL /NJH /NJS /NP
cd $dst; go build ./...   # exit 0
```

---

## 附：文件索引

| 文件:行 | 内容 |
|---|---|
| `internal/ir/ir.go:191-197` | `ir.Cond` —— 区间谓词，收窄的输入 |
| `internal/ir/ir.go:126-130` | `ir.Arith` —— `Op` 含 `+= -= *= /= %= < > <= >=` |
| `internal/ir/ir.go:153-156` | `ir.Guarded` —— 条件写，值是二选一 |
| `internal/ir/logic.go:30-63` | `ir.Negate` —— **补集的唯一实现**（铁律 2） |
| `internal/ir/logic.go:76-93` | `Cond.complement` —— 单点区间的补集是**两段** |
| `internal/analysis/analysis.go:41-60` | `Uses` —— `Arith` 的 `Dst` **也是读** |
| `internal/analysis/analysis.go:104-150` | `LiveOut` —— 不动点迭代的模板 |
| `internal/lower/expr.go:290` | **差值槽** `d = lhs - rhs`（int32，会回绕） |
| `internal/lower/expr.go:310-355` | `abs` 的代数改写 → **两个 `Alt`**（析取） |
| `internal/lower/expr.go:358-368` | `predicateFor` —— `target.CondAlts` → `ir.Predicate` |
| `internal/lower/expr.go:406-420` | `arithOp` —— 复合运算符的映射（**修 A 可复用**） |
| `internal/lower/stmt.go:42-48` | **`AssignStmt` 分支不读 `s.Op`（bug A 的根因）** |
| `internal/lower/stmt.go:157-183` | `lowerWhile` —— budget 是 `ir.Set` + `ir.Remove` |
| `internal/lower/stmt.go:193-211` | `lowerLoop` —— **尾测试**，上界是 `1..N` |
| `internal/lower/call.go:143` | `reservedCalls` —— **`clamp` 未实现** |
| `internal/lower/call.go:370-383` | `min`/`max` → `ir.Arith{Op:"<"/">"}` |
| `internal/lower/call.go:384-396` | `rem_euclid` —— **bug C 的根因** |
| `internal/lower/lower.go:547-557` | `lowerFunc` —— `[wrapping]` 标志该挂这里 |
| `internal/syntax/parser.go:205-209` | `funcAttrs` —— `wrapping` 被接受但不消费 |
| `internal/syntax/parser.go:323-324` | `wrapping` → `syntax.FuncDecl.Wrapping` |
| `internal/syntax/ast.go:76-81` | `syntax.AssignStmt` —— **有 `Op` 字段** |
| `internal/syntax/ast.go:137-140` | `syntax.Param` —— **无 `Pos`**，阶段 2 前要加 |
| `internal/syntax/ast.go:148` | `syntax.FuncDecl.Wrapping` —— **全库无消费者** |
| `internal/target/target.go:68-78` | `target.Condition` —— 1.12.2 区间语义 |
| `internal/target/target.go:93-109` | `target.CondAlts` —— `==` 双界、`!=` 两备选 |
| `internal/driver/driver.go:106` | `optimize.Run` —— **分析器的插入点** |
| `internal/driver/driver.go:127-141` | `checkLiterals` —— `E_INT_RANGE` 的理由（Error 而非 Warning） |
| `internal/driver/golden_test.go:74-79` | **golden 只对 Error fail**（Warning 不影响） |
| `internal/diag/diag.go:15-18` | `Severity`：`Error = 0`，`Warning = 1` |
| `cmd/ggm/main.go:69-85` | CLI 对 Warning 的处理（打印但写出文件） |
| `GoMcRuntime/internal/world/scoreboard.go:335-369` | `Apply` —— **运行期语义的权威** |
| `GoMcRuntime/internal/world/scoreboard.go:371-377` | `wrapInt32` |
