# 为 kibana-cli 贡献代码

*[English](CONTRIBUTING.md) | 中文*

感谢你改进 **kibana-cli**：一个 Kibana CLI：通过 Kibana 的 Console Proxy 搜索、读取和统计 Elasticsearch 里的应用日志，也能列出 Kibana 的数据视图、字段和保存对象。本文档介绍如何构建、测试和提交改动。

> 这是一个为 AI 工具实验而分享的业余项目；维护者不提供商业支持或生产环境保证。

## 构建操作手册在规范里

本仓库是一个 **AI 原生 CLI 工具**：主要调用方是 AI Agent。实现或修改功能之前，先读：

- **[AGENTS_zh.md](AGENTS_zh.md)**：入口，以及每次改动都要遵守的规则。
- **[`.agent/AGENT_zh.md`](.agent/AGENT_zh.md)**：操作手册，涵盖工作流、硬约束和自检。
- **[`.agent/CLI-SPEC_zh.md`](.agent/CLI-SPEC_zh.md)**：机器契约，涵盖输出、错误、命令模型、发现、读取、写入门控（write gate）和自更新（`CLI-xxx`）。
- **[`.agent/SEC-SPEC_zh.md`](.agent/SEC-SPEC_zh.md)**：风险档、不可信内容、授权范围文件、机密、状态存储和供应链（`SEC-xxx`）。
- **[`.agent/SKILL-SPEC_zh.md`](.agent/SKILL-SPEC_zh.md)**：随附的 Agent Skill（`SKL-xxx`）。
- **[`.agent/VERIFY-SPEC_zh.md`](.agent/VERIFY-SPEC_zh.md)**：符合性测试套件、测试、成熟度与证据，以及 Agent 评测（`VER-xxx`）。

这些规范、`contract/` 和 aicli 库（Go 中为 `internal/aicli/`，Python 中为 `<pkg>/_aicli/`），都是按 `.agent/SPEC_VERSION` 中的标签，从 [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec) 以副本方式引入的。它们的优先级高于默认习惯，并且在本仓库中只读：它们与固定版本的规范不一致时，`node scripts/check-spec.js` 会让 CI 失败。它们需要的改动，以 issue 或 pull request 的形式提交到规范仓库；改动发布后，更新 `.agent/SPEC_VERSION` 并运行 `node scripts/sync-spec.js`（REPO-063）。不使用库、自己实现输出、错误或写入门控的代码，不会被合并（CLI-001、CLI-050）。

## 开发环境

<!--
语言工具链：下面只保留一个块，删掉另一个。
  - Go（go.mod 中的版本）：编译型二进制 + npm 包装器
  - Python 3.10+：PyInstaller 二进制 + npm 包装器
  - Node 16+：每种变体都需要，用于 npm 包装器以及规范和版本脚本
形态始终是：安装依赖 -> 构建 -> 测试 -> 运行一次 `--help` 冒烟测试。
-->

```bash
# Clone
git clone https://github.com/fatecannotbealtered/kibana-cli.git
cd kibana-cli

# --- Go variant ---
go mod download
make build                      # or: go build -o bin/kibana-cli ./cmd/kibana-cli
make test                       # go vet, then go test -race ./... (conformance suite included)
./bin/kibana-cli --help

# --- Python variant ---
# pip install -e ".[dev]"
# python build.py
# pytest tests/ -v              # conformance suite included
# kibana-cli --help

# Both: vendored files and version locations
node scripts/check-spec.js && node scripts/check-version.js
```

如果拉依赖慢，用区域代理（如 Go：`GOPROXY=https://goproxy.cn,direct`；pip 用镜像源）。

## 命令

| 目标 | ▶ 命令 |
|------|--------|
| 构建 | `make build`（Go）/ `python build.py`（Python） |
| 测试（含符合性测试套件） | `make test` → `go test -race ./...` / `pytest tests/ -v` |
| 只跑符合性测试套件 | `make conformance` / `pytest tests/test_conformance.py` |
| 静态检查 | `make lint` → `golangci-lint run ./...` / `ruff check .` |
| 格式化 | `make fmt` → `gofmt -w .` / `ruff format .` |
| 内置副本文件与固定版本一致 | `node scripts/check-spec.js`（Go：`make spec-check`） |
| 固定版本变更后重新引入副本 | `node scripts/sync-spec.js`（Go：`make spec-sync`） |
| 各处版本号一致 | `node scripts/check-version.js` |

`make` 目标由变量驱动；在 Windows 上退回到底层工具命令。推送前在本地跑 **lint + test**。

## 分支与提交规范

