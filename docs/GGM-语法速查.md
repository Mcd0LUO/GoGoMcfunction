# GGM 语法速查

> 每条都编译验证过：本文的代码块整体放进 `ggm -check`，退出码 0。
> 「陷阱」里的是我照着直觉写、然后被编译器纠正的地方。

---

## 1. 词法

```ggm
// 行注释

命名空间    IDENT ":" IDENT            // demo:combat，必须有冒号
标识符      [A-Za-z_][A-Za-z0-9_]*
整数        [0-9]+                     // 没有小数
字符串      "..."  '...'
NBT 值      n"{...}"                   // SNBT，编译期解析
NBT 路径    p"tag.atk_base"            // 从物品根起算的键路径
```

**陷阱**：`namespace` 后面必须有冒号。写 `namespace demo` 报 `E_NO_NAMESPACE`，
因为 1.12.2 的函数 id 永远是域限定的。

**陷阱**：objective 名可以带点和冒号（`Ap.Amount`、`Count.残照花魄`、
`EasyCoord:x`），所以带点的名字有两种读法 —— 见 §5。

---

## 2. 顶层声明

```ggm
namespace demo:combat         // 每个文件一个，同模块共享

const CAP = 100               // 编译期常量，进 ggm.c 常量池

obj Hp                        // 声明 objective（模块级）
obj Combat.伤害               // 名字可以带点
obj Score stat.kills "击杀"   // 带 criteria 和显示名

extern ATK: int = @s.atk      // 绑定既有分数：执行者身上
extern SEED: int = seed.val   // 绑定假玩家 seed 的 val

nbt level = p"tag.level" : bits 8                        // 位折叠
nbt kind  = p"tag.kind"  : enum 1..4                     // 恒等枚举
nbt 残响  = p"tag.cast"  : enum { "残响": 1, "回响": 2 }  // 映射枚举

comptime {
    for n in 0..8 { score("#mul" + str(n), "table", n * n) }
    cmd("scoreboard objectives add table dummy")
}

fn clamp(v: int, lo: int, hi: int) -> int { ... }
class Boss { ... }
```

### 属性（写在声明前）

```ggm
[export]                 fn hit() { }        // 对外可见
[load]                   fn on_load() { }    // 世界加载时跑一次
[selector(@e[tag=boss])] class Boss { }      // 类的成员判定谓词
[selector(@e[tag=boss])] fn boss_tick() { }  // 函数也可加
[every("1s")]            fn tick() { }       // 周期，必须字符串
[spawn]                  fn spawned() { }    // 类方法：summon 后一次
[wrapping]               fn add(a: int) { }  // 按 int32 回绕
```

**陷阱**：`[every(20)]` 报 `E_PARSE`。周期必须是字符串字面量，
因为周期长度要在程序跑之前就知道。`[every("1s")]` 和 `[every("100ticks")]`
是同一个周期（按 tick 计数），都归一成 `p.100`。

---

## 3. 语句（8 种）

```ggm
let x = 1                  // 声明并初始化
x = 2                      // 赋值
x += 1                     // 复合：+= -= *= /= %=
return x                   // 早返回
f(1, 2)                    // 调用（语句位置）

if a > 0 { } else if b > 0 { } else { }

for i in 0..10 { }                            // 计数循环
for e in entities<@e[tag=monster]> { }        // 扇出
for e in entities<@e[tag=monster]> where e.Hp > 10 { }

while Mana < 20, budget 20 { }     // 前测，必须给 budget
loop  Mana > 100, budget 10 { }    // 后测，必须给 budget

@a[tag=player] Hp = 100            // 广播写：选择器在左

decode atk from @s.Inventory(0)[atkbits]
[add] decode atk from @s.Inventory(0)[atkbits]  // 累加，不重置
decode kind from @s.Inventory(0)[kind] {
    n"tag.kind:1b" set 1
    n"tag.kind:2b" set 2
}
encode @s.Inventory(0)[atkbits] = Hits over 0..11
```

**陷阱**：广播写只支持 `=`。`@a[tag=p] Mana += 1` 报 `E_PARSE`。

**陷阱**：`while` / `loop` 的 `budget` 是强制的。1.12.2 没有循环，
所有循环都是自我递归调用，没有界就是无限递归。

**陷阱**：`decode` 的块里**没有 `init` 子句**（规范 §4.4 写错了）。
默认重置为 0，加 `[add]` 属性才是累加。

**陷阱**：`decode` 的目标是 **objective**，方括号里是 **nbt 字段名**，两者分别声明：

```ggm
obj atk                                    // 目标 objective
nbt atkbits = p"tag.atk_base" : bits 11    // 编码方式
decode atk from @s.Inventory(0)[atkbits]
```

---

## 4. 表达式

```ggm
1 + 2  3 - 1  4 * 5  9 / 2  9 % 2      // 整数算术
-1                                      // 一元负号
a < b   a <= b   a > b   a >= b   a == b   a != b

a && b   a || b   !a                    // 逻辑（只在条件里）
(a || b) && c                           // 括号可用

Hp                                      // objective / 局部变量 / 常量
self.Combat.伤害                        // 执行者身上的 objective
@p[tag=mob]."Combat.伤害"               // 别的实体的具名分数（要引号）
seed.val                                // 假玩家的分数（holder.objective）
f(1, 2)                                 // 调用
min(a, b)   max(a, b)   rem_euclid(a, b)
reduce<max>(@a[tag=boss], Hp)           // 折叠一组实体
reduce<min>(@a[tag=boss], Hp)
```

