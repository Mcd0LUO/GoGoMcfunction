# Roadmap —— 被砍掉的语法

规则：**每一条都要等到出现第 3 个真实用例再实现。** 不是「有用就做」，而是「不用就活不下去才做」。

| 特性 | 为什么砍 | 复活条件 | 替代 |
|---|---|---|---|
| `task` / `wait` / `enter` / `spawn` | `class + @tick` 已经覆盖跨 tick，且更贴近知识库「状态挂在实体上」 | 出现「一段线性时序代码」写三遍以上 | `class CastTimer { @tick fn tick() }` |
| `pure` 关键字 | 纯度应靠推断，不该让用户写 | 需要 `comptime` 调用用户函数 | 无命令即纯，自动判定 |
| `enum` / `match` | `luo/math` 零使用 | 状态数 > 8 且要穷尽检查 | `if/else` |
| `fixed<N>` / `within` | `luo/math` 全是 `int` | 出现小数算术错误导致的线上 bug | 千分比 `int` |
| `ref<T>` / `list<T,N>` | 跨实体引用 = O(N) 广播，本来就该显式 | 有明确的一对一引用需求 | 消息队列 / 显式广播写 |
| 泛型 + trait 约束 | `max<int>` 在 stdlib 写死就够 | stdlib 出现第三个同构函数 | 内置 `min/max/abs/clamp` |
| `pool_*` 实体池 | 用 `for e in entities<>` 手写 3 行 | `ini_sum` 这种模式出现 3 次 | 手写 |
| `#[inline]` | 无真实用例 | 递归需要编译期展开 | 无 |
| `#[nbt_bits]` 位编码 | 只在 `on spawn` 用一次 | 需要把多值状态压进可运算整数 | 手写 `scoreboard players add ... {NBT}` |
| `@broadcast` 方法 | 广播写语句已覆盖 | 需要跨类广播 | 广播写语句 |
| `goto` / `break` / `continue` | 自递归够用 | 出现无法用自递归表达的跳出 | 标志位 |
| `every ... phase N` 手动相位 | 编译器自动分配 | 需要手工错开重任务 | 自动 |
| 规则引擎 `rule when then` | 分阶段求值成本高 | 规则数 > 30 | `on tick` + `if` |
| 声明式 `derived` / 脏标记 | 调试成本大于收益 | 出现「忘了更新缓存」的线上 bug | 手写脏标记 |
| LSP / Playground | 20 个用户 | 用户数 > 50 | 无 |

## 现在有什么

v1.0 保留的全部 7 种声明、6 种语句，见 `CMCF-语法规范-v1.0.md`。