- 从默认分支拉出：`git checkout -b feat/your-feature`。
- 一个分支只做一件逻辑改动；请求 review 前先 rebase 默认分支。
- 提交遵循 [Conventional Commits](https://www.conventionalcommits.org/)：

```text
<type>: <description>

<optional body>
```

| 类型 | 用于 |
|------|------|
| `feat` | 新功能 |
| `fix` | 缺陷修复 |
| `refactor` | 不改变行为的代码重构 |
| `docs` | 文档改动 |
| `test` | 新增或更新测试 |
| `chore` | 构建、CI、依赖或工具链改动 |
| `perf` | 性能优化 |
| `ci` | CI/CD 流水线改动 |

示例：`feat: add export command`、`fix: handle nil pointer in status check`、`docs: sync README_zh`。适用时按 ID 引用规范规则（`fix: bind the body file by content (CLI-054)`）。

## CI 检查

`.github/workflows/ci.yml` 在每次推送和每个 pull request 上运行：格式检查和 lint；在 Linux、macOS 和 Windows 上运行测试（含符合性测试套件）；依赖审计（`govulncheck` 或 `pip-audit`，以及 `npm audit`）；`check-version.js` 和 `check-spec.js`。任何一步变红都会阻止合并。`release.yml` 在签名或发布任何内容之前，对打了标签的提交运行同样的检查（REPO-070、REPO-071、VER-008）。

## 测试与证据

“测试过”是什么意思，由 [`.agent/VERIFY-SPEC_zh.md`](.agent/VERIFY-SPEC_zh.md) 定义：

- **符合性测试套件（VER-001）**。一个测试运行 `aicli.RunConformance`（Python：`run_conformance`），并提供一个统计写入次数的模拟上游、每个写命令的 `Sample` 和一个授权范围。它对每个写命令检查写入门控（G00 至 G10），并对整个工具检查 `reference`、命令树、用法错误、信封、`context`、`doctor` 和 `operation`（R01 至 R05）。缺少样例的写命令会让套件失败。绝不跳过、过滤或削弱任何一项检查。
- **真实输出（VER-002）**。命令级测试通过同一个辅助函数运行 CLI：它执行 App，用库自带的校验器校验信封，并对照 contract.json 检查退出码。
- **按实际运行统计覆盖（VER-003）**。注册表中的每个命令（包括内置命令）都至少在一个命令级测试中运行过。测试运行期间，`KIBANA_CLI_COVERAGE_FILE` 指向一个临时文件，库把每个执行过的命令记录在里面；完整跑完之后，`aicli.CheckCoverage`（Python：`check_coverage`）遍历注册表，列出每个从未运行过的命令（R06）。在 Go 中，这一步是 `cmd/main_test.go` 里的 `TestMain`。没有源码扫描式的守卫。
- **行为矩阵（VER-004）**。处于 `experimental` 或更高级别的每个命令，都要为适用于它的各行编写测试：成功、无效输入、认证与权限、上游失败、空结果、分页、退出码、读取无副作用，对写入还有写入门控和 apply 失败。不适用的行写成一个被跳过的子测试，跳过原因以 `not applicable:` 开头。每个改变可观察行为的缺陷修复，都要补一个回归测试。
- **成熟度（VER-005）**。每个命令在其 `Meta` 中声明 `Maturity` 和 `Evidence`，级别绝不超过证据：`draft`（已声明）、`experimental`（针对模拟上游跑过行为矩阵，`simulated:` 证据）、`verified`（另有一次针对真实 Kibana 的运行记录，`live:` 证据保存在 `docs/E2E.md` 中）、`stable`（另外通过了 Agent 评测，且没有未关闭的 P1 缺陷，`eval:` 证据）。改动命令的请求、解析或输出，会让它的 `live` 和 `eval` 证据作废：在同一次改动中重新运行它们，或者降低成熟度。
- **Agent 评测（VER-006）**。在发布改动了命令、输出或 Skill 的版本之前，用 `skills/kibana-cli/test-prompts.json` 运行 Agent 评测，带 Skill 和不带 Skill 各跑一次。要把任何命令标为或保持为 `stable`，都必须先做这项评测。
- **不碰用户状态（REPO-073）**。测试把状态目录指向临时目录（`App.StateDir` 或 `KIBANA_CLI_STATE_DIR`），绝不指向用户的确认密钥、操作记录或授权范围文件。

## 新增或修改命令

1. **客户端**：把上游调用加在该领域其他调用的旁边。复用共享的 HTTP 和认证辅助函数，用参数构造 URL（绝不拼接用户输入），用 `aicli.FromHTTP` 按状态码映射失败（CLI-016），给每个请求设置超时并校验 TLS，并且只重试读取、最多两次，绝不重试写入（CLI-110）。
2. **命令**：添加 cobra（Go）或 click（Python）命令，并用 `app.Register(cmd, aicli.Meta{...})` 注册：摘要、效果、`Dangerous`、成熟度和证据、输出 schema（`app.Schema`）、错误码和示例（写命令的示例展示 `--dry-run` 和 `--confirm` 这一对，危险命令的示例展示 `--dangerous`），适用时再加上 `Positional`、`FileInputs`、`Secrets`、`DefaultFields` 或 `BatchFlag`（CLI-020 至 CLI-027）。flag 的名称取自共享词汇（`--limit`、`--cursor`、`--query`、`--since`），动词保持标准含义（CLI-028、CLI-029）。`--help` 和 `reference` 由注册表生成，所以命令无需额外接线就会出现在两者中。
3. **读取**通过 `app.OK` 或 `app.List` 返回，并带上 `scope`，说明列表实际覆盖了什么（CLI-041）。**写入**通过 `app.Write`（或 `app.WriteBatch`）返回，提供只读的 `Preview`，以及回读自身改动的 `Apply`；只有情况属实时，才把 `Apply` 的错误包装成 `NotDispatched` 或 `Rejected`（CLI-050、CLI-059）。
4. **测试**：上面行为矩阵中的各行，以及每个新写命令的符合性 `Sample`。
5. **Skill**：重新生成命令地图。它位于 `skills/kibana-cli/SKILL.md` 中，以及 `README.md` 和 `README_zh.md` 的能力（Capabilities）一节中，夹在 `aicli:command-map` 标记之间。命令地图由注册表渲染，由命令地图测试检查；在 Go 中，`UPDATE_SKILL_MAP=1 go test ./cmd -run TestSkillCommandMap` 会重写每一块。绝不手工编辑它们。然后更新任务流程（playbook）和 `SKILL.md` 中讲领域知识的那几行，并添加或调整 `test-prompts.json` 的条目（`id`、`scenario`、`prompt`、`expected`，以及可选的 `grader_notes`）。只有部分任务需要的深入内容放到 `skills/kibana-cli/references/<topic>.md`，并从 `SKILL.md` 链接过去，后者保持在 150 行以内（SKL-002、SKL-003、SKL-011）。`metadata.version` 和 `metadata.min_version` 通过 `npm version` 跟随 `package.json`；绝不手工编辑。
6. **文档**：在 `CHANGELOG.md` 的 `## [Unreleased]` 下加一行。Agent 更新后会通过 `kibana-cli changelog` 读它，所以要写明改动了哪些命令、flag 和字段。将要移除的命令或 flag 要设置 `Meta.Deprecated` 或 `Meta.DeprecatedFlags`，并在 `Deprecated` 下加一行；只能在 MAJOR 版本中移除（CLI-081）。

## Pull Request 指南

1. 尽量 **一个 PR 一件逻辑改动**。
2. **测试**：行为改动要加/改测试。
3. **文档**：命令、flag 或输出变化时，更新面向用户的文档和 Skill。
4. **提交**：Conventional Commits；任何地方都不要出现机密或真实令牌。

### PR 检查清单

- [ ] 单一逻辑改动，diff 聚焦
- [ ] 测试已添加或更新并通过（`make test` / `pytest`），含符合性测试套件
- [ ] 每个新写命令都有符合性 `Sample`，每个新命令都在测试中运行（VER-001、VER-003）
- [ ] 每个改动过的命令，成熟度仍与证据相符（VER-005）
- [ ] lint 和格式检查通过
- [ ] `node scripts/check-spec.js` 通过：没有手工编辑任何内置副本
- [ ] `node scripts/check-version.js` 通过
- [ ] 命令地图已重新生成（Skill 和 README 的能力一节）；任务流程和 `test-prompts.json` 已调整
- [ ] 改动涉及命令、输出或 Skill，且有命令处于 `stable` 时，已运行 Agent 评测（VER-006）
- [ ] `CHANGELOG.md`（`## [Unreleased]`）已更新
- [ ] **双语文档已同步**：每处 `*.md` 改动都同步到对应的 `*_zh.md`（反之亦然）
- [ ] 代码、测试、测试夹具、证据记录和提交历史中都没有机密、令牌或真实凭证
- [ ] 提交信息遵循 Conventional Commits

## 安全

不要为未披露的安全漏洞开公开 issue。见 [SECURITY_zh.md](SECURITY_zh.md)。