**陷阱**：`&&` `||` `!` 只在**条件位置**可用，不能当值：

```ggm
let ok = a > 0 && b > 0        // ✗ E_EXPR: && is not an arithmetic operator

let ok = 0                     // ✓ 写成条件
if a > 0 && b > 0 { ok = 1 }
```

原因：1.12.2 的选择器是合取，没有布尔 OR。条件能编译成一个选择器，值不能 ——
值需要先物化成 0/1。

**陷阱**：`reduce<max>(...)` 只能在表达式位置。裸写成语句报 `E_STMT`。

**陷阱**：带点的 objective 名有两种读法，**声明优先**：

```ggm
obj Ap.Amount
X = Ap.Amount        // 读 objective "Ap.Amount"
X = nosuch.thing     // 没声明过 → 假玩家 nosuch 的 thing，并给警告
```

**陷阱**：`@sel."objective"` 的 objective 必须加引号，因为 `@p.Ap.Amount`
无法拆开。`self.` 后面不需要引号 —— `self` 本身不可能是 objective 名，
点号没有第二种读法。

**陷阱**：比较的结果是 0/1，可以直接当值用：

```ggm
fn is_raging() -> int {
    let r = Hp < 5      // r 是 0 或 1
    r
}
```

---

## 5. 类

```ggm
[selector(@e[type=zombie,tag=mob.monster])]
class Monster {
    var hp: int = 40          // 字段必须有常量初值
    var timer: int = 0

    [spawn]
    fn spawned() { hp = 40 }

    [every("1tick")]
    fn tick() {
        timer += 1
        if timer >= 20 { fire(); kill(self) }
    }

    fn deal(dmg: int) { hp -= dmg }
}
```

- 字段 = 执行者身上的 objective，名字是 `<类名>.<字段名>`，截断到 **16 字符**
- `[selector]` 是成员的判定谓词 —— 类和选择器是同一件事的两面
- 类方法是普通函数，id 是 `<ns>/<类名>/<方法名>`

**陷阱**：从类外调用方法要用**完整路径**：

```ggm
demo:combat/Monster/deal(3)     // ✓
Monster/deal(3)                 // ✗ 裸的 / 是除法，不是路径分隔符
deal(3)                         // ✗ 类外没有这个函数
```

类内调用同级方法用裸名（`fire()`）。

**陷阱**：类字段是 `var`，不是 `nbt`。`nbt` 是文件级的 NBT 字段声明，
给 `decode`/`encode` 用的，两者是不同的东西。

---

## 6. comptime

```ggm
comptime {
    for n in 0..8 { score("#mul" + str(n), "table", n * n) }
    for n in [1, 2, 4, 8] { score("pow" + str(n), "tbl", n) }
    cmd("scoreboard objectives add tbl dummy")
}
```

- `for n in a..b` 或 `for n in [列表]`，**都必须是编译期常量**
- `score(持有者, objective, 值)` 生成一条 `scoreboard players set`
- `cmd("...")` 原样输出一条命令，不做检查
- `str(n)` 整数转字符串，**仅编译期**

**陷阱**：`str()` 在运行时报 `E_STR_RUNTIME`。它只是拼名字用的。

---

## 7. 完整可编译样例

下面这段覆盖上面所有构造，`ggm -check` 退出码 0：

```ggm
namespace demo:all

const CAP = 100
obj Hp
obj Mana
obj Combat.伤害
obj atk
extern ATK: int = @s.atk
extern G: int = globals.counter
nbt atkbits = p"tag.atk_base" : bits 11

comptime {
    for n in 0..4 { score("#m" + str(n), "tbl", n * n) }
    cmd("scoreboard objectives add tbl dummy")
}

fn clamp(v: int, lo: int, hi: int) -> int {
    if v < lo { return lo }
    if v > hi { return hi }
    v
}

fn damage(amount: int) {
    Hp = clamp(Hp - amount, 0, CAP)
    G += 1
}

[selector(@e[tag=boss])]
class Boss {
    var phase: int = 1
    fn awaken() { phase = 2 }
}

[export]
fn run() {
    demo:all/Boss/awaken()
    damage(ATK)
    for e in entities<@e[tag=monster]> where e.Combat.伤害 > 10 {
        tag_add(self, "doomed")
    }
    while Mana < 20, budget 20 { Mana = Mana + 1 }
    loop  Mana > 100, budget 10 { Mana = Mana - 10 }
    @a[tag=player] Hp = 100
    let m = reduce<max>(@a[tag=boss], Hp)
    let ok = 0
    if (Hp > 0 && Mana > 0) || !(m == 0) { ok = 1 }
    decode atk from @s.Inventory(0)[atkbits]
    encode @s.Inventory(0)[atkbits] = Hp over 0..11
    Hp = ok + m
}
```

---

*本文件由 `ggm -check` 验证。改语法时请同步这里，并重跑最后一节。*
