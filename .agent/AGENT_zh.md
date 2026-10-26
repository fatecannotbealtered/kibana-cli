# AGENT.md：编码 Agent 的操作手册

**English → [AGENT.md](AGENT.md)**

构建或修改本工具之前，先读这份文件。它梳理各份规范的分工，列出你不能违反的约束，并给出每类任务的工作流。`.agent/`、`contract/` 和 aicli 库按 `.agent/SPEC_VERSION` 中的 tag，以副本方式引入（vendored）自 [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec)，它们的优先级高于你的默认做法。只读你的任务需要的那份规范，并在提交和评审中按 ID 引用规则（`CLI-054`）。

## 各规范的分工

| 文档 | 内容 | 规则 |
|---|---|---|
| `AGENT.md`（本文件） | 工作流、硬约束、自检 | – |
| `CLI-SPEC.md` | 机器契约：输出、错误、命令模型、发现、读操作、写入门控（write gate）、自更新、版本与弃用、设置与配置档、网络 | CLI-xxx |
| `SEC-SPEC.md` | 风险档（risk tier）、授权范围文件（scope file）、写入门控的安全、机密、对外连接、状态存储、供应链 | SEC-xxx |
| `SKILL-SPEC.md` | Skill：frontmatter、内容、篇幅、检查点、打包、评测 | SKL-xxx |
| `VERIFY-SPEC.md` | 符合性测试套件、在真实输出上校验契约、覆盖率、行为矩阵、成熟度、Agent 评测、发布一致性 | VER-xxx |
| [`REPO-SPEC.md`](https://github.com/fatecannotbealtered/ai-native-cli-spec/blob/main/REPO-SPEC_zh.md)（在规范仓库中，取你固定的 tag 版本） | 仓库布局、以副本方式引入、CI、发布、分发 | – |
| `contract/contract.json`、`contract/schemas/` | 错误码、退出码、枚举和键集合的机器可读事实来源；正文以它为准 | – |

aicli 库（Go：`internal/aicli/`；Python：`<pkg>/_aicli/`）实现了 CLI-SPEC 和 SEC-SPEC 的大部分内容：信封（envelope）和错误、注册表、帮助和 `reference`、用法建议、写入门控（绑定、令牌、操作记录、授权范围、回执）、内置命令（包括用 `App.Update` 启用的 `update`）、设置与配置档、校验器和符合性测试套件。每份规范都写明了库做了什么。你要写的是：命令及其 `Meta`、上游客户端、每个写操作的 `Preview` 和 `Apply`、`context` 和 `doctor` 的钩子、`UpdateConfig` 和声明的设置、测试、Skill 和证据。规范仓库的 `template/go/example/` 和 `template/python/example/` 用一个小型笔记 CLI 演示了所有模式。

## 硬约束

1. 绝不手工编辑内置副本文件（`.agent/`、`contract/`、aicli 目录）。在规范仓库中修改它们，然后运行 `node scripts/sync-spec.js`（SEC-050）。
2. 所有输出都经过 aicli：stdout 上只有一个 JSON 信封，别无其他。绝不自己往 stdout 写东西（CLI-001、CLI-002）。
3. 每个带有 `observe` 以外效果的命令，都调用 `App.Write` 或 `App.WriteBatch`。绝不自己添加令牌、密钥、确认存储或写入门控标志（CLI-050）。如实声明 `Effects` 和 `Dangerous`；读操作绝不写入（CLI-025）。
4. 除非用户明确要求你这样做，否则 CLI 和你都不创建或编辑授权范围文件（SEC-011、SKL-006）。
5. 用 `FromHTTP` 按状态码映射上游失败，绝不按消息文本映射（CLI-016）。在 `Apply` 中，只有什么都没发出去时才把错误包装成 `NotDispatched`，只有上游明确拒绝时才包装成 `Rejected`，而且包装的是 `FromHTTP` 返回的 `*aicli.Error`（连接根本没建立时状态码为 0），绝不包装原始错误；其他错误原样返回，让 aicli 报告 `E_OUTCOME_UNKNOWN`（CLI-015、CLI-050）。
6. 在 `Meta.Secrets` 中声明机密标志，绝不记录或回显它们，也绝不输出上游返回的机密（SEC-021）。把每个承载外部文本的输出路径声明为不可信，带着人写的文本的 ref 也包括在内（CLI-046、SEC-002）。
7. 列表要说明自己的完整性和范围（CLI-041）；计数、过滤和查找最新项都在工具里完成（CLI-044）。
8. `update` 是 aicli 的内置命令：把 `App.Update` 设为一个写明仓库和包名的 `UpdateConfig`，绝不自己编写 `update` 命令或任何更新代码（CLI-070）。
9. 每个写命令都有一个符合性 `Sample`，注册表中的每个命令都在测试中运行过（VER-001、VER-003）。
10. 只通过 `app.Setting` 读取设置，绝不把机密放进设置文件（CLI-100、SEC-040）。诊断输出经由 `<PREFIX>_DEBUG` 下的 `app.Debugf`，绝不通过 `--verbose` 或 `--debug` 标志（CLI-102）。
11. 每个上游请求都有超时并校验 TLS；读取最多重试两次，写入绝不重发；工具只连接配置的上游，不发送遥测（CLI-110、SEC-022）。
12. 使用共享的标志名和动词含义；命令或标志要先弃用再移除，并且只在 MAJOR 版本中移除（CLI-028、CLI-029、CLI-081）。

## 工作流 A：从模板新建工具

1. 实例化：按规范仓库中的 `template/INSTANTIATE.md` 操作：复制 `common/`，以及叠加层（overlay）中仅限 CI 和构建的文件（绝不复制它的 `aicli/` 或 `example/`；库由 sync-spec 以副本方式引入），填好占位符，并把规范的 tag（例如 `v2.0.0`）写入 `.agent/SPEC_VERSION`。
2. 引入副本：`node scripts/sync-spec.js` 复制规范、契约和 schema 以及 aicli；`node scripts/check-spec.js` 必须通过。
3. 接好应用（以 Go 为例；Python 基于 click，结构相同，名称用 snake_case）。所有环境变量都通过 `app.Env` 读取，这样测试可以注入；`main` 把它设为 `os.Getenv`，因为这个字段没有默认值。`Update` 启用内置的 `update`（CLI-070），`Settings` 声明由 `app.Setting` 解析的设置（CLI-100）。
   ```go
   app := &aicli.App{Name: "notes-cli", Version: version, RiskTier: "T1", EnvPrefix: "NOTES_CLI",
       Root: root, Changelog: changelogMD, Identity: identity, Env: os.Getenv,
       Update:   &aicli.UpdateConfig{Repo: "acme/notes-cli", NPMPackage: "@acme/notes-cli"}, // the built-in update
       Settings: []aicli.Setting{{Name: "host", Description: "Acme Notes URL"}}}            // NOTES_CLI_HOST, profiles
   os.Exit(app.Execute(os.Args[1:]))
   ```
4. 设计命令树：扁平的 `<noun> <verb>`，`list`、`get`、`search`、`create`、`update`、`delete` 排在领域动词之前（CLI-021），每个动词取它的标准含义，有合适的共享词汇时，标志就用共享词汇中的名称（CLI-028、CLI-029）。通过 `AddShortcut`（只嵌套在一个父级下的名词，每个有一个根级快捷方式）、`ShortFlags`、`FlagAliases` 和 `Meta.Aliases`，接受在工具群（fleet）中实测到的猜测写法，其余的通过 `FlagSynonyms` 和 `VerbSynonyms` 给出建议（CLI-022）。
5. 用 `app.Register(cmd, aicli.Meta{...})` 注册每个叶子命令：`Summary`、`Effects`、`Dangerous`、`Maturity`、`Output`（用 `app.Schema` 定义的 schema）、`ErrorCodes`、`Examples`（写命令要展示预演（dry-run）和确认这一对），适用时再加上 `Positional`、`FileInputs`、`Secrets`、`DefaultFields` 和 `BatchFlag`。
6. 读命令通过 `app.OK` 或 `app.List(cmd, aicli.Page{...})` 返回；错误使用构造函数（`Usage`、`Validation`、`NotFound`、`Ambiguous`、`FromHTTP`）。
7. 写命令通过 `app.Write` 返回；批量操作使用 `app.WriteBatch` 配合 `Meta.BatchFlag`（CLI-063）。需要查询上游才能确定的目标，由 `WriteSpec.ResolveTarget` 提供，写入门控在令牌检查之后才调用它；绝不在 `app.Write` 之前读取上游来确定目标（CLI-057）。
   ```go
   return app.Write(cmd, aicli.WriteSpec{
       Target:  aicli.Target{Key: "team/notes#7", Kind: "note", Label: "#7 Groceries"},
       Preview: preview, // reads only, deterministic; returns Changes and BaseVersion
       Apply:   apply,   // sends the change; returns Result{Outcome, Observed} from a read-back
   })
   ```
8. 测试：一个符合性测试，即 `aicli.RunConformance(t, h)`，配一个设置了 `NewApp`、`Writes`、`Reset`、`Samples` 和 `Scope` 的 `Harness`（VER-001）；每个命令的行为矩阵测试，通过一个校验每个信封的辅助函数运行（VER-002、VER-004）；以及覆盖率：在工具自己的测试运行期间设置 `<PREFIX>_COVERAGE_FILE`，然后在 `TestMain` 中的 `m.Run()` 之后，带上 `CoverageFile` 调用 `aicli.CheckCoverage(t, h)`（R06、VER-003）。
9. 安全：根据效果推导风险档，并填写 `SECURITY.md`（SEC-001、SEC-060）。
10. Skill：按 SKILL-SPEC 编写，命令地图取自 `app.CommandMap()`，放在 `aicli:command-map` 标记之间（CLI-020、SKL-002）；`test-prompts.json` 覆盖 VER-006 的各个场景。
11. 证据：命令在行为矩阵通过之前一直是 `draft`（通过后为 `experimental`）；有一次针对真实上游的运行记录，它就成为 `verified`；通过 Agent 评测后成为 `stable`（VER-005、VER-006）。
12. CI 和发布遵循 REPO-SPEC；发布时运行合并时的每一项检查（VER-008）。

## 工作流 B：扩展本工具

1. 读你要改动部分的规则：输出和错误（CLI-001 至 CLI-018）、命令和发现（CLI-020 至 CLI-037）、读操作（CLI-040 至 CLI-049）、写操作（CLI-050 至 CLI-065、SEC-010 至 SEC-021）、设置和网络（CLI-100 至 CLI-110、SEC-022）、Skill（SKILL-SPEC）。
2. 新命令：用 `Meta` 注册它；VER-003 要求有一个运行它的测试。如果它会写入，就用 `app.Write` 实现并加上它的 `Sample`，否则 G00 会失败。根据它的证据设定成熟度（VER-005）。
3. 参数或输出有变化：更新输出 schema。你要改名或移除的命令、标志或字段，先标为弃用（`Meta.Deprecated`、`Meta.DeprecatedFlags`），至少在一个 MINOR 版本中、并且至少在 90 天内保持可用，只在 MAJOR 版本中移除（CLI-081）。这次改动会使该命令的 `live` 和 `eval` 证据作废：重新运行它们，或者降低它的成熟度（VER-005）。
4. Agent 一直在猜一种你不接受的写法（评测轨迹、缺陷报告里能看到）？那就接受它（`Meta.Aliases`、`FlagAliases`、`AddShortcut`），或者让错误带上准确的 `did_you_mean`（`FlagSynonyms`、`VerbSynonyms`）（CLI-022）。
5. 更新 `CHANGELOG.md` 和 Skill（剧本手工更新，命令地图从注册表生成）。绝不编辑 `metadata.version` 或 `metadata.min_version`：每次发布时 `npm version` 都会把两者设为工具版本（`scripts/sync-version.js`），值不同时 `check-version.js` 会失败（SKL-001、REPO-031）。
6. 合并之前：包括符合性测试套件在内的测试、`node scripts/check-spec.js`、lint。在改动命令、输出或 Skill 的发布之前：Agent 评测（VER-006）。
7. 新的规范版本：把它的 tag 写入 `.agent/SPEC_VERSION`，运行 `node scripts/sync-spec.js` 和完整的测试套件，然后修改工具，绝不修改内置副本文件。

## 工作流 C：迁移 1.6.x 工具

这条路径以规范仓库中的 `MIGRATION-2.0.md` 为准；逐步照做。概要如下：

1. 固定到 `v2.0.1`，同步，让输出和错误都经过 aicli：`schema_version` 变为 `"2.0"`，JSON 默认紧凑输出。
2. 用 `Meta` 注册每个命令（效果、成熟度、证据），并加上根级快捷命令和实测到的猜测写法（CLI-021、CLI-022）。
3. 把每个写操作迁移到 `app.Write` 或 `app.WriteBatch` 上。删除工具自己的令牌、密钥和已用令牌存储代码，以及任何为旧测试削弱绑定的开关，并迁移那些手工签发令牌的测试。
4. 删除 `release_readiness` 及其 `doctor` 检查、FCC 检查和各工具自己的 `contract_gen`；由 VER-001 至 VER-005 取代它们。
5. 删除工具自己的 `update`，设置 `App.Update`；把配置迁移到带配置档的 `App.Settings` 中；把标志改成共享词汇中的名称，旧名称保留为已弃用（CLI-070、CLI-100、CLI-028、CLI-081）。
6. 告诉用户他们会遇到哪些变化：在他们授予授权范围文件之前，写入都会被拒绝（SEC-010）；部分成功的批量操作以 `E_PARTIAL` 失败（CLI-063）；密钥或令牌存储不可用时，写入会被阻止（CLI-055）。
7. 按 SKILL-SPEC 重写 Skill（没有引导步骤，不抄录协议，有命令地图），然后以最后一个 1.6.x 发布为基线运行 Agent 评测（VER-006）。

## 收尾前自检

- [ ] `node scripts/check-spec.js` 通过；没有手工编辑任何内置副本。
- [ ] 每个叶子命令都有 `Meta`；符合性测试套件通过，包括 R01（reference 预算）和 R02（命令树规则），`CheckCoverage`（R06）也通过。
- [ ] 每个非 `observe` 命令都经过 `app.Write` 或 `app.WriteBatch`，并且有 `Sample`。
- [ ] 读命令不发送任何改变状态的请求；`Effects` 和 `Dangerous` 与代码实际做的事相符。
- [ ] 每个命令的行为矩阵各行，要么已测试，要么以 `not applicable: <reason>` 跳过；测试中的每个信封都经过校验。
- [ ] 只在属实时才使用 `NotDispatched` 和 `Rejected`；上游错误经过 `FromHTTP`。
- [ ] 机密标志和不可信路径都已声明；列表带有完整性和范围信息。
- [ ] `update` 来自 `App.Update`，工具没有自己的更新代码；设置经由 `app.Setting` 读取，设置文件中没有机密。
- [ ] 上游请求有超时并校验 TLS，写入绝不重发，除了配置的上游，什么都不发往别处。
- [ ] 每个命令的成熟度与其证据相符。
- [ ] `CHANGELOG.md`、Skill 和 `test-prompts.json` 都已更新。
- [ ] 如果命令、输出或 Skill 有变化，且有命令处于 `stable`：Agent 评测已运行并通过。

本文属于 [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec)。Copyright (c) 2026 Sean Guo。采用 MIT 许可证，见规范仓库中的 `LICENSE`。
