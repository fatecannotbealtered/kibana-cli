# 开源检查清单

[English](OPEN_SOURCE_CHECKLIST.md) | [中文](OPEN_SOURCE_CHECKLIST_zh.md)

在 `kibana-cli` **首次公开推送之前**，逐项过一遍这道关卡。它是安全与质量检查点，不是文档：仓库公开之前，每一项都必须勾选，或者附上说明、有意识地豁免。仓库一旦公开，历史中泄露的机密就无法收回。规则 ID 指的是 `.agent/` 中的规范，以及 ai-native-cli-spec 中的 REPO-SPEC。

## 机密

- [ ] 工作区任何位置都没有凭证、令牌、API 密钥或密码。
- [ ] **git 历史**中没有机密，已用扫描工具检查（如 `gitleaks detect`、`git log -p | grep`）；如有发现，要重写历史或重建仓库，而不是只从 `HEAD` 中删除。
- [ ] 代码、配置、注释中没有内部主机名、内网 IP、内部 URL 或公司内部标识符。
- [ ] 测试夹具、录制的响应和证据记录（`docs/E2E.md` 及其引用的文件）只包含合成或脱敏的数据：argv 中的机密已脱敏，没有真实账户数据，没有真实令牌（VER-005）。
- [ ] `.env`、`*.local`、凭证文件，以及状态目录中的一切（`~/.kibana-cli/`：`confirm.key`、`operations/`、`scope.json`、`update-check.json`）都未被跟踪（`git status --ignored`）；运行时状态绝不放在仓库里（REPO-050）。
- [ ] 测试使用临时状态目录，绝不使用开发者的确认密钥、操作记录或授权范围文件（REPO-073）。
- [ ] 保存的凭证遵循钥匙串（keyring）模式：操作系统钥匙串优先，加密文件只作为会被报告出来的回退方案（`context.credentials.source`），配置文件中没有机密（SEC-040）。

## 文档

- [ ] `README.md` 遵循 REPO-010 骨架：标题、语言切换和徽章 → 一句话 → Agent 安装 → 它做什么 → 授权 → 能力 → Agent 工作流 → 机器契约 → 配置 → 卸载 → 项目结构 → 开发 → 链接。
- [ ] Agent 安装代码块粘贴到干净的 shell 里就能运行；它用固定版本的安装器（`npx skills@<version> add`）从已安装的包安装 Skill，而不是从某个分支安装；它也不先运行 `context`、`doctor` 或 `reference`（REPO-010、SKL-009、CLI-034）。
- [ ] 授权一节给出授权范围文件的路径、一份最小授予和保护声明，并且从不让 Agent 去写授权范围文件（REPO-011、SEC-013）。
- [ ] README 能力一节中的地图和 Skill 中的命令地图都是 `CommandMap()` 的输出，命令地图测试会检查这两处（SKL-002）。
- [ ] 配置一节列出每项设置及其环境变量，并展示设置文件中的配置档；卸载一节说明怎样移除包或二进制、状态目录、钥匙串条目、放在别处的设置文件和 Skill 目录（REPO-010、REPO-011、CLI-100）。
- [ ] `README.md` 与 `README_zh.md` **内容同步**：章节相同、命令相同、填入的值相同。
- [ ] `CHANGELOG.md` 采用 Keep a Changelog 格式，顶部是 `## [Unreleased]`，第一个发布小节带有真实日期。
- [ ] `LICENSE` 存在，许可证经过有意选择（默认 MIT），版权年份和版权持有人已填写。
- [ ] 没有遗留模板占位符：`grep -rnE '\{\{[A-Z_0-9]+\}\}' . --exclude-dir=node_modules --exclude-dir=.git` 什么也找不到。

## 治理

- [ ] `SECURITY.md` 有可用的披露渠道和受支持版本表，并写明风险档，用一句话说明最坏情况（SEC-001）。
- [ ] 对有写命令的工具，`SECURITY.md` 还要给出授权范围文件的路径、如何授予和撤回授权范围，以及如实的保护声明（SEC-011、SEC-013）。
- [ ] 工具不发送遥测，只连接配置的上游，以及在 `update` 和 `doctor` 中连接它的发布源；其他任何目的地都列在 `SECURITY.md` 中（SEC-022）。
- [ ] `CONTRIBUTING.md` 存在（环境搭建、分支与提交规范、测试与证据、PR 流程）。
- [ ] 若项目接受外部贡献，`CODE_OF_CONDUCT.md` 存在（Contributor Covenant）。
- [ ] 如果 `kibana-cli` 封装了第三方产品（Kibana），`NOTICE.md` 载明商标声明和非关联声明，`docs/COMPATIBILITY.md` 列出验证过的上游版本；否则两个 NOTICE 文件都已删除。

## 构建 / CI

