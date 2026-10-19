# GoMcRuntime 缺陷清单

排查 CMCF 编译产物时发现的，按影响排序。**行号基于 2026-10-19 的工作副本。**

---

## 1. `summon` 的 NBT `Tags` 不进 `Entity.Tags` 【严重】

**位置**：`internal/version/v1_12_2/cmd_entity.go:76-78`

```go
if data := exec.NBTOf(args, 2); data != nil {
    mergeNBT(e.NBT, data)   // 只写 NBT，没同步 e.Tags
}
```

**复现**：

```
summon armor_stand 0 0 0 {Tags:["w"]}
execute @e[tag=w] ~ ~ ~ function t:x/h
  → error: no matching entities
```

**真实 1.12.2**：NBT 的 `Tags` 与计分板标签是**同一集合**。summon 后 `tag=` 立即可见。

**验证**：`scoreboard players tag @e[type=armor_stand] add w` 之后 `@e[tag=w]` 就能匹配，
说明选择器读的是 `e.Tags`，而 summon 写的是 `e.NBT`。

---

## 2. `entitydata` 写 `Tags` 不同步 【严重，与 1 同源】

**位置**：`cmd_entity.go:160-161`

```go
data := exec.NBTOf(args, 1)
mergeNBT(e.NBT, data)
```

写 `entitydata @e {Tags:["x"]}` 后 `@e[tag=x]` 仍匹配不到。

**建议**：不要双向同步（容易漏），改成**单一数据源**——把 `Tags` 做成
`e.NBT` 的虚拟视图：

```go
func (e *Entity) SetTag(tag string, present bool) {
    // 读写 e.NBT 的 Tags 列表，e.Tags 变成缓存或彻底删除
}
```

这样 `summon` / `entitydata` / `scoreboard players tag` 三条路径自动一致。

---

## 3. `check` 在函数加载失败时退出码为 0

**位置**：`internal/cli/cli.go` `runCheck`

实测：一个含非法命令的函数导致加载失败，`check` 仍打印 `ok: N function(s)
loaded` 并以 0 退出。作为 CI 门禁时这会漏掉问题。

**建议**：有任何 `LoadFunctions` 错误时返回 1。

---

## 4. `@s` 无执行者的错误信息不指向根因

控制台直接 `function t:x/foo` 时若函数体用 `@s`：

```
error: no matching entities
```

这是正确的 1.12.2 行为，但信息量太低。**建议**：

```
error: @s has no executor in this context (function invoked without execute)
```

---

## 5. 已确认**不是**缺陷的两点（记录以免重复排查）

| 怀疑 | 实测结论 |
|---|---|
| 命令失败会中止函数 | **不会**。`unknown objective` 之后 `Ran 3 command(s)` 且后续写入生效，与 1.12.2 一致 |
| `execute` 多实体失败传播 | 未测（需要两个可区分实体） |

---

## 6. 一个设计建议（非缺陷）

`difftest --full` 的 `scores:` 输出 `objective/holder = value`。
如果同一 objective 有多个 holder，**顺序是否稳定**未确认。
我的测试依赖这个解析；若顺序不稳定，建议排序后输出。

---

*本清单由 CMCF 项目在对接过程中产生。修复后请回填结论。*