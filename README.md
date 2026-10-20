# CMCF

把 CMCF 编译成 Minecraft Java **1.12.2** 的 `.mcfunction`。

**cmcf 0.5.0** · 语言规范 1.0 · 目标 1.12.2

```
cmcf [flags] <file.cmcf> ...

  -out DIR     输出目录（默认 data/functions）
  -O N         优化级别 0..3（默认 2）
  -v           打印该级别跳过的优化及原因
  -check       只检查，不产出
  -q           安静模式
  -version     打印版本并退出
  -comments    生成文件带声明位置注释（默认关闭）
```

## 安装

```
go install github.com/Mcd0LUO/CelesteaMcfunction/cmd/cmcf@latest
```

或者从源码构建：

```
go build -o cmcf.exe ./cmd/cmcf
```

## 状态

这是第一个 baseline：生成结果已冻结并逐字比对，
见 [docs/BASELINE.md](docs/BASELINE.md) 与 [CHANGELOG.md](CHANGELOG.md)。

**编译器版本和语言规范版本不是同一个。** 编译器实现的是规范的一个子集——
规范第 7 节的检查清单里有一整张「计划中」的表。两者都印在 `cmcf -version` 里，
是唯一不需要加脚注就能说清这件事的方式。

语言特性见 [docs/CMCF-语法规范-v1.0.md](docs/CMCF-语法规范-v1.0.md)，
被砍掉的东西和复活条件见 [docs/roadmap.md](docs/roadmap.md)。

## 为什么值得存在

1.12.2 的选择器条件是**区间**不是相等：`score_x=N` 意为 `x <= N`。
写错不会报错，只会静默多匹配。编译器把这条规则封在 `internal/target` 里，
全项目只有一处实现。

其它被编译器接管的坑：

- 每个周期任务必须自己归零，漏了就永久失效
- `maxCommandChainLength` 超限后**静默丢弃**后续命令
- objective 必须先声明，否则写入静默失败
- `@s` 只在有执行者的上下文里有效

## 架构

```
cmd/cmcf -> driver -> syntax -> lower -> optimize -> backend
                        ir / analysis / target / mc / diag
```

依赖方向严格单向，见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。两条硬规则：

- `lower` 不写命令文本，只产出 IR
- `backend` 不认识语言，只认 IR

## 文档

| 文档 | 内容 |
|---|---|
| [ARCHITECTURE](docs/ARCHITECTURE.md) | 分层、IR 形态、加特性该改哪里 |
| [OPTIMIZATION](docs/OPTIMIZATION.md) | -O 级别的语义、两种循环、为什么不做循环旋转 |
| [VERIFICATION](docs/VERIFICATION.md) | 为什么要真跑、抓到的静默错误 |
| [BASELINE](docs/BASELINE.md) | 0.1.0 冻结了什么、怎么验证、不保证什么 |
| [CHANGELOG](CHANGELOG.md) | 每个版本改了什么 |
| [语法规范 v1.0](docs/CMCF-语法规范-v1.0.md) | 完整语法与降级规则 |
| [roadmap](docs/roadmap.md) | 砍掉的特性与复活条件 |

## 验证

编译产物由 [GoMcRuntime](https://github.com/Mcd0LUO/gomc) 执行。它实现的是
**经反编译和真服实测校验过的** 1.12.2 语义，不是猜测。

```sh
go test ./...                                    # 63 条，语义测试跑在真 1.12.2 语义上
go test ./internal/driver -run GoldenBaseline    # 逐字：与签入的输出比对
```

运行时通过 `CMCF_GOMC` 环境变量定位，默认查找 `D:\code\go\GoMcRuntime\gomc.exe`。
找不到时语义测试会 skip，而不是失败。

Golden 比对失败时**先读 diff 再决定**：

```sh
go test ./internal/driver -run GoldenBaseline -update
git diff internal/driver/testdata/golden
```

不看就 `-update`，签入的只是「现在是这样」，而不是「我知道变成了这样」。

## 许可证

[MIT](LICENSE)。