- [ ] 待推送的提交上，CI（`.github/workflows/ci.yml`）为**绿色**，并且任何一步变红都会阻止合并（不只是提示）。
- [ ] CI 运行格式检查和 lint、在每个操作系统上运行含符合性测试套件的测试、对发布涉及的每个生态做依赖审计，并运行 `check-version.js` 和 `check-spec.js`（REPO-070、SEC-050）。
- [ ] 符合性测试运行时，每个写命令都有 `Sample`，并开启覆盖记录（R06），什么都不过滤（VER-001、VER-003）。
- [ ] 测试产生的每个信封都经过校验，处于 `experimental` 或更高级别的每个命令都有行为矩阵测试（VER-002、VER-004）。
- [ ] 每个命令的成熟度与证据相符：`verified` 引用一条 `live:` 记录，`stable` 还要引用一份 `eval:` 报告，并且引用的每个文件都存在（VER-005）。
- [ ] `release.yml` 在签名或发布任何内容之前，对打了标签的提交运行合并时的每一项检查（VER-008）。
- [ ] 没有遗留已退役的 1.x 文件或字段：没有 FCC 守卫，没有 `release_readiness`，没有各工具自带的 `contract_gen`，也没有工具自写的确认令牌、密钥或已消费令牌存储代码（REPO-001）。
- [ ] 格式化工具配置已提交（按语言：ruff / golangci-lint / prettier），且 CI 运行格式校验。
- [ ] 没有提交构建产物、缓存、虚拟环境或 IDE 配置（已由 `.gitignore` 覆盖）。
- [ ] launcher-budget 作业已运行；如果 npm 启动器每次调用增加超过 100 ms，README 的 Agent 安装一节要把原生二进制放到 PATH 上（REPO-041）。

## 分发

- [ ] `package.json` 的 `version` 与待发布的 git 标签一致（`vX.Y.Z` ↔ `X.Y.Z`）；`release.yml` 对此做守卫，不一致即失败。
- [ ] `package.json` 的 `version` 是唯一手工编辑的版本号：`npm version` 会同步锁文件、每个 Skill 的 `metadata.version` 和 `metadata.min_version`、Python 的 `__version__` 以及 CHANGELOG，并且 `check-version.js` 为绿色（REPO-031）。
- [ ] 二进制本身（`bin/`、`*.exe`、`dist/`）**不提交**；由 CI 构建，并由 `.gitignore` 覆盖。
- [ ] 发布压缩包附带一个 `checksums.txt`，它在按标签触发的发布工作流中用 Sigstore 无密钥签名签署，bundle 发布在它旁边；二进制更新路径按失败即拒绝（fail closed）的方式，在进程内验证签名和校验和（REPO-042、SEC-050）。
- [ ] npm 从 CI 构建的产物发布主包装器包，以及每个受支持 OS/CPU 的平台包，使用 `npm publish --provenance`；安装时不再下载任何其他东西（REPO-040）。
- [ ] 每个分发渠道都附带与自身版本一致的 Skill：`npm pack --dry-run` 列出了 `skills/`（REPO-043）。
- [ ] 运行时的 `changelog` 命令和 GitHub Release 正文都来自 `CHANGELOG.md`，而不是手工复制的文本（REPO-032）。
- [ ] `update` 是 aicli 的内置命令，用 `App.Update` 和本工具发布时使用的仓库名、包名启用；工具没有自己的更新代码，发布压缩包的根目录下放着二进制和 `skills/`，这样裸 `update`（一条命令，不需要确认令牌）就能校验这次发布、安装它并从中复制 Skill（CLI-070、REPO-043）。

## AI 原生

- [ ] 根目录 `AGENTS.md` 存在并指向 `.agent/AGENT.md`。
- [ ] `.agent/` 中有五份规范（`AGENT`、`CLI-SPEC`、`SEC-SPEC`、`SKILL-SPEC`、`VERIFY-SPEC`）和 `SPEC_VERSION`；`contract/` 中有 `contract.json` 和 `schemas/`；aicli 库只存在于它的内置副本路径（`internal/aicli/` 或 `<pkg>/_aicli/`），并带有 `MANIFEST.json`；`check-spec.js` 为绿色（REPO-001、REPO-062）。
- [ ] `skills/kibana-cli/SKILL.md` 遵循 SKILL-SPEC 2.0：frontmatter 只用 Agent Skills 的键（`name` 与目录名相同；`description` 说明做什么、何时用、不用于什么；`license`；`compatibility`；`metadata` 含 `version`、`min_version` 和 `requires.bins`），最多 150 行，有生成的命令地图、一份写入配方、按风险分级的 `STOP CHECKPOINT` 行，有授权范围、不可信内容、歧义和回复丢失各自的那几行，以及任务结束时的报告（SKL-001 至 SKL-008）。
- [ ] `skills/kibana-cli/test-prompts.json` 是合法的 JSON，条目只有 `id`、`scenario`、`prompt`、`expected` 和可选的 `grader_notes`，并覆盖 VER-006 的每个场景（SKL-011）。
- [ ] 全新安装后，`kibana-cli reference --index`、`kibana-cli context` 和 `kibana-cli doctor` 能离线运行并输出合法的信封；授予授权范围后，`doctor` 的 `authorization` 和 `version` 检查通过（CLI-031、CLI-035、CLI-036）。
- [ ] 没有授权范围文件时，每个写命令都以 `E_FORBIDDEN` 和 `details.scope` 被拒绝，不发送任何内容（SEC-010）。
- [ ] 标志使用共享词汇，动词保持标准含义；设置来自 `App.Settings`；没有 `--verbose` 或 `--debug` 标志；上游请求有超时并校验 TLS，绝不重发写入（CLI-028、CLI-029、CLI-100、CLI-102、CLI-110）。
- [ ] `SECURITY.md` 中的风险档等于 `reference.risk_tier`，并且不低于各命令的效果所要求的档位（SEC-001）。
- [ ] 没有通过 Agent 评测的命令，不能是 `stable`（VER-005、VER-006）。
