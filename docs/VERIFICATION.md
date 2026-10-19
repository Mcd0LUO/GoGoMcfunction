# 验证：为什么要有一个解释器

> 本文记录一件事：**这个编译器到目前为止发现的每一个 bug，产物都是语法合法、能被游戏加载、不报任何错的。**

---

## 1. 问题

黄金测试比对文本。文本对了不代表行为对了。真实的失败长这样：

```mcfunction
scoreboard players operation @s t0 = @s a0
scoreboard players operation @s t0 -= #4 cmcf.c
execute @s[score_t0=-1] ~ ~ ~ scoreboard players set @s return 1
scoreboard players operation @s return = @s l0
```

每一条都合法、都能加载、都不报错。但第二条分支执行完后**没有退出**，最后一行把 `1` 覆盖了。
纯文本测试永远抓不到这个。

## 2. 方案

`internal/vm` 在 Go 里直接执行生成的 `.mcfunction`，实现 1.12.2 的一个子集：

| 命令 | 支持 |
|---|---|
| `scoreboard players set/add/remove/reset/operation` | 全部操作符，含 `< > <= >=` 极值 |
| `execute <sel> ~ ~ ~ <cmd>` | 选择器条件求值 |
| `function <name> [if/unless <sel>]` | 含存在性测试 |
| `tag` / `kill` / `say` | 部分 |

**选择器的区间语义是实现的核心**：`score_x=N` 是 `x <= N`，`score_x_min=N` 是 `x >= N`。
这与真实游戏一致，所以编译器对这套语义的任何误解都会在测试里暴露。

## 3. 资源上限是硬性要求

第一版没有上限，结果是 **OOM 崩溃**：测试卡住五分钟，最后抛出几 MB 的
goroutine 转储，真正的错误信息被淹没了。

现在每个资源都有上限，且默认值故意设小：

```go
var DefaultLimits = Limits{
	Steps:         2_000_000,
	Depth:         2_048,
	Scores:        200_000,
	TagsPerEntity: 1_024,
}
```

超限报的是结构化的 `*vm.Err`，不是包裹过的错误链——
**调用链包裹在深递归下会指数级膨胀**，同样会把真错误淹没。

同一个 bug 现在的表现：

```
sqrt(5): vm: recursion depth limit (2048) exceeded in t:x/sqrt$b2
0.01s
```

## 4. 步数是「每次调用」的

最初步数在 VM 上累计，导致「调用 20000 次正确函数」被误判为死循环。
现在 `Reset()` 重置单次计数，`TotalSteps()` 保留累计值。

这个区分很重要：**测试里循环调用和程序里死循环是两回事**。

## 5. 抓到的 bug

解释器上线后抓到的，全部是文本测试无法发现的：

| bug | 症状 | 根因 |
|---|---|---|
| 回边指向入口块 | 循环永不终止 | `mergeLinear` 让入口块吸收了循环体，回边重跑初始化，计数器和预算每轮被重置 |
| 预算作为析取 | 循环跑满预算才停 | `loop` 的预算被追加成 alternative 而非 conjunct，条件变成 `cond OR budget>0` |
| 预算折进条件 | 退出守卫从 1 条变 4 条 | 补集要做叉积；预算单列后恢复 1 条 |
| 常量池无调用者 | 所有字面量读作 0 | `consts` 函数没有接线。1.12.2 没有 load 钩子，必须显式调用 |
| 块 0 引用 `$b0` | 函数不存在 | `renderTerm` 绕过了 id==0 的特殊命名 |

## 6. 当前覆盖

```
TestSqrtMatchesMath     sqrt(0..20000) 对照数学真值
TestSqrtAllLevels       同上，在 O0/O1/O2/O3 各跑一遍
TestLoopTerminates      尾巴判据循环按条件退出
TestWhilePreTest        前测循环条件为假时体不执行
TestBranching           早退 if 链只走一个分支
TestBudgetStopsLoop     预算真的限制迭代次数
```

## 7. 一个诚实的发现：手写库的 sqrt 是错的

验证过程中用数学真值对照，发现原库 `luo:math/sqrt` 有两个问题：

1. **`n < 4` 除零**：`n/4 == 0`，迭代里 `n/x` 失败
2. **收敛判据 `|x-prev| <= 1` 是错的**：对 `n=8` 返回 3（真值 2），
   且对多数输入**在两个值之间永久振荡**——因为整数除法让迭代映射非单调

正确的整数牛顿法是 `while y < x` 形式：

```
x = n;  y = (x+1)/2
while y < x { x = y; y = (x + n/x)/2 }
return x
```

`0..20000` 零误差，最多 10 次迭代。

**这不是编译器的问题，是算法的问题。** 但它说明了一件事：
如果没有真值对照，这个缺陷会一直躺在库里，因为**它不报错**。

---

*本文与 `internal/vm` 同步。加新命令支持时，先加解释器，再加后端。*
