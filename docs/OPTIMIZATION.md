# 优化级别与循环形态

> 本文回答两个问题：**-O 级别间的差别到底是什么**，以及**为什么语言里要有两种循环**。
> 这是两件相关的事：级别决定编译器能证明什么，语法决定编译器不必证明什么。

---

## 1. -O 级别的语义

借鉴 C++，但有一个关键差别：

```
C++ 的 -O 级别 = 打开了哪些优化
CMCF 的 -O 级别 = 每个优化被允许依赖多强的证明
```

之所以这样定，是因为在 1.12.2 上「优化错误」的代价是**静默的**：命令语法合法、
函数能加载、游戏不报错，只是行为不对。所以一个优化只有在其依赖的分析确实可用时才
被允许启用，而不是作为独立开关自由组合。

| 级别 | 允许的证明强度 | 启用 |
|---|---|---|
| `-O0` | 无需证明（只保证正确性） | 常量池 |
| `-O1` | 结构性、无条件成立 | + CFG 化简、不可达消除、自赋值消除 |
| `-O2`（默认） | 全函数不动点（活跃性） | + 死存储消除、寄存器复用、常量收集 |
| `-O3` | 跨块值分析 | 保留，尚无通过此门槛的优化 |

**级别是单调的**：O1 启用的优化 O2 一定启用。不存在「O3 关掉 O2 的某个优化」。

### 为什么常量池在 O0

这一条值得单独说，因为它是从 bug 里学到的。

字面量 `4` 降级成 `#4 cmcf.c`，它是一个 fake player。如果没人初始化它，
计分板读出来是 **0**。最初我把「生成常量池」归到 O2，结果 `-O1` 的产物里
`x -= #4` 实际减的是 0，程序全错，而且没有任何报错。

**生成常量池是正确性要求，不是优化。** 它现在在 O0 也运行。

### 查看某个级别跳过了什么

```
$ cmcf -O1 -v foo.cmcf
skipped at -O1: dead-store (needs O2: whole-function liveness fixed point; deleting a live write changes behaviour)
skipped at -O1: coalesce-temps (needs O2: interference graph built from liveness; ...)
```

---

## 2. 两种循环

```
while cond #[budget(N)] { body }    // 前测：先判据，可能一次都不执行
loop  cond #[budget(N)] { body }    // 后测：先执行一次，再看是否继续
```

### 为什么需要 `loop`

`while` 降级成「循环头判据 + 循环体」，两个基本块，**两个文件**：

```
H: cond; Branch(cond, B, X)
B: body; Jump(H)
```

手写库写的是后测形式，**一个文件**：

```
B: body; cond; recurse-if(cond)
```

编译器可以尝试自动把 `while` 旋转成后测，但**这在一般情况下是错的**：

```
while abs(x - prev) >= 1 { prev = x; x = (x + n/x)/2 }
```

循环头的判据读 `x` 和 `prev`，而循环体**会改写它们**。把判据移到尾部，
它读的就是更新后的值——语义完全变了。手写版能这么写，是因为作者**手工**把尾部判据
写成了最终值的表达式。编译器要推出这一点需要值编号分析，而当前没有。

于是我**撤回了循环旋转优化**，改为在语言里提供 `loop`：
把「分析难题」变成「语法选择」，用户显式表达意图，编译器不需要猜。

```
// 一个文件，一个自调用
loop abs(x - prev) >= 1 #[budget(32)] {
    prev = x
    x = (x + n / x) / 2
}
```

生成：

```mcfunction
scoreboard players operation @s l1 = @s l0
scoreboard players operation @s t0 = @s a0
...
scoreboard players operation @s t2 = @s l0
scoreboard players operation @s t2 -= @s l1
scoreboard players remove @s l2 1
execute @s[score_t2=-1] ~ ~ ~ function luo:math/sqrt$b2
execute @s[score_t2_min=1] ~ ~ ~ function luo:math/sqrt$b2
execute @s[score_l2_min=1] ~ ~ ~ function luo:math/sqrt$b2
execute @s[score_t2_min=0,score_t2=0,score_l2=0] ~ ~ ~ function luo:math/sqrt$b3
```

三条继续守卫 + 一条退出守卫。退出是 `NOT(d<=-1 ∨ d>=1 ∨ budget>=1) = d==0 ∧ budget<=0`，
一个合取式，正好一条 `execute`。

---

## 3. 实测效果

```
-O0: files=18  commands=101  （无优化，调试用）
-O1: files=17  commands=99
-O2: files=17  commands=99
-O3: files=17  commands=99
```

命令行数几乎不变，但**寄存器压力差别很大**：

| 级别 | objective 总数 | 临时槽 |
|---|---|---|
| -O1 | 28 | 19（t0..t19） |
| -O2 | 15 | 6（t0,t1,t2,t12,t13,t19） |

1.12.2 的每个 objective 都要占存档空间、参与选择器匹配，所以 objective 减半是实打实的收益，
只是它不出现在行数上。

---

## 4. 一个不做的优化：循环旋转

代码里 `rotateLoops` 和 `rotatePostTest` 两个函数保留着，但**不在任何
管线里**，并有注释说明为什么。它们会：

- 把循环头的判据移到尾部（读错值）
- 把 2-alternative 的 `|d|>=1` 和预算守卫叉积成 4 条重叠守卫
- 让回边指向错误的块

保留它们是为了将来实现值编号后可以启用。**在任何时候启用都会产生能加载、
不报错、但行为错误的程序**——这正是本编译器最该避免的失败模式。

---

## 5. 加新优化时该做什么

1. 在 `analysis/` 写它需要的分析，单独测试
2. 在 `optimize/` 写 pass，测试**断言 IR 结构**而不是输出字符串
3. 在 `Catalog()` 注册，写清 `Requires`——即**它为什么是正确的**
4. 用真实代码验证：至少跑一遍 `examples/luo-math-full.cmcf`

第 3 步不是形式主义。本文第 1 节的常量池 bug 就发生在「没想清楚这个 pass 依赖什么」上。

---

*本文与 `internal/optimize/level.go` 同步。改管线必须先改本文。*
