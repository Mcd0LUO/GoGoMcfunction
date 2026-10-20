# GoMcRuntime 缺陷清单

排查 GGM 编译产物时发现的。**2026-10-19 复测：全部已修复并回填结论。**
原始行号基于修复前的工作副本。

---

## 1. `summon` 的 NBT `Tags` 不进 `Entity.Tags` 【已修复】

**原位置**：`internal/version/v1_12_2/cmd_entity.go:76-78`

```
summon armor_stand 0 0 0 {Tags:["w"]}
execute @e[tag=w] ~ ~ ~ function t:x/h
  → error: no matching entities
```

**复测结论**：修复采纳了**单一数据源**方案——`Entity.Tags` 字段被删除，
`TagList()` / `HasTag()` 直接读 `e.NBT`（`internal/world/entity.go:110,125`）。
`summon` / `entitydata` / `scoreboard players tag` 三条写路径自动一致，
不存在需要维护的双向同步。

```
step 1: summon armor_stand 0 0 0 {Tags:["w"],Marker:1b}
step 3: execute @e[tag=w] ~ ~ ~ say found-w
  feedback: [Armor Stand] found-w
```

---

## 2. `entitydata` 写 `Tags` 不同步 【已修复，与 1 同源】

`entitydata @e[type=zombie] {Tags:["renamed"]}` 之后：

```
step 5: execute @e[tag=renamed] ~ ~ ~ say found-renamed
  feedback: [Zombie] found-renamed
step 6: execute @e[tag=z] ~ ~ ~ say stale-z
  error: Selector '@e[tag=z]' found nothing
```

新标签可见、旧标签消失，符合 1.12.2 的列表替换语义。

---

## 3. `check` 在函数加载失败时退出码为 0 【已修复】

```
$ gomc check world-bad
error[Unknown]: .../t/bad.mcfunction:2: Unknown or invalid command 'this'
1 error(s)
exit=1
```

现在可以直接当 CI 门禁用。

---

## 4. `@s` 无执行者的错误信息不指向根因 【已修复】

```
$ gomc run world-s t:s
[Server] after
ran 2 command(s) from t:s
error[Runtime]: @s has no executor in this context: the command was not run through /execute
exit=1
```

顺带确认了失败**不中止函数**（`[Server] after` 照常输出），与 1.12.2 一致。
另外这里区分了「命令失败」与「加载失败」两类退出码，比原来只看加载更完整。

---

## 5. `difftest --full` 的 `scores:` 顺序 【已确认稳定】

同一 objective 多 holder，连续 5 次运行输出完全一致，且按键排序：

```
obj/9num = 5
obj/Beta = 4
obj/alpha = 2
obj/mid = 3
obj/zeta = 7
```

解析依赖成立，无需再改。**保留为契约**：以后若改成 map 迭代顺序会破坏下游。

---

## 6. 已确认**不是**缺陷的两点

| 怀疑 | 实测结论 |
|---|---|
| 命令失败会中止函数 | **不会**。`unknown objective` / `@s` 无执行者之后函数继续跑完，与 1.12.2 一致 |
| `execute` 多实体失败传播 | 不需要特殊处理；单个实体选择器落空只报错不中断 |

---

## 附：对接中发现的一个**语义噪音**（非缺陷，暂不处理）

GGM 用选择器做分支，例如：

```
execute @s[score_t0=-1] ~ ~ ~ function t:x/sqrt$b4
execute @s[score_t0_min=0] ~ ~ ~ function t:x/sqrt$b5
```

两支必有一真。**假的那支会打印 `found nothing` 错误**。
这是 1.12.2 选择器分支的固有代价，手写库同样如此，无法用其它写法消除——
1.12.2 既没有 `if`，也没有能静默失败的 execute 变体。

影响：日志噪音、以及每个分支多一条必然报错的命令。
功能正确性不受影响，因此 GGM 的测试**不对 in-function error 断言**，
只断言最终结果。

---

*本清单由 GGM 项目在对接过程中产生。2026-10-19 全部复测通过。*
