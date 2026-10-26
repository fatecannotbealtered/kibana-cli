# CLI-SPEC：机器契约

**English → [CLI-SPEC.md](CLI-SPEC.md)**

**规范 2.0.1** · 信封（envelope）`schema_version` 为 `"2.0"` · 机器可读孪生：[`contract/contract.json`](../contract/contract.json) · 规则 CLI-001 至 CLI-110

本文是 AI 原生 CLI 与调用它的 Agent 之间的契约：工具输出什么、怎样失败、命令如何组织和查找、怎样报告读取结果、怎样执行写操作，以及怎样更新自身、读取设置、访问网络。它取代 CLI-SPEC 1.6.3；1.6.x 工具的维护者请按 ai-native-cli-spec 仓库中的 `MIGRATION-2.0.md` 迁移。本目录中的配套规范：[SEC-SPEC_zh.md](SEC-SPEC_zh.md)（安全，`SEC-xxx`）、[SKILL-SPEC_zh.md](SKILL-SPEC_zh.md)（Skill，`SKL-xxx`）和 [VERIFY-SPEC_zh.md](VERIFY-SPEC_zh.md)（验证与 Agent 评测，`VER-xxx`）。[AGENT_zh.md](AGENT_zh.md) 是工作手册。

## 如何阅读本规范

- 「必须」「不得」「应当」「可以」分别对应 RFC 2119 中 MUST、MUST NOT、SHOULD、MAY 的含义。
- 每条规则都有稳定的 ID 和标题，然后是要求本身、`Why:`（原因）和 `Check:`（检查）；ID 永不重新编号，各组之间的编号空档是有意留出的。如果 aicli 会运行某项符合性检查，`Check:` 就写出它的编号：`RunConformance` 对每个写命令运行 `G00`–`G10`，对每个工具运行一次 `R01`–`R05`（VER-001）；`R06` 则单独由 `CheckCoverage` 运行（VER-003）。
- 错误码、退出码、枚举和键集以 `contract.json` 为准。本文与契约不一致时以契约为准，本文的写法视为缺陷。
- 证据来自 2026-10 的 gitlab-cli Agent 评测（5 个实验组共 720 次试验，3 个模型，开启和关闭 Skill 两种条件），以及对工具群（fleet）中 11 个工具的审计；其中 10 个工具带确认门控。文中数字引自该报告，以及收录其证据的 v2 原则草案。
- 示例使用一个类似 GitLab 的工具，名为 `tool`，状态目录为 `~/.tool`，环境变量前缀为 `TOOL_`。JSON 示例都是合法的 JSON；较长的示例为便于阅读做了折行。

## 调用方（资料性）

主要调用方是通过 Claude Code 这类运行框架（harness）工作的语言模型。

- **每次调用都是一次往返。** 模型写出命令、等待、读取结果；每次往返都要耗费以秒计的模型时间。启动时间还要叠加上去：在一台 Windows 测试机上，经 npm 启动器每次调用耗时 1.1–2.3 s，单独运行二进制则为 0.4–0.7 s。
- **输出要经过一个窗口。** Claude Code 内联显示成功结果，上限约 30,000 个字符，超出后只给出 2,000 个字符的预览和一个文件路径。失败结果最多显示约 10,000 个字符；再长就只保留开头和结尾，而且不会保存。命令默认超时 2 分钟，模型也能看到 stderr。
- **模型读的是文本。** 它沿用见过的惯例（`--help`、`<noun> <verb>`、gh 和 glab 的标志），会读错误信息，会从先前的输出里复制标识符。它数长列表会数错，还会编造没人给过它的标识符。

**准确性分三层**，契约对每一层都有支撑：

1. 调用准确：调用第一次就有效，或者一步就能改对（CLI-012、CLI-022、CLI-048）。
2. 报告准确：工具报告的一切都确实观察到了，而且属实。未知就保持未知，部分生效绝不凑整为成功，不完整的列表会说明自己不完整（CLI-015、CLI-041、CLI-059）。
3. 回答准确：Agent 的最终回答与证据相符；在工具内计算而不是交出原始列表（CLI-044）、标出外部文本（CLI-046），都有助于做到这一点。

**速度有三项成本**：往返次数（CLI-034、CLI-049、CLI-052）、每次往返的 token 数（CLI-003、CLI-040、CLI-042），以及每次调用的额外开销（CLI-090）。

**授权来自人。** 由人授予授权范围文件（scope file），工具负责强制执行（SEC-010）。预览（preview）及其确认令牌（confirm token）把 Agent 自己的计划绑定到实际运行的请求上；它们不是批准。

## aicli 提供什么

> aicli 是本规范模板中的共享库：一个扁平的 Go 包（`template/go/aicli`，以副本方式引入到 `internal/aicli/`）和一个 Python 包（`template/python/aicli`，以副本方式引入到 `<pkg>/_aicli/`）。`scripts/sync-spec.js` 按规范的 tag 钉住版本复制它，副本一旦漂移，`scripts/check-spec.js` 就让 CI 失败。不要在工具仓库里修改它。
>
> | 规则 | aicli 负责 | 工具仍需负责 |
> |---|---|---|
> | 输出：CLI-001–006、046、047 | 唯一的信封输出器（panic 转为 `E_UNKNOWN`）；紧凑 JSON 和 `--pretty`；`--fields` 投影；`meta`；格式检查和 `OutputFormat`；安静的 stderr；通知；NDJSON 流输出器（CLI-006） | 声明 `raw` 命令和流式命令；指明外部文本的路径 |
> | 错误：CLI-010–018 | 契约的错误码表；每种 details 形状的构造函数；信息充实的用法错误；`FromHTTP`；在 `App.ExtCodes`（`CodeSpec`）中声明的扩展错误码，负责检查并公布 | 写给模型看的错误信息（CLI-017）；自身错误上的 `next_action`；在 403 错误里给出角色；`contract-ext.json` |
> | 命令模型：CLI-020–029 | 注册表（registry，即 cobra 或 click 命令树加上 `aicli.Meta`，用 `Meta.Params`（`ParamDoc`）声明类型、枚举和单位）；命令树检查；先匹配路径的解析；位置参数；接受的猜测写法（`AddShortcut`、`ShortFlags`、`FlagAliases`、`Meta.FlagAliases`、`Meta.Aliases`）、给出建议的猜测写法（`FlagSynonyms`、`VerbSynonyms`，以及共享标志名的默认同义词）和全树范围的建议；Skill 的命令地图（command map，`App.CommandMap`） | 如实的 `Meta`：效果、输出 schema、错误码、示例、成熟度（maturity）、证据；自己的猜测清单；共享的标志名和动词含义（CLI-028、CLI-029） |
> | 发现：CLI-030–037 | `--help`、`reference`（完整版、`--index`、`--command`）、`context`（工具声明了设置时带 `profile`）、`doctor`（其 `version` 检查从 `App.SkillDirs` 读取已安装的 Skill；工具声明了设置时有 `settings` 检查）、`changelog` | `Identity`、`DoctorChecks` 和 `ContextExtra` 钩子；内嵌的 `CHANGELOG.md` |
> | 读取：CLI-040–049 | 投影和 `WantsField`；带完整性规则的 `Page`；`Ambiguous`；文件和 stdin 输入；`ParseTime` 和 `ParseDuration`；经过写入门控写出的 `--output <file>` 文件（`WriteOutput`、`OpenOutput`，CLI-042） | 默认字段、如实的 `scope`、服务端过滤、ref、URL 和名称、响应大小（CLI-040–045）；`--input`（CLI-048）；多 ref 读取和 `--wait`（CLI-049） |
> | 写操作：CLI-050–065 | 整个写入门控（write gate）：绑定（`SetResolved` 绑定从其他形式解析出的值）、令牌、密钥、操作记录、授权范围检查、回执（receipt）和 `ok`、批量操作（经由 `ApplyAll` 调用原生批量接口）、`operation show/list/resolve`、`HumanRequired` | `Target`，需要查询才能确定目标时改用 `ResolveTarget`；只读、确定性、带基准版本的 `Preview`；如实分类失败并回读所改内容的 `Apply`；长时间运行的任务（CLI-064） |
> | 自更新：CLI-070、071 | 用 `App.Update`（`UpdateConfig`）启用的内置命令 `update`：发现发布、Sigstore 签名和校验和校验、原子替换或运行包管理器、Skill 同步、分阶段的错误；`RecordUpdateCheck`、`meta.notices` 中的缓存通知，以及 `doctor` 的刷新 | 一个写明仓库和包名的 `UpdateConfig`；在二进制旁边带上 `skills/` 的发布归档（REPO-043） |
> | 版本：CLI-080、081 | `reference` 中的 `spec_version` 和 `schema_version`；依据 `Meta.Deprecated`、`Meta.DeprecatedFlags` 和 `Meta.RenamedFlags` 处理弃用：`Deprecated:` 前缀、`deprecated` 通知、不含已弃用命令的命令地图 | 弃用什么以及用什么替代；`CHANGELOG.md` 中的条目 |
> | 设置与诊断：CLI-100–102 | `App.Settings` 和 `App.Setting`：设置文件、配置档和 `--profile`；`App.Debugf` 和 `<PREFIX>_DEBUG` | 声明的设置；`auth` 命令（CLI-101） |
> | 网络：CLI-110 | 自身请求（`update`、`doctor` 的刷新）遵守的规则 | 上游客户端：超时、只重试读取、代理、TLS、User-Agent |
> | 验证 | `RunConformance`（`G00`–`G10`、`R01`–`R05`）、`CheckCoverage`（`R06`，从 `TestMain` 运行）、`ValidateEnvelope` | 每个写命令一个符合性样例；命令级测试；Agent 评测（VERIFY-SPEC） |

## 输出

### CLI-001 stdout 上只有一个 JSON 文档

在 `json` 模式下，命令必须向 stdout 写出恰好一个 JSON 文档，即信封（CLI-002），后跟 `\n`；只有声明为流的命令使用 NDJSON（CLI-006），`--help` 输出文本（CLI-030）。进度、警告和诊断信息写到 stderr；stdout 不带横幅、交互提示、进度条或颜色码。在所有平台上，两个流都是不带 BOM 的 UTF-8，换行符为 `\n`。stdin 不是终端时，任何命令都不弹出交互提示。

原因：Agent 解析 stdout 并检查 `ok`。多出一个字节，有效的结果就变成解析错误；一个交互提示会让调用一直挂起，直到运行框架把它杀掉。

检查：aicli 通过同一个输出器写出每个信封，panic 也不例外；`R04` 校验测试套件产生的每个信封。

### CLI-002 信封

成功信封必须恰好包含 `ok`、`schema_version`、`data`、`meta` 这几个键；错误信封恰好包含 `ok`、`schema_version`、`error`、`meta`。`schema_version` 是字符串 `"2.0"`。载荷放在 `data` 中，绝不放在顶层；失败的响应绝不带 `data`。

```json
{"ok":true,"schema_version":"2.0","data":{"ref":"acme/web!5","title":"Add retry","state":"opened","url":"https://gitlab.example.com/acme/web/-/merge_requests/5"},"meta":{"duration_ms":12,"untrusted":["data.title"]}}
```

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_NOT_FOUND","message":"No such merge request in this project, or this account cannot see it.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Run `tool mr list --project acme/web` to find the right one."}},"meta":{"duration_ms":3}}
```

原因：Agent 在每个工具里都用同样的方式检查 `ok`、读取 `data` 或 `error`；键集固定，校验器就能机械地发现漂移。

检查：`R04`（对每个信封运行 `ValidateEnvelope`）。

### CLI-003 默认输出紧凑 JSON

JSON 输出默认必须是紧凑的：不缩进，记号之间不留空格。`--pretty` 为人阅读加上缩进。工具仍接受 `--compact`，但它不起作用，这样旧的 Skill 和脚本照常可用。

原因：一个 50 项的列表，缩进时约 14,000 个字符，紧凑时约 10,000 个。评测没能显示出收益，因为 Agent 本来就在 74–96% 的调用中传了 `--compact`；但不知道这个标志的 Agent 仍要承担缩进的开销。

检查：aicli 的输出器。

### CLI-004 meta

每个信封都必须带 `meta.duration_ms`（整数毫秒），成功和失败都一样。其他键只有 `notices`（CLI-047、CLI-071）和 `untrusted`（CLI-046），为空时各自省略。

原因：Agent 和评测从 `duration_ms` 读出一次调用的开销；`meta` 的键集保持封闭，它才不会变成第二个没有文档的载荷。

检查：`R04`。

### CLI-005 输出格式

`--format` 必须接受 `json`（默认格式，也是给 Agent 的唯一格式）和 `text`（给人看；可能变化，绝不拿来解析），还要接受 `raw`（仅限声明了这种格式的命令，`Meta.RawOutput`）：日志、diff 和文件内容按字节原样透传。`--json` 是 `--format json` 的别名。命令不支持的格式以 `E_USAGE`、`problem: unsupported_format` 失败，并以 JSON 输出。`raw` 命令如果在写出任何输出之前失败，就输出 JSON 错误信封。raw 形式与 JSON 形式不同的命令（文件的字节与描述这个文件的对象），用 `aicli.OutputFormat(cmd)`（Python 为 `output_format(ctx)`）询问本次运行用的是哪种格式。格式永远不改变退出码，也不改变写入门控（CLI-055）。

原因：评测中，对只支持 JSON 的命令使用 `--format raw`，得到的是 Agent 无法解析的纯文本错误；正是这类摩擦让「指派 MR 并打标签」（T07）代价高昂。

检查：aicli 拒绝未声明的格式；`G06` 用 `--format text` 运行每个写命令。

### CLI-006 NDJSON 流

输出是流的命令（跟踪日志、事件订阅、长时间导出）可以输出 NDJSON。此时每一行都必须是完整的信封，外加一个键 `type`，取值为 `item` 或 `summary`；最后一行必须是 `summary`：要么是 `ok: true`，带上这个流覆盖了什么（计数；提前停止时还要带 CLI-041 的完整性键），要么是 `ok: false`，带上 `error`。退出码与汇总行一致；没有汇总行就结束的流是不完整的。命令的简介和示例要说明它以流的形式输出。aicli 提供流输出器，负责输出条目行和收尾的汇总行。在 `--format raw` 下，流只写出字节，没有信封，也没有汇总行，由退出码说明它是怎样结束的。条目行列出的不可信路径来自条目自己的 schema，而不是命令的输出 schema。

```jsonl
{"ok":true,"schema_version":"2.0","type":"item","data":{"time":"2026-10-12T08:00:01Z","line":"Running tests"},"meta":{"duration_ms":40,"untrusted":["data.line"]}}
{"ok":true,"schema_version":"2.0","type":"summary","data":{"count":1,"has_more":false},"meta":{"duration_ms":41}}
```

原因：流让 Agent 可以提前停下，内存占用也保持平稳；汇总行能区分正常结束的流和被杀掉的流。在 gitlab-cli 试点（2026-10）中，`job log --follow` 的每一行都列出了作业对象的不可信字段，raw 输出的各行之后还跟着一行管道用不上的 JSON 汇总。

检查：人工评审；工具的流测试逐行解析。

### CLI-007 命名与取值

所有地方的键都必须是 snake_case，`data` 和 `details` 里也一样。ID 一律是字符串，即使上游用的是数字。时间采用带 `Z` 后缀的 ISO 8601 UTC 格式（`2026-10-12T08:00:00Z`），以 `_at` 结尾的键只放这样的时间；不带时刻的日期放在以 `_on` 结尾的键下（`expires_on`）。枚举值用小写 snake_case，除非 contract.json 另有规定。数量把单位写在键名里（`duration_ms`、`retry_after_ms`），或写在参数的 `unit` 里（CLI-024）。

原因：Agent 会在工具之间、调用之间复制键和值；gitlab-cli 1.2.17 返回的是 `hasMore`，而工具群里的其他工具都用 `has_more`。GitLab 会把一些日期放在 `_at` 键下（成员的 `expires_at` 为 `2026-12-31`），gitlab-cli 试点（2026-10）把这些键改了名。

检查：`R04` 检查键的大小写，以及 `_at` 键的值是否为 UTC 时间；其他取值靠人工评审。

## 错误

### CLI-010 错误对象与错误码表

`error` 必须恰好包含 `code`、`message`、`retryable`、`details` 这几个键。退出码和 `retryable` 按下表由 `code` 决定（contract.json `error_codes.core`），对于核心错误码，工具不得改动它们。工具专有的错误码只能来自 `contract-ext.json`（CLI-018）。`retryable: true` 表示什么都没有改变，所以退避之后可以再试一次：这次调用是读操作，或者是工具确知没有生效的写操作（CLI-015）。重试写操作要从 `--dry-run` 重新开始（CLI-055）。

| 错误码 | 退出码 | 可重试 | 含义 |
|---|---|---|---|
| `E_USAGE` | 2 | false | 命令行有误：未知命令或标志、意外的参数、缺少参数或参数无效。 |
| `E_VALIDATION` | 2 | false | 请求格式正确，但上游或工具拒绝了其内容。 |
| `E_AMBIGUOUS` | 2 | false | 一个名称匹配到多个资源；从 `details.candidates` 中选一个。 |
| `E_UNSUPPORTED` | 2 | false | 请求有效，但当前构建、上游版本或模式无法执行。 |
| `E_NOT_FOUND` | 3 | false | 资源不存在，或当前账号看不到。 |
| `E_AUTH` | 4 | false | 凭证缺失、无效或已过期。 |
| `E_FORBIDDEN` | 4 | false | 不允许：没有已授予的授权范围（`details.scope`），或上游拒绝了该账号（`details.required`）。 |
| `E_CONFIG` | 4 | false | 本地配置缺失或无效。 |
| `E_CONFIRMATION_REQUIRED` | 5 | false | 写操作需要先 `--dry-run` 再 `--confirm <token>`，或者需要 `--dangerous`；什么都没有改动。 |
| `E_CONFLICT` | 6 | false | 前置条件不满足（`details.conflict`）。 |
| `E_OUTCOME_UNKNOWN` | 6 | false | 改动可能已经生效；先用 `details.lookup` 查清楚，再做别的。 |
| `E_NETWORK` | 7 | true | 连不上上游；什么都没发出。 |
| `E_RATE_LIMITED` | 7 | true | 触发了上游限流；等待 `details.retry_after_ms`。 |
| `E_SERVER` | 7 | true | 上游在处理请求时出错，且什么都没有改变：这是一次读操作，或者是工具确知没有生效的写操作。可以安全重试；写操作要从 `--dry-run` 重新开始。 |
| `E_TIMEOUT` | 8 | true | 读请求超时。 |
| `E_HUMAN_REQUIRED` | 9 | false | 需要人先操作（`details.action`），然后运行 `details.resume`。 |
| `E_PARTIAL` | 1 | false | 改动部分生效；读取回执或各条目，绝不整体重发请求。 |
| `E_VERIFY_FAILED` | 1 | false | 改动已生效，但回读结果与请求不符。 |
| `E_VERIFY_INCOMPLETE` | 1 | false | 改动已生效，但必需的回读没能完成。 |
| `E_INTEGRITY` | 1 | false | 发布签名或校验和验证失败；不要重试。 |
| `E_IO` | 1 | false | 本地文件或工具的状态存储出错；什么都没发出。 |
| `E_INTERRUPTED` | 130 | true | 在产生任何持久影响之前被信号中断。 |
| `E_UNKNOWN` | 1 | false | 内部错误；读取 `message` 和 `details`。 |

| 退出码 | 含义 | Agent 的下一步 |
|---|---|---|
| 0 | 成功 | 继续 |
| 1 | 部分生效、回读校验、完整性、IO 或未知错误 | 读取 `error.details` |
| 2 | 用法、校验、歧义、不支持 | 修正请求；不要原样重试 |
| 3 | 未找到 | 找到正确的目标 |
| 4 | 认证、禁止访问、配置 | 修正凭证、权限或授权范围，或询问用户 |
| 5 | 需要确认 | 使用预览和令牌，或加上 `--dangerous` |
| 6 | 冲突或结果未知 | 重新读取状态，或查询该操作 |
| 7 | 网络、限流、服务端 | 退避后重试 |
| 8 | 超时 | 退避后重试 |
| 9 | 需要人工操作 | 转告 `details.action`，然后运行 `details.resume` |
| 130 | 被信号中断 | 检查状态，然后重试 |

原因：Agent 根据 `code`、退出码和 `retryable` 决定是重试、退避、修正调用还是询问用户。只有这三者在所有工具里都一致，这个决定才能在工具之间通用。

检查：aicli 依据从 contract.json 生成的表构造每个错误；`R04`。

### CLI-011 dispatch_state 与 next_action

每个错误的 `details` 都必须带上：

- `dispatch_state`：是否已发出改变状态的请求。`not_started`：没有发出。所有读操作都属于这种情况，包括上游以 404 或 429 这类错误回应的读操作；写操作在应用（apply）步骤之前的一切也属于这种情况：用法错误和校验错误、有歧义的名称、写入门控或授权范围的拒绝、本地故障。请求离开本机之前就失败的应用步骤（连接失败，`NotDispatched`）也算在内。`dispatched`：写请求已到达上游，且其回应已知；改动是否生效由回执说明。`unknown`：写请求可能已到达上游，但没有观察到回应（CLI-015）。
- `next_action`：用一句话告诉 Agent 下一步做什么；有确切的命令时，把命令放在反引号里。

原因：Agent 总会读错误，所以错误是引导它成本最低的地方；`dispatch_state` 告诉它改动是否可能已到达上游，也就是重试是否可能把改动重复一遍。

检查：`R04`；aicli 在自己抛出的每个错误上都填好这两项（解析、写入门控、授权范围、操作记录）。

### CLI-012 用法错误与校验错误的 details

`E_USAGE` 和 `E_VALIDATION` 必须带 `problem`，取值为 `unknown_command`、`unknown_flag`、`unexpected_argument`、`missing_parameter`、`invalid_value`、`conflicting_flags`、`unsupported_format` 或 `input_too_large`（`confirmation_required` 和 `dangerous_flag_required` 属于 `E_CONFIRMATION_REQUIRED`）。适用时还必须带上以下各项：`parameter`（照输入原样给出的标志，或者那个不是命令的词）；`expected`（可接受的取值或格式）；`did_you_mean`（一个数组，元素是以工具名开头、可直接运行的命令路径，如 `tool mr get`，或者是标志）；`usage`（命令的概要及其标志，见下例）；对于 `unknown_command`，还有 `available`（该组中的命令）。工具能构造出改正后的命令时，`next_action` 应当就是这条命令。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_USAGE","message":"mr list has no such flag; it filters by assignee with --assignee.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Run `tool mr list --project acme/web --assignee carol`.","problem":"unknown_flag","parameter":"--assignee-name","did_you_mean":["--assignee"],"usage":"tool mr list [--project <ref>] [--state opened|merged|closed|all] [--assignee <user>] [--limit <n>]"}},"meta":{"duration_ms":2}}
```

原因：点明改法的错误有帮助，但直接接受猜测的写法帮助更大（CLI-022）。评测中，单靠结构化的用法错误，每次试验的无效调用只从 0.44 降到 0.40（B 组对比 A 组，全部试验）；接受实测到的猜测写法后，从 0.22 降到 0.07（D 组对比 C 组）。

检查：`R03`（未知命令、未知标志、意外的位置参数）。

### CLI-013 冲突的 details

`E_CONFLICT` 必须带 `conflict`，取值为下列之一：

| `conflict` | 含义 | 其他 details |
|---|---|---|
| `token_mismatch` | 令牌不属于这个请求 | `parameter`：第一个不同的输入，以其标志表示（`--assignee`；文件输入在命令有对应的字面值标志时用该标志表示，如 `--body`）；未绑定到标志的位置参数用 `args`；换了命令或构建用 `command` 或 `tool_version`；上下文变化用 `--idempotency-key`、`account`、`host` 或 `target`；令牌格式错误或未知用 `--confirm` |
| `token_expired` | 令牌已超过其 `expires_at` | |
| `token_used` | 令牌已被消费 | |
| `state_drift` | 自预演（dry-run）以来目标已变化：基准版本或重新运行的预览不同（CLI-057） | |
| `scope_changed` | 自预演以来授权范围文件已变化 | |
| `unresolved_operation` | 该目标上先前的某个操作处于 `unknown` 或仍在 `dispatching` | `active_operation` |
| `lock_held` | 该操作的租约仍然有效（`operation resolve`） | `active_operation` |
| `upstream_conflict` | 上游因其当前状态而拒绝（HTTP 409 或 412） | `http_status` |
| `idempotency_mismatch` | 该幂等键（idempotency key）已用于另一个请求 | |

`active_operation` 是造成阻塞的那个操作的 ID；`operation show` 可以查看它。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_CONFLICT","message":"The confirm token was issued for different inputs; nothing was sent.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Repeat the previewed command exactly, or run it with --dry-run to get a token for the new inputs.","conflict":"token_mismatch","parameter":"--assignee"}},"meta":{"duration_ms":5}}
```

原因：评测中，有一次不匹配附带的是笼统的提示（「可能有另一处改动同时发生」），对它的实际情况并不适用：那其实是一次 `--body` 与 `--body-file` 之间的确认。点出是哪个输入，就不必再猜了。

检查：`G03`（不匹配时点出参数）；写入门控的所有冲突错误都由 aicli 构造。

### CLI-014 禁止访问的 details

本地授权范围（SEC-010）的拒绝必须带 `details.scope`：`status` 和 `path`，已知时还有 `scope_id`、`missing` 和 `grant_example`（格式见 SEC-011）；能构造出预览时还要带 `details.preview`。上游返回 HTTP 403 时，如果工具知道该账号缺什么，应当带 `details.required`：一个角色名（`"maintainer"`），或者在也知道调用方角色时给出 `{"role": …, "current_role": …}`。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_FORBIDDEN","message":"No granted scope allows this change; a person must grant one first.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Ask the user to grant this scope by creating C:\\Users\\me\\.tool\\scope.json with the grant_example content (adjusted); do not create it yourself.","scope":{"status":"missing","path":"C:\\Users\\me\\.tool\\scope.json","scope_id":null,"missing":{"effects":["change"],"dangerous":false,"target":"acme/web!5"},"grant_example":{"scope_id":"scp_2026-10-12_mr","tool":"tool","granted_by":"<your name>","granted_at":"2026-10-12T08:00:00Z","expires_at":"2026-10-19T08:00:00Z","effects":["change"],"dangerous":false,"targets":[{"key":"acme/web"}]}}}},"meta":{"duration_ms":6}}
```

应用写操作时遇到上游 403，其 `details` 如下（省略了其中的 `receipt`，CLI-059）：

```json
{"dispatch_state":"dispatched","next_action":"Tell the user that creating CI/CD variables needs the maintainer role; this account is a developer.","required":{"role":"maintainer","current_role":"developer"},"http_status":403}
```

原因：在评测的禁止写入任务（T10）中，由于错误没有点出角色，Agent 在 403 之后又花了 2–4 次调用，用 `auth status`、`project get` 和 `variable list` 去探查原因；任务也变得更贵（从 6.2 次调用增加到 6.8 次）。

检查：`G07`（没有授权范围时，预演和确认都返回带 `details.scope` 的 `E_FORBIDDEN`，且什么都不发出）。

### CLI-015 派发后结果未知

当一个改动请求可能已到达上游、而工具没有观察到结果时（超时、连接断开、5xx、调用过程中被中断），错误必须是 `E_OUTCOME_UNKNOWN`（退出码 6，不可重试），并带上 `operation_id` 和 `lookup`；后者是查看该操作的确切命令。它绝不是 `E_NETWORK`、`E_SERVER`、`E_TIMEOUT` 或 `E_INTERRUPTED`：这些错误码表示什么都没有改变。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_OUTCOME_UNKNOWN","message":"The update was sent but no reply arrived; it may have been applied.","retryable":false,"details":{"dispatch_state":"unknown","next_action":"Run `tool operation show --id op_4hx2mq7tz3b5kd6wqe2nrc7vya` and re-read the merge request before anything else; do not resend the update.","operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya","lookup":"tool operation show --id op_4hx2mq7tz3b5kd6wqe2nrc7vya"}},"meta":{"duration_ms":30012}}
```

原因：1.6.3 即使在写请求已发出之后，也把 `E_NETWORK` 和 `E_TIMEOUT` 标为可重试，于是遵守契约的 Agent 可能把同一条评论发两次，或把同一条消息发两次。

检查：凡是工具没有用 `NotDispatched` 或 `Rejected` 包装的 `Apply` 错误，aicli 都报告为结果未知；工具的回应丢失测试（VER-004）。

### CLI-016 上游 HTTP 映射

上游故障必须在同一个函数里映射，依据 HTTP 状态码或上游的错误类型，绝不依据消息文本。aicli 的 `FromHTTP(status, message, upstream, retryAfter)` 实现了下表：

| 上游回应 | 错误码 | details |
|---|---|---|
| 400、422 | `E_VALIDATION` | `problem: invalid_value` |
| 401 | `E_AUTH` | |
| 403 | `E_FORBIDDEN` | 已知时带 `required`（CLI-014） |
| 404、410 | `E_NOT_FOUND` | |
| 408 | `E_TIMEOUT` | |
| 409、412 | `E_CONFLICT` | `conflict: upstream_conflict` |
| 413 | `E_VALIDATION` | `problem: input_too_large` |
| 429 | `E_RATE_LIMITED` | `retry_after_ms` 取自 `Retry-After` |
| 其他 4xx | `E_VALIDATION` | `problem: invalid_value` |
| 5xx | `E_SERVER` | |
| 发送前连接失败（DNS、拒绝连接、TLS） | `E_NETWORK` | |

上游的错误文本放进 `details.upstream_message`，并列入 `meta.untrusted`；状态码放进 `details.http_status`。`FromHTTP` 设置 `dispatch_state: not_started`，因为读操作不发送改变状态的请求（CLI-011）。在应用写操作期间，这张表只适用于能证明什么都没改变的回应，即 4xx（包括 429）：工具用 `Rejected` 包装该错误，写入门控连同回执一起报告 `dispatched`（CLI-057 第 14 步）。发送之后出现的 5xx、超时或连接断开属于结果未知（CLI-015）。写操作的应用步骤只在工具确知什么都没有生效时才报告 `E_SERVER`（回执规则第 4 条，CLI-059）。

原因：按消息文本匹配，会把只是含有「not found」之类字眼的消息分错类；只用一个函数，状态码、错误码和退出码就不会各自漂移。

检查：aicli 的 `FromHTTP` 测试；工具的上游故障测试（VER-004）。

### CLI-017 写给模型看的错误信息

`message` 必须是一句话，最多两句，用平实的英语点明违反了哪条约束、下一步做什么。它不得回显输入值（其中可能含有机密或注入的文本）、堆栈跟踪或上游文本；上游文本放进 `details.upstream_message`。工具写给 Agent 看的内容，即 `message`、`next_action`、通知和帮助，无论区域设置（locale）如何都用英语；上游内容（标题、正文、上游错误文本）原样透传，不做翻译。

原因：1.6.3 让 Agent 不要读 `message`，转而让每个 Skill 复制一棵错误决策树。可 Agent 照样会读这个字段，所以它必须写对。只用一种语言，Skill、评测的评分者和 Agent 自己的笔记在每个工具里都能对上同样的词；上游文本原样透传，作者写了什么就保留什么。

检查：人工评审；`R04` 把每个错误限制在 2,000 个字符以内（CLI-042）。

### CLI-018 扩展错误码

工具专有的错误码必须在 `contract-ext.json` 中声明：名称匹配 `^E_[A-Z0-9][A-Z0-9_]*$`，且不与核心错误码重名；声明 `exit`、`retryable` 和 `meaning`；其退出码是文档列出的退出码之一，退出码 9 只留给需要等人操作的错误码；其 details 带 `dispatch_state` 和 `next_action`。宁可用核心错误码配上精确的 details，也不要新增错误码。已知的扩展错误码：`E_2FA_REQUIRED`（退出码 9，archery-cli）。使用 aicli 时，`contract-ext.json` 中的每个错误码还要在 `App.ExtCodes` 中声明为 `CodeSpec{Exit, Retryable, Meaning}`；命令树检查会拒绝违反上述规则的错误码，`reference` 负责公布它。

原因：核心错误码只有在没有任何工具重新定义它们时，才能在整个工具群里通用。

检查：对工具的真实输出做契约校验（VER-002）。

## 命令模型

### CLI-020 唯一的注册表

每个工具都必须有唯一一个注册表来定义它的命令树。注册表为每个命令记录路径、别名、简介、带类型的参数、效果、危险标记、输出 schema、错误码、示例、成熟度和证据。`--help`、`reference`、Skill 的命令地图（`App.CommandMap()`，SKL-002）和覆盖率枚举（VER-003）都从它生成；不在它旁边手工维护任何东西。

使用 aicli 时，注册表就是 cobra（Go）或 click（Python）命令树，加上每个叶子命令一个 `aicli.Meta`，通过 `App.Register` 挂上去。命令组的简介来自 `App.Group`；输出 schema 用 `App.Schema` 声明，同一个 schema 名称注册两次属于缺陷，aicli 会报告它。

```go
app.Register(mrUpdate, aicli.Meta{
    Summary:    "Update a merge request's title, assignees, reviewers or labels",
    Effects:    []aicli.Effect{aicli.Change},
    Maturity:   "verified",
    Evidence:   []string{"simulated:e2e/mr_update_test.go", "live:docs/E2E.md#mr-update"},
    Output:     "merge_request",
    ErrorCodes: []string{"E_NOT_FOUND", "E_FORBIDDEN", "E_VALIDATION"},
    Positional: "mr",
    Examples: []string{
        "tool mr update acme/web!5 --assignee carol --dry-run",
        "tool mr update acme/web!5 --assignee carol --confirm <confirm_token>",
    },
})
```

原因：在 1.6.3 下，每个工具都自创一种 `reference` 结构（有的用 `params`，有的用 `flags` 加 `positionalArgs`），Agent 无法把对一个工具的了解带到另一个工具上，也没有任何东西能发现这种漂移。

检查：`R01`（reference 通过校验）；`R02`（每个叶子命令都有简介、输出 schema 和示例）；`R06`（`CheckCoverage`，VER-003）。

### CLI-021 命令树形状

命令树必须同时满足以下各项：

- 路径唯一；一个路径要么是命令组，要么是命令，不能两者都是。
- 别名在同级命令之间唯一：`label create` 和 `milestone create` 都可以接受 `add`，但别名不能与同级命令的名称或别名重复，根级别名也不能遮盖根级命令或内置命令。
- 内置名称 `reference`、`context`、`doctor`、`changelog`、`update`、`operation`、`help` 和 `version` 在根级保留。aicli 的命令树检查会拒绝使用这些名称的工具命令，`update` 也不例外：它是 aicli 的内置命令（CLI-070）。
- 任何参数都不遮蔽全局控制标志（`--format`、`--json`、`--pretty`、`--compact`、`--fields`、`--quiet`、`--dry-run`、`--confirm`、`--dangerous`）；保留标志 `--idempotency-key`（CLI-062）、`--allow-downgrade`（CLI-070）和 `--profile`（CLI-101）只表示那几条规则所规定的含义。
- 命令采用 `<noun> <verb>` 形式，先用 `list`、`get`、`search`、`create`、`update` 和 `delete`（含义见 CLI-029），其次才是 `merge`、`approve` 这类领域动词。

新工具应当使用扁平的命令树：用 `branch delete`，而不是 `repo branch delete`。

原因：Agent 会照着从 gh 和 glab 学到的形式输入命令；在每个没有根级快捷方式的评测组里，嵌套名词都是最常见的未知命令猜测。

检查：aicli 的命令树检查；`R02`。

### CLI-022 根级可达与实测的猜测写法

用户会说出的每个名词都必须能从根级到达：只嵌套在一个父级下的名词要有根级快捷方式（`branch` 对应 `repo branch`）。嵌套在多个父级下的名词（`comment` 同时在 `issue` 和 `mr` 之下）不设快捷方式，或者设一个带限定的快捷方式（`issue-comment`），因为一个词不能指向两条路径。应当接受名词的复数别名。有证据表明 Agent 会首先猜的写法，必须被接受，或者以确切的 `did_you_mean` 回应：每个工具维护一份猜测清单，以下面的工具群清单为起点，再根据自己评测中的失败调用扩充。建议必须搜索整个命令树，不能只看输错的那个词的同级节点。

工具群清单，来自 2026-10 的评测：

- 嵌套命令组的根级名词：`repo` 下的 `branch`、`file`、`commit` 和 `tree`；`project members` 对应的 `member` 和 `members`；
- 复数名词：`issues`、`mrs`；
- 动词：`view` 和 `show` 表示 `get`；`note` 和 `notes` 表示 `comment`；`list` 和 `search` 可以互相替代，`create` 和 `add` 也一样；
- 标志：`-p`、`-R` 和 `--repo` 表示 `--project`；对 `--all` 的回应是 `--limit` 加一条提示；在分支命令上，`--branch` 表示 `--name`；
- 位置形式的标识符（`branch delete old-experiment`），以及在以位置参数接收项目的命令上使用的 `--project`；
- 在只支持 JSON 的命令上使用 `--format raw`，返回带 `unsupported_format` 的 `E_USAGE`。

aicli 通过 `App.AddShortcut(word, path...)`（根级名词）、`App.ShortFlags`（`-p`）、`App.FlagAliases`（用 `--repo` 表示 `--project`，对所有命令有效）、`Meta.FlagAliases`（只对一个命令有效的别名，例如用 `--branch` 表示 `--name`）和 `Meta.Aliases`（其他命令名）接受猜测写法，通过 `App.FlagSynonyms` 和 `App.VerbSynonyms` 给出建议；它的未知命令错误和未知标志错误都会搜索整个命令树。`AddShortcut` 拒绝已经指向另一条路径的词；`App.Shortcuts` 中某个词的目标不是已注册的路径时，命令树检查会报告它。

原因：接受这些猜测写法，加上 v3 Skill 的命令地图，把每次试验的无效调用从 0.22 降到 0.07（D 组对比 C 组）；相对同一小时内运行的 gitlab-cli 1.2.17，则是从 0.54 降到 0.07（D 组对比 A′ 组）。关闭 Skill、只有 CLI 发生变化时，无效调用比 C 组少 75%，降到 0.04。在 gitlab-cli 试点（2026-10）中，issue 组和合并请求组各自都需要一个根级 `comment`，哪一组最后注册，这个词就会悄悄归它。

检查：`R02`（只在一个父级下的每个名词都有根级快捷方式，没有一个词被映射两次）；`R03`；工具的测试逐条运行其猜测清单。

### CLI-023 解析

解析器必须先匹配命令路径（处理完别名和快捷方式之后），再只解析该命令声明过的参数。未知标志是 `E_USAGE` `unknown_flag`，意外的位置参数是 `E_USAGE` `unexpected_argument`，两者都绝不忽略。如果一个命令的标识值天然适合以位置参数给出，该命令必须接受位置参数，并把它绑定到声明的标志上（`Meta.Positional`）：`branch delete old-experiment` 和 `branch delete --name old-experiment` 是同一个请求，对确认令牌来说也是（CLI-054）。

原因：gitlab-cli 1.2.17 在 `repo branch delete old-experiment` 中静默忽略了位置参数，然后因为缺少 `--name` 而失败。

检查：`R03`。

### CLI-024 参数

每个参数都必须声明 `type`：`string`、`path`、`integer`、`number`、`boolean`、`json` 或 `enum`。参数可以声明 `enum`（允许的取值）、`unit`（用于数量）、`default`（必填参数上绝不设）、`multiple`（可重复；`string`、`integer` 和 `number` 的值还会按逗号拆分，`path` 和 `json` 的值从不拆分）、`secret`（绝不回显、存储、写入日志或以明文绑定；SEC-021）以及 `file_input: true`（用在 `--<name>-file <path|->` 标志上，其内容作为 `<name>` 绑定，CLI-048）。在 `reference` 中，一个参数表示为 `{name, kind, type, required}`，`kind` 为 `flag` 或 `positional`，再加上上述可选键和 `description`。使用 aicli 时，`type` 跟随该标志的 pflag 或 click 类型，`Meta.Params`（`aicli.ParamDoc{Type, Enum, Unit}`）用来设定 `path`、`json` 或 `enum` 类型、允许的取值（在命令运行前检查）和单位。

原因：参数有了类型，工具就能在发出任何请求之前拒绝错误的值，`reference` 和 `--help` 也能准确描述它。

检查：`R01`。

### CLI-025 效果

每个命令都必须声明它的效果，而且声明必须与命令的实际行为一致：

| 效果 | 含义 |
|---|---|
| `observe` | 只读 |
| `change` | 修改上游服务上的状态 |
| `deliver` | 内容送达目标之外的人或系统：发送、发布、通知、分享 |
| `configure` | 修改权限、设置、凭证、集成或自动化，无论是在上游，还是在工具自己的凭证和配置档（profile）中 |
| `artifact` | 创建新的本地文件 |
| `persist` | 覆盖已有的本地文件 |

带有 `observe` 以外任何效果的命令都是写命令（在 `reference` 中为 `type: write`），要经过写入门控（CLI-050）。`deliver` 和 `configure` 默认让命令成为危险命令；无法撤销的写命令（删除、合并、清除）必须声明为危险（SEC-001）。批量命令只有在它做的事本身危险时才是危险命令，即它的效果危险，或者它的操作无法撤销；作用于很多目标，并不会让它变得危险（`issue bulk close` 不是危险命令，因为每个 issue 都能重新打开）。命令本身不危险时，某一次计划也可能是危险的（一次删除文件的提交）：`Preview.Dangerous` 让这一次运行成为危险操作（CLI-056）。保存在某个资源上的评论是 `change`，即使上游会通知关注这个资源的人；`deliver` 用于要送到某人手上的内容，例如消息、邮件或共享，并且要写明目的地（CLI-051）。工具的 `risk_tier`（风险档）至少要达到其效果所要求的级别：`observe` 为 T0；`change`、`artifact` 和 `persist` 为 T1；`deliver` 和 `configure` 为 T2（SEC-001）。工具自己的操作记录、确认密钥和通知缓存，以及它的自更新（`update`，CLI-070），都不算效果。所以内置命令 `reference`、`context`、`doctor`、`changelog` 和 `operation` 都是读命令；`update` 改变的是工具本身，不经过写入门控，只声明 `observe`：这是规范写明的例外，不是矛盾。

原因：工具群审计发现了不经过任何门控的写操作（wechat-mp 的 `token refresh`；confluence 的 `page attachment download`，写到服务器选定的文件名），还发现了一个经过门控、却被 `reference` 报告为读操作的写操作（kibana 的 `patterns infer --write`）。在 gitlab-cli 试点（2026-10）中，规范、模板和评审 Skill 对批量命令给出了不同的答案；一条会通知关注者的评论，看起来既像 `change` 又像 `deliver`。

检查：`G00`（每个写命令都有符合性样例）；`R02`；效果测试（VER-004）。

### CLI-026 每个命令的错误码

每个命令都必须列出它可能返回的错误码，由 `reference --command` 展示。aicli 公布 `Meta.ErrorCodes`，连同库本身会为该命令抛出的错误码（用法错误；工具声明了设置时，每个工具命令都带 `E_CONFIG`，因为未知的配置档会让其中任何一个命令停下，CLI-101；写命令还有写入门控、授权范围和操作记录的错误）。不可能出现的错误码不列。

原因：Agent 会为可能出现的分支做打算；列出却到不了的错误码，会把它们引上一条并不存在的路。

检查：`R01`。

### CLI-027 成熟度

每个命令都必须声明成熟度：`draft`、`experimental`、`verified` 或 `stable`，并附上 `evidence` 引用：测试文件、录制的运行记录、评测报告。各级别需要什么由 VER-005 规定；来自模拟上游的证据最多让命令达到 `experimental`。这一项取代 1.6.3 的 `release_readiness`，后者已从 `reference` 和 `doctor` 中移除。

原因：工具对自身的断言需要证据，而 1.6.3 自行声明的就绪等级背后没有任何证据。

检查：`R01`；VER-005。

### CLI-028 共享词汇

命令提供以下某个概念时，必须使用标准的标志名，并取这里的含义：

- `--limit <n>`：每页大小。命令的帮助写明它的默认值和最大值；超过最大值返回 `E_USAGE` `invalid_value`，绝不悄悄调低。
- `--cursor <cursor>` 或 `--offset <n>`：下一页从哪里开始，原样复制上一页的 `next_cursor` 或 `next_offset`（CLI-041）。
- `--query <text>`：`search` 命令要查找的内容（CLI-029）。
- `--since <time>` 和 `--until <time>`：时间范围，用 ISO 8601 UTC 或相对时间（`7d`），在 `scope` 中以绝对 UTC 时间回显（CLI-048）。唯一的例外是 `changelog --since <version>`（CLI-037），它的列表按版本排序。
- `--sort <field>` 和 `--order asc|desc`：列表的顺序，在该页的 `sort` 中回显。
- `--state <state>`：按状态过滤；`all` 去掉这个过滤。
- `--refs a,b,c`：一次读取多个资源（CLI-049）。
- `--wait <duration>`：在状态变化或时长用完时返回（CLI-049、CLI-064）。
- `--output <path>`：把结果写入本地文件，只返回汇总（CLI-042）。
- `--input <path|->` 和 `--<param>-file <path|->`：从文件或 stdin 读入全部参数（一个 JSON 对象），或者一个长值（CLI-048）。
- `--profile <name>`：账户配置档（CLI-101）。
- `--idempotency-key <key>`：让创建操作可以安全地重复（CLI-062）。

时长写作 `<n>ms`、`<n>s`、`<n>m`、`<n>h` 或 `<n>d`，用于 `--wait`、相对时间以及任何接收时长的标志；`aicli.ParseDuration` 和 `aicli.ParseTime`（Python 为 `parse_duration`、`parse_time_arg`）解析时长和时间（绝对或相对），所以 `7d` 在每个工具里含义相同。对常见的同义写法，只要命令有对应的标准标志、而工具的 `FlagSynonyms` 没有为这个词设条目，aicli 就会建议标准名称：`--per-page`、`--page-size`、`--max-results` 和 `--max` 对应 `--limit`；`--page-token`、`--next-token` 和 `--next` 对应 `--cursor`；`--search`、`--keyword`、`--keywords` 和 `--term` 对应 `--query`；`--order-by` 和 `--sort-by` 对应 `--sort`。

原因：Agent 会输入它们在别的工具里学到的标志。工具群实测到的首选猜测里，就有用 `-R` 和 `--repo` 表示 `--project`、用 `--all` 表示 `--limit`（CLI-022）。有了统一的词汇，Agent 在一个工具上学到的东西，到下一个工具上直接就是有效的调用，用不着先有猜测清单。

检查：对照这份清单评审每个标志；`R03` 和 aicli 的建议测试。

### CLI-029 动词含义

标准动词（CLI-021）在每个工具里的含义都必须相同：

- `list` 返回一页匹配项（CLI-041）。没有匹配时返回空页，`ok: true`，绝不返回 `E_NOT_FOUND`。
- `get` 按 ref、URL 或名称返回一个资源（CLI-048）。资源不存在时返回 `E_NOT_FOUND`；一个名称匹配到多个资源时返回 `E_AMBIGUOUS`。
- `search` 返回与 `--query` 匹配的一页结果，该页的 `sort` 写明这一页实际的顺序：只有上游按相关度排序时才写 `relevance`，否则写上游实际用的顺序（`created_at_desc`）。工具无法判断是哪一种时（GitLab 只在启用高级搜索时按相关度排序），`sort` 写 `upstream`，由 `scope` 说明这个顺序可能是什么。
- `create` 在与回执并列的 `result` 中返回新资源的 `ref`（CLI-059），并且应当接受 `--idempotency-key`（CLI-062）。
- `update` 只修改给出的字段。字段只有在明确要求时才清空，即给出空值或 `--clear-<field>`，绝不因为漏掉了某个标志而被清空。没有指明任何要修改字段的调用，在进入写入门控之前就返回 `E_USAGE`（`missing_parameter`），所以不会为它签发令牌。
- `delete` 的目标不存在时，在预演阶段报告 `E_NOT_FOUND`，绝不报告成功。
- 领域动词（`merge`、`approve`、`share`）声明自己确切的效果（CLI-025），其简介写明会改变什么。

原因：Agent 会把在一个工具上学到的东西带到下一个工具（CLI-022），所以一个动词在某个工具里换了含义，就会把正确的习惯变成错误的调用或错误的报告。没有匹配就失败的 `list`，或者对一个从未存在过的目标报告成功的 `delete`，会让 Agent 的回答里出现不实的陈述；把没给出的字段统统清空的 `update`，会毁掉用户从未提到的数据。在 gitlab-cli 试点（2026-10）中，`relevance` 在大多数自托管的 GitLab 实例上都不属实，因为它们的基础搜索按从新到旧列出 issue 和合并请求。

检查：人工评审；VER-004 的空结果行和无效输入行。

## 发现

### CLI-030 帮助

`<path> --help` 必须从注册表生成：简介、用法行、参数（含类型、默认值和允许的取值）、效果，以及示例。根级 `--help` 列出所有命令，每个一行。写命令的帮助要写明它的效果和操作步骤：先用 `--dry-run` 预览（不带它的调用也会返回预览），再带上 `--confirm <token>` 重复同一命令；危险命令的两步都要加上 `--dangerous`。帮助以文本形式输出到 stdout，退出码为 0，是唯一不是信封的输出；`reference --command` 是它的机器可读孪生，内容与它一致。没有 shell 补全命令：aicli 不添加，并关闭 cobra 默认的 `completion` 命令。

原因：没有 Skill 时，每个评测组里的 Agent 每个任务都要调用 3.6–4.4 次帮助，所以帮助是它们的热路径：必须简短，也必须正确。Agent 不使用 shell 补全，所以 `completion` 命令只会让每次根级 `--help` 变长。

检查：aicli 从注册表渲染帮助；`R02`。

### CLI-031 reference --index

`reference --index` 必须为每个命令返回一条紧凑条目 `{path, type, effects, dangerous, maturity, summary}`，放在 `{tool, version, commands}` 中，总长不超过 8,000 个字符。如果索引会超过这个长度，工具改为返回命令组：`{tool, version, groups}`，每组为 `{path, summary, command_count}`；`reference --index <group>` 返回该组的命令。

```json
{"ok":true,"schema_version":"2.0","data":{"tool":"tool","version":"2.0.0","commands":[
  {"path":"mr list","type":"read","effects":["observe"],"dangerous":false,"maturity":"stable","summary":"List merge requests with assignees, labels and pipeline status"},
  {"path":"mr update","type":"write","effects":["change"],"dangerous":false,"maturity":"verified","summary":"Update a merge request's title, assignees, reviewers or labels"},
  {"path":"branch delete","type":"write","effects":["change"],"dangerous":true,"maturity":"verified","summary":"Delete a branch"}]},
 "meta":{"duration_ms":3}}
```

原因：在整个工具群里，完整的 `reference` 有 17,000–113,000 个字符；10 个工具里有 8 个超出 30,000 个字符的内联窗口，于是 Skill 规定的第一步只拿回一段 2,000 个字符的预览。一份索引只需 270–1,300 个 token。

检查：`R01`（合法，且不超过 8,000 个字符或已分组）。

### CLI-032 reference --command

`reference --command <path>` 必须返回 `{tool, version, command, schemas, error_codes}`：该命令的完整条目（`path`、`aliases`、`summary`、`type`、`effects`、`dangerous`、`maturity`、`evidence`、`params`、`output`、`error_codes`、`examples`）、其 `output` 所指的 schema 条目（命令设置了 `Meta.DefaultFields` 时，带的是命令自己的默认字段），以及所列每个错误码的 `{exit, retryable}`，总长不超过 6,000 个字符。它接受别名；未知路径返回带 `did_you_mean` 的 `E_USAGE` `invalid_value`（`parameter: "--command"`）。

```json
{"ok":true,"schema_version":"2.0","data":{"tool":"tool","version":"2.0.0",
 "command":{"path":"branch delete","aliases":["branches delete"],"summary":"Delete a branch","type":"write","effects":["change"],"dangerous":true,"maturity":"verified","evidence":["simulated:e2e/branch_test.go","live:docs/E2E.md#branch-delete"],
  "params":[{"name":"project","kind":"flag","type":"string","required":true,"description":"Project ref, for example acme/web"},{"name":"name","kind":"positional","type":"string","required":true,"description":"Branch to delete; --name also works"}],
  "output":"branch_deleted","error_codes":["E_USAGE","E_AUTH","E_FORBIDDEN","E_NOT_FOUND","E_CONFIRMATION_REQUIRED","E_CONFLICT","E_OUTCOME_UNKNOWN","E_NETWORK","E_RATE_LIMITED","E_IO"],
  "examples":["tool branch delete old-experiment --project acme/web --dangerous --dry-run","tool branch delete old-experiment --project acme/web --dangerous --confirm <confirm_token>"]},
 "schemas":{"branch_deleted":{"shape":"object","fields":["operation_id","receipt","result"],"default_fields":["operation_id","receipt","result"],"untrusted_fields":[]}},
 "error_codes":{"E_USAGE":{"exit":2,"retryable":false},"E_AUTH":{"exit":4,"retryable":false},"E_FORBIDDEN":{"exit":4,"retryable":false},"E_NOT_FOUND":{"exit":3,"retryable":false},"E_CONFIRMATION_REQUIRED":{"exit":5,"retryable":false},
  "E_CONFLICT":{"exit":6,"retryable":false},"E_OUTCOME_UNKNOWN":{"exit":6,"retryable":false},"E_NETWORK":{"exit":7,"retryable":true},"E_RATE_LIMITED":{"exit":7,"retryable":true},"E_IO":{"exit":1,"retryable":false}}},
 "meta":{"duration_ms":2}}
```

原因：查一个命令约花 130–200 个 token，而完整的 `reference` 要 4,000–28,000 个。预算定为 6,000，是因为在 gitlab-cli 试点（2026-10）中，一个有 15 个标志的写命令（`mr create`）需要 4,552 个字符，大部分是它的参数和错误码。

检查：`R01`（合法，且不超过 6,000 个字符）。

### CLI-033 完整的 reference

不带标志的 `reference` 必须返回完整的注册表，供工具链使用（校验器、生成器、Skill 命令地图生成器）：`{tool, version, spec_version, schema_version, risk_tier, global_flags, commands, schemas, error_codes, exit_codes}`。`schemas` 把每个输出 schema 名映射到 `{shape, fields, default_fields, untrusted_fields}`，`shape` 为 `object`、`array` 或 `page`。它不受 8,000 个字符的成功响应预算约束（CLI-042）。Skill 绝不让 Agent 加载它（SKL-003）。

原因：校验器和生成器需要整个注册表；Agent 只需要其中一小部分（CLI-031、CLI-032）。

检查：`R01`。

### CLI-034 没有必经的第一步

每个命令都必须在事先没有调用 `context`、`doctor` 或 `reference` 的情况下正常工作。确实需要其中某一个时（没有凭证、配置损坏、Skill 过时），由错误的 `next_action` 说明是哪一个。

原因：1.2.17 的 Skill 要求先运行 `context`、`doctor` 和 `reference` 做引导，开启 Skill 时每个任务因此要花 1.6 次调用，准确性却没有提高；去掉这一步，再配合 Skill 里的命令地图，才带来了收益。

检查：人工评审；SKL-003 让 Skill 里不出现引导步骤。

### CLI-035 context

`context` 必须只依据本地状态、不发起网络调用，报告以下内容：

| 键 | 内容 |
|---|---|
| `tool`、`version`、`spec_version` | 这是哪个构建 |
| `account`、`host` | 工具以谁的身份、在哪里操作，已去除机密（URL 中不含令牌或密码） |
| `credentials` | `{configured, source}`；绝不包含机密或机密的哈希 |
| `state` | `{dir, available, protection}`；`protection` 为 `posix-0700`、`windows-profile-acl`、`windows-explicit-acl` 或 `windows-unverified`，针对解析后的状态目录计算（SEC-030）；只有存储不可用时，`dir` 和 `protection` 才为 `null` |
| `authorization` | 来自授权范围文件的 `{status, scope_id, path, effects, dangerous, expires_at}`（SEC-011）；`status` 为 `granted`，否则说明原因（`missing`、`invalid`、`expired`、`revoked`、`budget_spent`） |
| `profile` | 仅在工具声明了设置时出现（CLI-100）：`{name, source, profiles, file}`，即当前生效的配置档或 `null`、它的来源（`flag`、`env`、`config`、`single` 或 `none`，CLI-101）、按排序列出的全部配置档名称，以及设置文件的路径（没有该文件时为 `null`）。文件无效时报告 `name: null`、`source: "none"`、`profiles: []` 和它的路径，`context` 仍然成功 |

aicli 依据 `App.Identity`、状态目录、授权范围文件和设置文件构造这些内容；`App.ContextExtra` 添加工具专有的键。

```json
{"ok":true,"schema_version":"2.0","data":{"tool":"tool","version":"2.0.0","spec_version":"2.0.0","account":"ci-bot","host":"gitlab.example.com",
 "credentials":{"configured":true,"source":"keyring"},
 "state":{"dir":"C:\\Users\\me\\.tool","available":true,"protection":"windows-profile-acl"},
 "authorization":{"status":"granted","scope_id":"scp_2026-10-12_triage","path":"C:\\Users\\me\\.tool\\scope.json","effects":["change"],"dangerous":false,"expires_at":"2026-10-19T08:00:00Z"}},
 "meta":{"duration_ms":4}}
```

原因：Agent 不需要一次网络往返，就能知道自己以谁的身份操作、可以做什么。

检查：`R05`。

### CLI-036 doctor

`doctor` 必须返回 `data.checks`，每项为 `{check, status, message, fix}`，`status` 取 `pass`、`warn` 或 `fail`，检查通过时 `fix` 为 `null`。它必须包含 `credentials`、`connectivity`、`state_store`、`authorization` 和 `version` 这几项检查；工具声明了设置时（CLI-100），还要有 `settings`：没有设置文件或文件有效时为 `pass`，并写明当前生效的配置档及其来源；文件无效，或者所要求的配置档不存在（即使没有文件）时为 `fail`，其 `fix` 点出该文件。`version` 把工具与已安装 Skill 的 `metadata.min_version` 和 `metadata.version` 比较，这两项从 `App.SkillDirs` 中每个目录的 `<dir>/<tool>/SKILL.md` 读取（默认目录为 `~/.agents/skills` 和 `~/.claude/skills`）：没有 Skill → `warn`，其 `fix` 指向 README 中的安装命令（`update` 不安装缺失的 Skill）；`min_version` 比工具新 → `fail`（更新工具）；Skill 的 `version` 比工具旧 → `warn`（运行 `update` 同步 Skill）。网络故障只让 `connectivity` 失败。无论各项检查结果如何，`doctor` 都返回 `ok: true`，不做任何改动；设置了 `App.Update` 时，它会在 `version` 检查之前以 3 s 的超时刷新更新通知缓存，失败则忽略（CLI-071）。aicli 运行 `credentials`（依据 `App.Identity`）、`state_store`、`authorization`、`version` 和 `settings`；在工具提供 `connectivity` 检查之前，一律把它报告为 `warn`。来自 `App.DoctorChecks` 的检查会替换库里同名的检查，其他检查则追加进去。

```json
{"ok":true,"schema_version":"2.0","data":{"checks":[
  {"check":"credentials","status":"pass","message":"A token from the keyring is configured.","fix":null},
  {"check":"connectivity","status":"fail","message":"gitlab.example.com did not answer within 5 s.","fix":"Check the VPN or HTTPS_PROXY, then run `tool doctor` again."},
  {"check":"state_store","status":"pass","message":"The state directory is writable and protected by the Windows profile ACL.","fix":null},
  {"check":"authorization","status":"warn","message":"No scope is granted, so every write will be refused.","fix":"Ask the user to grant a scope in C:\\Users\\me\\.tool\\scope.json."},
  {"check":"version","status":"pass","message":"tool 2.0.0 matches its installed Skill (version 2.0.0, min_version 2.0.0).","fix":null}]},
 "meta":{"duration_ms":5012}}
```

原因：Agent 一次调用就能拿到所有问题及其修法，网络不通也不会掩盖授权范围或状态存储的损坏。

检查：`R05`。

### CLI-037 changelog

`changelog [--since <version>]` 必须依据构建时内嵌的 `CHANGELOG.md`（Keep a Changelog 格式）报告变更：`{current_version, entries}`，过滤时再加上 `since` 和 `count`。每个条目为 `{version, date, changes}`，`changes` 以 `added`、`changed`、`fixed`、`deprecated`、`removed` 和 `security` 为键。`--since` 只返回比给定版本更新的版本。不带 `--since` 时，它列出放得进成功响应大小预算（CLI-042）的最新条目；省略了较旧的条目时，加上 `truncated: true`、`truncated_reason` 和 `omitted`。aicli 负责解析 `App.Changelog`。

```json
{"ok":true,"schema_version":"2.0","data":{"current_version":"2.1.0","since":"2.0.0","count":1,"entries":[{"version":"2.1.0","date":"2026-11-02","changes":{"added":["mr list --reviewer filter"],"fixed":["branch delete accepts branch names that contain slashes"]}}]},"meta":{"duration_ms":1}}
```

原因：更新之后，Agent 一次调用就能刷新自己的认知，而不是沿用过时的用法。

检查：aicli 的 changelog 测试；`R04`。

### CLI-038 会过期的凭证

凭证会过期的工具（OAuth 访问令牌、会话、临时密钥），能续期时必须在调用过程中自行续期，不牵扯 Agent。无法续期时，必须返回 `E_AUTH`，由其 `next_action` 点明登录步骤；只有人才能登录时，返回带 `action: sign_in` 的 `E_HUMAN_REQUIRED`（CLI-065）。工具应当在 `context.credentials` 中报告 `expires_at` 和 `refreshable`，其 `doctor` 的 `credentials` 检查应当在过期前给出 `warn`。在调用过程中续期工具自己的会话不算效果；以创建、替换或吊销凭证为职责的命令要声明 `configure`（CLI-025）。刷新令牌属于机密（SEC-021）。

原因：否则，会话过期会在任务进行到一半时表现为一个让人摸不着头脑的失败。1.6.3 把这一条作为可选模式，供 wechat-mp-cli 这类工具使用，这个工具的访问令牌有效期约两小时。

检查：人工评审；工具的认证测试（VER-004）。

## 读取与输出大小

### CLI-040 默认字段

每个列表命令都必须声明默认字段，依据用户会问的问题来选，而不是照搬 API schema：Agent 选择和核对条目所需的字段（`ref`、标题或名称、状态、`updated_at`、`url`）；常见任务会问到的字段（对合并请求而言：指派人、评审人、标签、流水线状态）。`--fields a,b` 替换默认字段，`--fields +a` 在默认字段上追加，`--fields all` 返回全部字段。未知字段返回 `E_USAGE`（`invalid_value`，`parameter: "--fields"`，`expected` 列出可用字段）。投影作用于每个条目、`get` 返回的对象，以及已确认写操作的 `data.result`：没有 `--fields` 时，它像读取结果一样被裁剪到默认字段（绝不作用于回执）；完整性键（CLI-041）始终保留。每个条目都要多一次上游读取的默认字段，在任务需要时可以保留：工具只在响应会显示它时才读取（`aicli.WantsField(cmd, name)`），并行读取，并设一个由 `scope` 写明的上限；不值得这个开销的字段不放进默认字段。投影由 aicli 完成；默认字段由工具选定（单个命令用 `Meta.DefaultFields`，否则用 schema 的 `default_fields`），`--help` 和 `reference` 显示命令自己的默认字段：这时命令的 `output` 指向一份 schema 的副本，名为 `<output>_for_<command words joined by _>`，里面带着这些默认字段。

原因：在 Skill 保持不变的条件下，给 MR 条目加上指派人、标签和流水线状态，并在工具内计算流水线过滤之后，「哪些 MR 的流水线失败了」（T02）从 13.6 次调用降到 6.1 次；「指派 MR 并打标签」（T07）从 13.0 次降到 9.5 次。这个默认字段每个合并请求要多一次 GitLab 读取，因为 GitLab 的合并请求列表不带流水线状态（gitlab-cli 试点，2026-10）。

检查：aicli 的投影测试；对所选默认字段做人工评审。

### CLI-041 完整性

每个列表都必须返回 `items`、`count`（本次响应中的条目数）和 `has_more`；上游报告总数时，应当返回 `total`，即整个列表的条目数，这样问数量的问题就不需要翻页（CLI-044）。`has_more` 为 true 时，`next_cursor`、`next_offset` 和 `continuation_reason` 恰好出现其中一个（`offset` 可以回显当前位置）。被工具截断的列表带 `truncated: true` 和 `truncated_reason`。每个列表都应当说明 `sort` 和 `scope`（contract.json 中的可选键）；`scope` 说明列表覆盖了什么：容器、实际应用的过滤条件和解析后的时间。如果列表的覆盖范围比读者默认的要窄（只有直接成员；在单页上做的客户端过滤），必须在 `scope` 中说明；扇出到多个容器的读取，要在 `scope` 中列出无法读取的容器，绝不静默丢弃。

```json
{"ok":true,"schema_version":"2.0","data":{"items":[{"ref":"@carol","username":"carol","access":"developer","membership":"inherited"}],"count":1,"has_more":false,"scope":{"project":"acme/payments-service","membership":"effective: direct and inherited from groups"},"sort":"username_asc"},"meta":{"duration_ms":41}}
```

原因：gitlab-cli 1.2.17 的 `project members` 只列直接成员，对一个成员全部经由所属组加入的项目，返回了 `items: []` 和 `hasMore: false`。于是有个 Agent 告诉用户，机器人账号「能看到这个项目……但成员列表里没有它」，而这并不属实。条目写的是 `membership: inherited`，而不是成员经由哪个组加入，因为 GitLab 的 REST API 不提供这个组的名字（gitlab-cli 试点，2026-10）。

检查：aicli 的 `Page` 强制执行这些键的规则，`R04` 校验每一页；人工评审检查 `scope` 是否属实。

### CLI-042 大小预算

使用默认标志时，成功响应必须控制在 8,000 个字符以内，每个错误响应控制在 2,000 个字符以内（按整个信封计）。成功响应预算针对默认调用：要求更多内容的调用（`--limit 100`、`--fields all`）可以超出；默认一页就可能超出预算的列表（标题很长、默认字段很多），要调低默认的 `--limit` 或减少默认字段。完整的 `reference`（CLI-033）和 `raw` 输出不受成功响应预算约束（contract.json `size_budgets.exempt`）；`reference --index` 和 `reference --command` 保留各自的预算（CLI-031、CLI-032）。更大的结果改用分页、专门的获取命令（CLI-043），或者用一个命令把结果写到 `--output <file>`，只返回汇总信息和路径。写新的本地文件属于 `artifact` 效果，覆盖已有文件属于 `persist`，所以这样的命令是受写入门控管辖的写命令：新文件的路径必须在授权范围的 `outputs` 之内，已有文件的路径必须在 `roots` 之内（SEC-012）。只声明 `artifact`、没有声明 `persist` 的命令从不覆盖文件：路径已存在时返回 `E_VALIDATION`（`invalid_value`，`details.path`），而不是授权范围拒绝。aicli 提供两种写法：`App.WriteOutput` 让内存中的小结果单独经过写入门控，以文件为目标；以上游资源为目标的 `App.Write`（例如要获取其产物的作业，文件列在 `Preview.Outputs` 中），在它的 `Apply` 里用 `App.OpenOutput(path, overwrite)` 把数据流写进目标旁边的临时文件，由 `Commit` 移到位。两者在写入时都会再检查一次授权范围。

原因：运行框架只把约 30,000 个字符以内的成功结果、约 10,000 个字符以内的失败结果内联显示，超出后 Agent 只能拿到预览，或者丢掉中间部分。一个 50 项的列表，只带三个默认字段约 4,000 个字符，带全部字段并缩进则约 14,000 个。在 gitlab-cli 试点（2026-10）中，一次下载的输出文件已经存在，得到的拒绝却是缺少 `roots` 授予，而这个授予帮不上忙；整个产物归档还必须全部放进内存。

检查：`R01`（reference 的预算）；`R04`（对套件的默认调用检查成功响应预算，对每个信封检查错误响应预算；`ValidateEnvelope` 只检查错误响应预算）；基于测试夹具的响应大小测试（VER-004）。

### CLI-043 重型子资源

diff、日志、文件内容和附件必须由单独的命令提供，绝不内联在列表或默认的 `get` 输出中；列表只带有界的概要（数量、路径、大小）。这些重型命令返回结构化数据（diff 按文件拆成条目，带路径和行数），这样 `--fields` 就能把它们投影成一份清单。单个正文仍可能超出成功响应预算：很长的文件、很大的 diff、贴了日志的描述。这时它的 JSON 形式只带一个有界的窗口：用 `--offset` 和 `--limit` 按行或按文件分页，或者在写明的长度处截断文本，带上 `<field>_truncated: true` 和一条点出获取其余部分的命令的通知。`--format raw`（`Meta.RawOutput`）返回完整的正文；长的文本字段不放进默认字段，需要时用 `--fields +description` 加上。

原因：Agent 只为它选中的那个条目承担重型载荷的开销，而且可以先要一份清单。在 gitlab-cli 试点（2026-10）中，单个文件、单个 diff 和单份发布说明都超过过 8,000 个字符。

检查：人工评审。

### CLI-044 在工具内计算

计数、分组、过滤以及找出最新或排名第一的条目，都必须在工具内完成，能在服务端做就在服务端做；命令绝不把列表交给模型去数。客户端过滤要先翻完整个集合，并在 `scope` 中说明；做不到时，列表标为 `truncated`。上游报告了总数时，列表返回 `total`，这样计数就不需要翻页（CLI-041）；分组计数（谁开的 issue 最多）是一页 `{<key>, count}` 条目，按 `count_desc` 排序，由 `scope` 说明统计了什么；工具没能读完整个集合时，标为 `truncated`。

原因：在「哪些打开状态的 MR 流水线失败了」（T02）中，使用 1.2.17 的 Agent 先列出 MR，再逐个获取。改为在工具内计算 `mr list --pipeline-status failed` 后，这个任务只需 6.1 次调用而不是 13.6 次；Skill 点明这个过滤条件后降到 2.8 次。在 gitlab-cli 试点（2026-10）中，要找出 46 个 issue 里开得最多的作者，Agent 仍然得自己去数一页 46 个条目。

检查：人工评审；Agent 评测（VER-006）。

### CLI-045 富文本用 Markdown

富文本（HTML 邮件、Confluence 存储格式、Jira 的文档格式）应当默认以 Markdown 返回，按需提供原始格式，输入时也接受 Markdown。

原因：模型天然会读写 Markdown；原始标记既费 token，又容易出错。

检查：人工评审。

### CLI-046 不可信内容的路径

响应如果携带外部内容（由人或其他系统写的文本：标题、描述、评论、邮件正文、文件内容、上游错误文本，以及嵌入了这类文本的 `ref`，例如 `acme/web/-/branches/<name>` 中的分支名、标签名或文件名），必须在 `meta.untrusted` 中列出这些内容的位置，每个响应列一次。位置以路径表示：从信封根开始、以点分隔的键，数组的每个元素用 `[]` 表示（`data.items[].title`、`data.description`、`error.details.upstream_message`）。这个列表跟随 `--fields`：保留仍在响应中的字段的路径，去掉被投影删掉的字段的路径。1.6.3 中每个对象上的 `_untrusted` 键已取消。

aicli 从输出 schema 的 `untrusted_fields`、`App.OK(cmd, data, untrusted...)`、`Page.Untrusted` 和 `Error.Untrusted` 收集这些路径，并去掉被投影删掉的字段的路径。Agent 和工具应如何对待这些内容，见 SEC-002。

原因：在每个条目上重复 `_untrusted`，会让一个 50 项列表的响应多出约 16% 的字符；一份路径列表就能表达同样的信息。ref 是 Agent 会复制进下一条命令的东西；在 gitlab-cli 试点（2026-10）中，分支、标签和文件的 ref 都带着人写的文本。

检查：`R04`；Agent 评测中的注入指令场景（VER-006）。

### CLI-047 安静的 stderr，带类型的通知

stderr 不是终端时，工具不得向它写进度条、加载动画、横幅或提示；警告通过 `meta.notices` 传递，每条为 `{type, severity, message}`，`severity` 取 `info` 或 `warning`。`--quiet` 抑制 stderr 上所有非错误输出。`update_available` 通知（CLI-071）只从本地缓存读取，每天最多附加一次；每次使用已弃用的命令或标志，都会附带一条 `deprecated` 通知（CLI-081）。

原因：运行框架会把 stderr 展示给模型，所以每一行多余输出在每次调用时都要消耗 token；带类型的通知，Agent 可以照着处理，也可以忽略。

检查：aicli 的输出器和通知测试。

### CLI-048 接受 Agent 手上已有的东西

- 输出中的每个资源都必须带一个 `ref` 字符串（`acme/web!5`、`acme/web#12`），每个接收该资源的命令都必须接受这个 ref，也必须接受该资源的网页 URL。
- 子资源的 ref 是父资源的 ref，再接上集合名和它的 ID：`acme/web#12/notes/99`、`acme/web!5/discussions/<id>`，这样对父资源的授予就能覆盖它（SEC-012）。网页 URL 及其锚点（`…/-/issues/12#note_99`）仍然作为输入接受。
- 在名称是自然输入的地方（项目、用户、看板），有歧义的名称必须返回 `E_AMBIGUOUS`（退出码 2），附带 `candidates`，每项为 `{ref, label}`，绝不静默挑一个。
- 应当接受相对时间（`7d`、`24h`），并在 `scope` 中以绝对 UTC 时间回显；`--input -` 或 `--input <file>` 应当以一个 JSON 对象接收全部参数；长文本应当通过 `--<param>-file <path|->`（`Meta.FileInputs`）传入，按内容绑定（CLI-054）。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_AMBIGUOUS","message":"More than one project matches that name; choose one by ref.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Ask the user which project they mean, then re-run with its ref.","candidates":[{"ref":"acme/payments-service","label":"acme / payments-service"},{"ref":"legacy/payments","label":"legacy / payments"}]}},"meta":{"duration_ms":120,"untrusted":["error.details.candidates[].label"]}}
```

aicli 提供 `Ambiguous` 构造函数、文件和 stdin 输入（`ReadFileInput`）以及位置参数绑定；解析 ref、URL 和名称，以及 `--input`，由工具自己的代码实现（解析出的值用 `SetResolved` 绑定，每个输入只绑定它自己那一部分，CLI-054）。

原因：Agent 会从先前的输出、用户的消息和 URL 中复制标识符；命令要求一个它们手上没有的 ID 时，它们就会编一个。评测中的歧义项目任务（T04）特意设置了两个名为「payments」的项目。在 gitlab-cli 试点（2026-10）中，issue 和合并请求的评论、讨论串和变更文件都没有 ref 形式，各个命令组只好各自发明一种。

检查：对每种资源类型，测试其 ref、URL 和名称解析到同一个目标（VER-004）。

### CLI-049 减少往返

Agent 原本要循环调用的读取，应当接受多个 ref（`get --refs a,b,c`）。状态命令应当接受 `--wait <duration>`，在状态变化或等待结束时返回。每个命令都应当在有界的时间内完成，远低于运行框架 2 分钟的上限；更久的工作改为按长时间运行的工作处理（CLI-064）。

原因：每次调用都要花一次往返的模型时间；被运行框架在 2 分钟时杀掉的命令什么也返回不了。

检查：人工评审。

## 写操作

所有写操作都走同一个流程，只在 aicli 中实现一次。人授予的授权范围（SEC-010 至 SEC-012）在预演时检查一次，确认时再检查一次；没有授权范围，任何写操作都会在发出任何东西之前被拒绝。

```text
tool mr update acme/web!5 --assignee carol --dry-run            -> ok: preview, confirm_token, expires_at, operation_id
tool mr update acme/web!5 --assignee carol                      -> E_CONFIRMATION_REQUIRED carrying the same four
tool mr update acme/web!5 --assignee carol --confirm <token>    -> re-checks, Apply, receipt; ok derived from the receipt
```

### CLI-050 唯一的共享写入门控

所有带有 `observe` 以外效果的命令都必须经过 aicli 的写入门控：单个目标用 `App.Write`，多个目标用 `App.WriteBatch`。工具不得自行实现令牌、绑定、密钥、操作记录或单次使用存储，也不得在 `Apply` 之外的任何地方调用上游的写接口。

```go
return app.Write(cmd, aicli.WriteSpec{
    Target: aicli.Target{Key: mr.Ref, Kind: "merge_request", Label: mr.Label},
    Preview: func(ctx context.Context) (aicli.Preview, error) { // read-only and deterministic; runs at dry-run, refusal and confirm
        cur, err := api.GetMR(ctx, mr.Ref)
        if err != nil {
            return aicli.Preview{}, err
        }
        return aicli.Preview{Action: "Update merge request", Changes: diff(cur, want),
            BaseVersion: cur.UpdatedAt, BaseSource: "updated_at", CanApply: true}, nil
    },
    Apply: func(ctx context.Context, op aicli.Op) (aicli.Result, error) {
        got, err := api.UpdateMR(ctx, mr.Ref, want)
        if err != nil {
            return aicli.Result{}, classify(err) // NotDispatched, Rejected, or unchanged (= outcome unknown)
        }
        return aicli.Result{
            Outcome: aicli.Outcome{Execution: "completed", Change: "applied", Delivery: "not_applicable",
                Persistence: "not_applicable", Verification: verify(got, want)},
            Observed: map[string]any{"assignees": got.Assignees, "labels": got.Labels},
            Data:     got,
        }, nil
    },
})
```

`classify` 由工具实现。它用 `aicli.NotDispatched` 包装在任何东西离开本机之前产生的错误，用 `aicli.Rejected` 包装能证明什么都没改变的上游回应（经 `FromHTTP` 映射的 4xx，包括 429）。其他错误原样返回，写入门控把它们报告为 `E_OUTCOME_UNKNOWN`。要包装的是 `FromHTTP` 构造出的 `*aicli.Error`，绝不是原始的传输层错误：`aicli.NotDispatched(aicli.FromHTTP(0, msg, "", 0))` 报告 `E_NETWORK`，`aicli.Rejected(aicli.FromHTTP(status, msg, body, 0))` 报告该状态码对应的错误码；如果包装的是普通错误，写入门控拿不到错误码，就退回 `E_SERVER`（回执规则第 4 条）。Python 有同样的 API，采用 snake_case 命名（`app.write`、`WriteSpec`）。

确定目标需要查询上游时（要解析的名称或 ID、变量的环境范围、账户自己的命名空间），设置 `WriteSpec.ResolveTarget`，不用 `Target`。写入门控在预演或裸调用时，于授权范围检查之前调用它；确认时，只在令牌检查之后才调用（CLI-057），所以重复的确认在任何工具代码运行之前就得到 `token_used`；它的错误作为工具自己的错误返回，什么都没有发出。`App.Write` 之前的代码在每次确认时都先于这些检查运行，所以它不读取写操作本身会改变的上游内容。

原因：工具群审计发现，各工具自己实现的门控都以相同的方式失效。全部 10 个有门控的工具，绑定都漏掉了输入（gitlab-cli：59 个写命令中有 23 个；其余 9 个工具共 72 项已确认的问题）；9 个工具的令牌可以伪造，8 个在存储失败时令牌可以重复使用；2 个工具在 `--format text` 下跳过了门控。规则本身是对的，十份实现却都不对。

检查：对每个写命令运行 `G00`–`G10`。

### CLI-051 预演

`--dry-run` 必须不改动上游的任何东西（可以读取），并返回 `ok: true`，其 `data` = `{preview, confirm_token, expires_at, operation_id}`。

- 预览包含 `operation_id`、`command`、`action`、`effects`、`dangerous`、`inputs`、`can_apply` 和 `blocked_by`，另外要么是 `target` 和 `changes`（单个目标），要么是 `targets`、`total` 和 `items`（批量，CLI-063）。可选：`base`（制订计划时所依据的版本），以及缩短的批量预览中的 `omitted`（CLI-063）。按效果必需：`destinations`（`deliver` 命令发往何处）和 `outputs`（`artifact` 或 `persist` 命令写入的本地路径），分别来自 `Preview.Destinations` 和 `Preview.Outputs`；缺了它们的 `Preview` 会被写入门控拒绝，因为授权范围要检查它们（SEC-012）。
- `target` 为 `{key, kind, label}`；每项变更（`aicli.FieldChange`）为 `{field}` 加上 `before`、`after`、`add`、`remove` 或 `note`。
- `inputs` 回显已绑定的输入（CLI-054），机密显示为 `"***"`，文件输入显示为 `{"sha256": …, "bytes": …}`，名为 `id`、`iid` 或以 `_id` 结尾的输入显示为字符串（CLI-007）；工具声明了设置时，当前配置档显示为 `profile`，设置显示为 `setting`，这是一个以设置名称为键的对象（CLI-054）。
- `can_apply` 为 false 时，`blocked_by` 列出若干 `{kind, message}` 条目，`confirm_token` 和 `expires_at` 为 `null`。响应仍然是 `ok: true`，这样 Agent 能看到原因。
- 带 `--idempotency-key` 的预演，如果它的键与较早已执行的请求匹配，就以那个请求的重放作答，而不返回预览（CLI-062）：什么都不计划，也不发送。

```json
{"ok":true,"schema_version":"2.0","data":{
  "preview":{"operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya","command":"mr update","action":"Update merge request",
    "target":{"key":"acme/web!5","kind":"merge_request","label":"!5 Add retry"},"effects":["change"],"dangerous":false,
    "changes":[{"field":"assignees","before":["bob"],"after":["carol"]},{"field":"labels","add":["needs-review"]}],
    "inputs":{"mr":"5","project":"acme/web","assignee":"carol","add_labels":["needs-review"]},
    "base":{"version":"2026-10-11T18:30:24Z","source":"updated_at"},"can_apply":true,"blocked_by":[]},
  "confirm_token":"ct2_4hx2mq7tz3b5kd6wqe2nrc7vya_9f2a61c04be87d35a0c6e1f24b7d9e03",
  "expires_at":"2026-10-12T08:15:00Z","operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya"},
 "meta":{"duration_ms":182,"untrusted":["data.preview.target.label"]}}
```

原因：Agent 拿预览去和用户的请求核对；`operation_id` 在任何操作运行之前就已确定，所以即使回应丢失，也仍然可以查询（CLI-058）。

检查：`G02`（退出码 0、规范格式的令牌、合法的预览、没有上游写入）。

### CLI-052 既不带 --dry-run 也不带 --confirm 的写操作

两个标志都不带的写操作必须不做任何改动，并返回 `E_CONFIRMATION_REQUIRED`（退出码 5，`problem: confirmation_required`），其 details 中带有与预演返回内容相同的 `preview`、`confirm_token`、`expires_at` 和 `operation_id`。所有输出格式下都是如此。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_CONFIRMATION_REQUIRED","message":"This command changes data and nothing was changed; check the preview, then confirm with its token.","retryable":false,"details":{
  "dispatch_state":"not_started","problem":"confirmation_required","next_action":"If details.preview matches the request, re-run the same command with --confirm ct2_7mq2xk4vz6bn3dt5wy2hce7rpa_0c4e8a1f6b2d9e3a7c5f1b8d4e0a6c2f.",
  "preview":{"operation_id":"op_7mq2xk4vz6bn3dt5wy2hce7rpa","command":"issue close","action":"Close issue","target":{"key":"acme/web#12","kind":"issue","label":"#12 Timeout in checkout"},"effects":["change"],"dangerous":false,"changes":[{"field":"state","before":"opened","after":"closed"}],"inputs":{"issue":"12","project":"acme/web"},"can_apply":true,"blocked_by":[]},
  "confirm_token":"ct2_7mq2xk4vz6bn3dt5wy2hce7rpa_0c4e8a1f6b2d9e3a7c5f1b8d4e0a6c2f",
  "expires_at":"2026-10-12T08:20:00Z","operation_id":"op_7mq2xk4vz6bn3dt5wy2hce7rpa"}},
 "meta":{"duration_ms":95,"untrusted":["error.details.preview.target.label"]}}
```

原因：评测中，71 次里有 51 次 Agent 直接依据这个拒绝完成了确认。1.2.17 的拒绝不带预览，39 次里只有 1 次这样做；其余都多花一次调用重新运行 `--dry-run`。

检查：`G01`（退出码 5，`details` 中有预览和令牌，没有上游写入）；`G06`（在 `--format text` 下同样如此）。

### CLI-053 确认时重复同一请求

`--confirm <token>` 必须用在同一个命令、同样的输入上，危险命令还要再次带上 `--dangerous`；令牌绝不会自行执行一份存储起来的计划。任何差异都返回 `E_CONFLICT` `token_mismatch`，由 `parameter` 点出第一个不同的输入，并且什么都不发出。`--confirm` 与 `--dry-run` 同时使用返回 `E_USAGE` `conflicting_flags`。

原因：令牌把 Agent 自己的计划绑定到实际运行的请求上。绑定整个调用几乎没有代价：C、D 两组共 288 次试验中，只有一次合法的确认遇到了不匹配，是 `--body` 与 `--body-file` 的情况，按内容绑定（CLI-054）现在已经接受这种情况。

检查：`G03`（逐一改动样例的输入：改一个值、改一个位置参数、去掉一个标志、加一个标志）；`G04`。

### CLI-054 令牌绑定的内容

令牌必须绑定解析后的请求：

- 规范的命令路径（处理完别名和快捷方式之后）；
- 每个实际给出的参数（标志或位置参数），取归一化之后的值，控制标志 `format`、`json`、`pretty`、`compact`、`fields`、`quiet`、`dry-run`、`confirm`、`dangerous` 和 `help` 除外；
- 每个文件或 stdin 输入内容的 SHA-256；
- 解析后的账号和主机（来自 `App.Identity`）以及目标（`Target.Key`）；
- 工具声明了设置时（CLI-100），当前配置档的名称，无论由什么选定（`--profile`、`<PREFIX>_PROFILE`、`default_profile` 或唯一的配置档），作为输入 `profile`；以及每个已声明设置解析后的值，作为输入 `setting.<name>`；
- 授权范围摘要（授权范围文件字节的 SHA-256）、工具版本、目标的基准版本（预览中有时，`Preview.BaseVersion`）、幂等键，以及过期时间。

aicli 在绑定前做归一化：

- 标志名取规范名（`--repo` 以 `project` 绑定）。标量以解析后值的字符串形式绑定；重复给出或逗号分隔的值按输入顺序以数组绑定。位置参数以 `args` 绑定，除非 `Meta.Positional` 把它们转成标志，所以 `branch delete x` 和 `branch delete --name x` 是同一个请求。
- 文件或 stdin 输入只读取一次，以其内容的 `sha256:<hex>` 绑定在它所填充的参数名下；该参数的字面值以字面值的 `sha256:<hex>` 绑定。因此，在 `f` 的内容为 `X` 时，`--body X` 和 `--body-file f` 是同一个请求。
- 机密值以确认密钥计算的 HMAC 绑定，绝不以明文绑定（SEC-021）。
- 工具以多种形式接受的值（ID、ref、URL、名称）按给定的原样绑定，除非工具先解析它：工具在 `App.Write` 之前调用 `aicli.SetResolved(cmd, flag, canonical)`，这样每种形式都绑定到同一个值。用户没有给出的标志，如果它的值包含在另一个输入里（`acme/web!5` 中的项目），也可以这样设置。每个输入只绑定它自己那一部分：iid 绑定为 `mr`，项目绑定为 `project`，绝不绑定同时包含两者的 ref；这样改动了 `--project`，点出的就是 `--project`，而不是 `--mr`（G03）。在结构化的值里指明文件的（`--action 'update:path=a;content_file=b'`），只有工具在 `App.Write` 之前把文件读一次，并用 `SetResolved` 绑定一个含有内容 SHA-256 的形式，才算绑定了文件的内容。

这些声明（claims）保存在操作记录里，而不在令牌里：`{v, tool, tool_version, op, cmd, inv, inputs, account, host, target, scope, base, idem, exp}`，其中 `v` 为 2，其余每个值都是字符串或摘要。`inputs` 为每个输入名保存一个摘要，因此不匹配时能点出是哪个输入；`inv` 是规范化输入的 SHA-256。规范 JSON 指键已排序、没有空白、使用 UTF-8。

原因：工具群审计发现，每个有门控的工具，其绑定都漏掉了输入：文件内容只按路径绑定（jira 的 `issue attach`、outlook 的附件）；批量路径只绑定了目标列表（archery 的一个 `query run` 令牌接受了不同的 `--sql`）；从环境变量或配置中解析出的账号没有纳入绑定；gitlab-cli 为 `--assignee alice` 签发的一个令牌，结果把 MR 指派给了 dave。在 gitlab-cli 试点（2026-10）中，issue 以整个 ref（`acme/web#12`）绑定，又和它的项目并列，结果改动 `--project` 被当成了改动 `--issue`。

检查：`G03`。

### CLI-055 令牌机制

以下全部由 aicli 负责，工具不配置其中任何一项：

- **格式。** `ct2_`，加上操作 ID 的 26 个 base32 字符，加 `_`，再加 32 个小写十六进制字符。这 32 个字符是 HMAC-SHA256 的前 16 个字节：以密钥对 `"aicli-confirm/2\x00"` 加规范化声明计算得出。只接受 `^ct2_[a-z2-7]{26}_[0-9a-f]{32}$`；其他任何形式都是带 `parameter: "--confirm"` 的 `token_mismatch`。
- **密钥。** `<state_dir>/confirm.key` 中的 32 个随机字节，以独占创建方式、权限模式 0600 创建。密钥文件如果不是恰好 32 个可读字节，就返回 `E_IO`，其 `next_action` 说明要删除该文件，以便重新生成密钥。绝不存在无密钥的退路。
- **过期。** 最长为预演之后 15 分钟。
- **按操作 ID 单次使用。** 确认步骤在发出任何东西之前，以独占创建方式创建 `operations/<id>.consumed`，因此无论是重新编码的令牌还是并发的确认，都无法让同一个操作运行两次。即使之后什么都没有发出，令牌也保持已消费状态；重试要从一次新的预演开始。
- **失败即拒绝（fail closed）。** 没有可用的密钥，就没有令牌，也没有写入；操作记录写不进去，就不写入。任何输出格式、标志或环境变量都关不掉写入门控，测试开关也不例外。

原因：审计发现，6 个工具（包括 gitlab-cli）在无法加载密钥时退回无密钥的 SHA-256；10 个工具中有 8 个在已消费存储出错时跳过单次使用检查，这正是 1.6.3 §9 的要求；4 个工具中重新编码的令牌可以重放（一个 outlook `mail send` 令牌发出了 4 封邮件）；jira-cli 和 confluence-cli 在 `--format text` 下完全没有门控。

检查：`G04`（第二次确认返回 `token_used`）；`G05`（转成大写、加了填充、截断的令牌以及外来令牌都被拒绝）；`G06`（文本模式）；`G08`（密钥损坏、操作记录被阻断时返回 `E_IO`，且什么都不发出）。

### CLI-056 危险命令

危险命令的两步都必须带 `--dangerous`：预演（或裸调用）和确认。不带它时，写入门控在做任何其他事情之前就返回带 `problem: dangerous_flag_required` 的 `E_CONFIRMATION_REQUIRED`。授权范围还必须授予 `dangerous: true`（SEC-012）。命令本身不危险时，一次计划也可能是危险的（一次删除文件的 `commit create`）：`Preview.Dangerous` 让写入门控把这一次运行当作危险操作，两步都要带 `--dangerous`，预览中为 `preview.dangerous: true`，授权范围也要授予 `dangerous: true`。绑定包含这一项，所以在预演和确认之间变成危险的计划属于 `state_drift`。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_CONFIRMATION_REQUIRED","message":"This command is dangerous and needs --dangerous in both the dry-run and the confirm step.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Re-run with --dangerous --dry-run, check the preview, then repeat the command with --dangerous --confirm <confirm_token>.","problem":"dangerous_flag_required"}},"meta":{"duration_ms":2}}
```

原因：两次输入 `--dangerous` 是一种意图声明，复制来的命令或注入的指令不会碰巧带上它；忘了带只多花一次调用（评测 D 组 813 次调用中有 10 次）。在 gitlab-cli 试点（2026-10）中，只含删除动作的 `repo commit create` 不带 `--dangerous` 就删掉了文件，而 `repo file delete` 却需要它。

检查：`G09`。

### CLI-057 确认时按固定顺序复查

确认时，写入门控必须重新运行命令的 `Preview` 并比较：基准版本变了，返回 `E_CONFLICT` `state_drift`；授权范围文件变了，返回 `scope_changed`；账号、主机或目标变了，返回 `token_mismatch`，`parameter` 为 `account`、`host` 或 `target`。重新运行的预览还必须计划同样的改动：`action`、`changes`、`destinations`、`outputs` 或 `dangerous` 不同，或者 `can_apply: false`，同样是 `state_drift`，所以 `Preview` 必须是确定性的：不含时间戳，`Changes` 和列表值的顺序稳定。aicli 按下面的顺序运行每项检查，遇到第一个失败就停止。

确认（`--confirm <token>`）：

1. 解析令牌；格式不规范 → `token_mismatch`（`parameter: "--confirm"`）。
2. 加载密钥；不可用 → `E_IO`。
3. 加载操作记录；不存在 → `token_mismatch`。
4. 对存储的声明校验 HMAC；不对 → `token_mismatch`。
5. 已存在已消费标记，或记录已不是 `planned` → `token_used`，无论令牌多旧。
6. 已过 `expires_at` → `token_expired`，并附上同一命令的新计划。
7. 重新计算输入摘要；不同 → `token_mismatch`，点出第一个不同的参数。
8. 危险命令，或者预演时被判定为危险的计划（`Preview.Dangerous`），缺少 `--dangerous` → `E_CONFIRMATION_REQUIRED`（`dangerous_flag_required`）。直到这时才运行工具代码：设置了 `WriteSpec.ResolveTarget`（批量为 `BatchSpec.ResolveItems`）时，由它确定目标，它的错误就是工具自己的错误。
9. 取得目标的锁（状态目录中的 `locks/<hash>.lock`；批量操作按排序后的顺序取得每个条目的锁），最多等待 10 s → `lock_held`。第 10 至 15 步都在锁内运行，所以同一目标上的两次确认绝不交错。再检查一次已消费标记 → `token_used`。
10. 授权范围：缺失或不足 → 带 `details.scope` 的 `E_FORBIDDEN`；摘要变化 → `scope_changed`；操作预算已用完 → `E_FORBIDDEN`（`scope.status: budget_spent`）。
11. 同一目标上有未解决的操作 → `unresolved_operation`。重新运行 `Preview`：基准版本或计划变化，或者 `can_apply: false` → `state_drift`。重新计算账号、主机和目标：不同 → `token_mismatch`（`parameter` 为 `account`、`host` 或 `target`）。
12. 存在使用同一幂等键的较早操作（CLI-062）→ 返回原始回执并带 `replayed: true`，或者返回 `idempotency_mismatch`、`idempotency_resolved` 或 `unresolved_operation`。
13. 创建已消费标记，并把这次操作计入授权范围的预算；标记已存在 → `token_used`；预算已用完 → `E_FORBIDDEN`（`budget_spent`）；其他任何错误 → `E_IO`。
14. 把记录写为 `dispatching`，带 `lease_until`；失败 → `E_IO`。
15. 释放锁。
16. 运行 `Apply`。
17. 分类：`NotDispatched` → `dispatch_state: not_started`，变更维度为 `not_applied`；`Rejected` → `dispatched`、`not_applied`，带上该错误的错误码；其他任何错误 → `E_OUTCOME_UNKNOWN`；成功 → 依据 `Result` 生成回执。
18. 写入最终记录；写入失败时仍然返回结果，并附一条 `journal_write_failed` 通知。
19. 按回执规则输出（CLI-059）。工具构造的 `result` 如果无法编码，就以 `{}` 代替，并附一条 `result_unencodable` 通知；回执照常有效。

第 1 至 14 步中的任何失败都是 `dispatch_state: not_started`：什么都还没有发出。第 14 步写下 `dispatching` 之后，就不再报告 `not_started`：库无法归类的错误一律是带操作 ID 的 `E_OUTCOME_UNKNOWN`。

预演和裸调用：

1. 危险命令缺少 `--dangerous` → `dangerous_flag_required`。
2. 命令设置了 `ResolveTarget` 时，先用它确定目标（它的错误就是工具自己的错误），再检查授权范围；拒绝时返回带 `details.scope` 的 `E_FORBIDDEN`，能构造出预览时还带 `details.preview`。
3. 同一目标上有未解决的操作 → `unresolved_operation`。
4. 运行 `Preview`；对它判定为危险的计划（`Preview.Dangerous`），缺少 `--dangerous` → `dangerous_flag_required`，授权范围没有授予 `dangerous: true` → `E_FORBIDDEN`。
5. 分配操作 ID。
6. 把记录写为 `planned`；失败 → `E_IO`。
7. 签发令牌，除非 `can_apply` 为 false。
8. 输出：`--dry-run` 返回带 `data` 的 `ok: true`；裸调用返回 `E_CONFIRMATION_REQUIRED`，`details` 中是同样的内容。

原因：绑定基准版本，能阻止依据旧状态计划的写操作在新状态上运行；固定的顺序让每个工具以同样的方式拒绝，而且总是在发出任何东西之前。顺序很重要。在 2.0 的发布评审中，先检查过期的已用令牌在 10 分钟后返回了 `token_expired` 和一个新令牌，照着做就把写操作发了两次。没有锁时，两次按同一版本计划的确认都通过了漂移检查，后一次覆盖了前一次。在 gitlab-cli 试点（2026-10）中，工具在 `App.Write` 之前做的一次查询，对重复确认的 `variable delete` 回答了 `E_NOT_FOUND`，掩盖了 `token_used`；`ResolveTarget` 正是为此放在令牌检查之后运行。

检查：`G10`（授权范围文件在预演和确认之间发生变化 → `scope_changed`，什么都不发出）；其他步骤由 aicli 的写入门控测试覆盖。

### CLI-058 操作记录

`operation_id`（`op_` 加 16 个随机字节，编码为 26 个不带填充的小写 RFC 4648 base32 字符；批量用 `opb_`）必须在预演时分配，并随预览一起返回。记录必须在发出任何东西之前写好；写不进去，就什么都不发（`E_IO`，`dispatch_state: not_started`）。这推翻了 1.6.3 §9 的规定：后者在令牌存储失败时仍让写操作继续。

aicli 把操作记录保存在状态目录中：优先 `App.StateDir`，其次 `<PREFIX>_STATE_DIR`，再次 `~/.<tool>`，以权限模式 0700 创建；路径是符号链接或不是目录时返回 `E_IO`（SEC-030）。

- `operations/<id>.json` 保存记录，以原子方式替换（临时文件、fsync、重命名）；`operations/<id>.consumed` 是单次使用标记。
- 记录的状态依次为 `planned` → `dispatching` → `applied`、`not_applied`、`partial`、`failed` 或 `unknown`，经 `operation resolve` 后变为 `resolved`。超过 `expires_at` 的 `planned` 记录读作 `expired`；超过 `lease_until` 的 `dispatching` 记录读作 `unknown`（租约时长为 `WriteSpec.Timeout`，默认 120 s，再加 5 s，因此比 `Apply` 的截止时间更长）。`Apply` 必须遵守它的截止时间（Go 中是 context，Python 中是 `op.timeout`）；`Apply` 仍在运行时被解决的记录保留其解决结果，这次确认报告 `E_OUTCOME_UNKNOWN`。
- 未解决指 `unknown` 或 `dispatching`。无法读取的记录，或缺少 `operation_id`、`status`、`command`、`created_at` 或 `effects` 的记录，都是不可读记录：读作 `unknown`，阻塞其目标，`operation show` 把它报告为不可读。
- 超过 30 天的记录会被清理（未解决的除外），操作记录最多保留 2,000 条，先丢弃最旧的已解决记录。批量操作的条目随批量记录一起清理。授权范围的预算记在 `scope-usage.json` 中，清理绝不触及它（SEC-011）。

原因：不留记录的写操作，在回应丢失后就无从查询；失败即放行的存储会让一个令牌驱动重复写入，在 1.6.3 §9 下，10 个有门控的工具中有 8 个允许这种情况。

检查：`G08`（操作记录路径被阻断 → `E_IO`，什么都不发出）。

### CLI-059 回执

确认后的写操作必须返回 `data` = `{operation_id, receipt, result}`。回执包含 `operation_id`、`command`、`target`、`effects`、`outcome`、`observed` 和 `replayed`，还可能包含 `mismatched`、`failed_stage` 和 `notes`。`outcome` 分别报告五个维度；不适用的维度取 `not_applicable`。

| 维度 | 取值 |
|---|---|
| `execution` | `not_started`、`running`、`completed`、`failed`、`interrupted`、`unknown` |
| `change` | `not_applicable`、`not_applied`、`applied`、`partial`、`rolled_back`、`unknown` |
| `delivery` | `not_applicable`、`not_attempted`、`delivered`、`partial`、`rejected`、`unknown` |
| `persistence` | `not_applicable`、`unchanged`、`saved`、`partial`、`unknown` |
| `verification` | `not_applicable`、`not_run`、`passed`、`failed`、`incomplete` |

- 执行失败或被中断时带 `failed_stage`（`apply`、`deliver`、`persist`、`verify`）。
- `observed` 是对本次写操作打算修改的字段的回读，来自写操作的响应或一次重新读取。只有 `observed` 与请求一致时，`verification` 才是 `passed`；`mismatched` 列出不一致的字段。
- `result` 是命令自己的载荷，除非 `--fields` 另有要求，否则裁剪到默认字段（CLI-040）；对创建操作而言，其中带有新资源的 `ref` 和 `url`。

`ok` 和错误码由回执推导得出，绝不是任意选定的：由 contract.json `receipt_rules` 中第一条匹配的规则决定。

| # | 回执中有 | ok | 错误码 |
|---|---|---|---|
| 1 | 执行、变更、送达或持久化中有 `unknown`，或执行为 `running` | false | `E_OUTCOME_UNKNOWN` |
| 2 | 变更、送达或持久化中有 `partial` | false | `E_PARTIAL` |
| 3 | 执行为 `failed`、`interrupted` 或 `not_started`，但有持久效果（变更为 `applied`、送达为 `delivered` 或持久化为 `saved`） | false | `E_PARTIAL` |
| 4 | 执行为 `failed`、`interrupted` 或 `not_started`，且没有持久效果 | false | 应用步骤的原因错误码（默认 `E_SERVER`） |
| 5 | 变更为 `rolled_back` | false | 导致工具回滚的原因错误码（默认 `E_VERIFY_FAILED`） |
| 6 | 送达为 `rejected` | false | 送达步骤的原因错误码（默认 `E_FORBIDDEN`） |
| 7 | 校验为 `failed` | false | `E_VERIFY_FAILED` |
| 8 | 校验为 `incomplete` | false | `E_VERIFY_INCOMPLETE` |
| 9 | 其他情况 | true | 无 |

这是基础规则集（base profile），其中 `change: applied` 加 `verification: not_run` 为 `ok: true`。tool-driver-cli-spec 定义了一个更严格的驱动规则集（driver profile），它把同样的回执判为 `E_VERIFY_INCOMPLETE`。`ok` 为 false 时，回执移到 `error.details.receipt`（CLI-014 和 CLI-015 下的示例省略了它）。

```json
{"ok":true,"schema_version":"2.0","data":{"operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya",
  "receipt":{"operation_id":"op_4hx2mq7tz3b5kd6wqe2nrc7vya","command":"mr update","target":{"key":"acme/web!5","kind":"merge_request"},"effects":["change"],
    "outcome":{"execution":"completed","change":"applied","delivery":"not_applicable","persistence":"not_applicable","verification":"passed"},
    "observed":{"assignees":["carol"],"labels":["backend","needs-review"]},"mismatched":[],"replayed":false},
  "result":{"ref":"acme/web!5","title":"Add retry","state":"opened","assignees":["carol"],"labels":["backend","needs-review"],"url":"https://gitlab.example.com/acme/web/-/merge_requests/5"}},
 "meta":{"duration_ms":420,"untrusted":["data.result.title"]}}
```

原因：写操作结果带上被修改的字段后，Agent 在最后一次写入之后的重新读取，从每次含写操作的试验 1.00 次降到 0.37 次（C 组对比 A 组）。从观察到的结果推导 `ok`，部分生效和结果未知就不会被报告为成功。

检查：aicli 依据契约中的表推导 `ok`，并用契约中的示例回执做测试；`R04`。

### CLI-060 结果未知就保持未知

结果未知必须报告为 `E_OUTCOME_UNKNOWN`（CLI-015），该操作保持 `unknown`，直到 `operation resolve` 记下观察到的情况。在它解决之前，同一目标上新的预演和确认应当被拒绝，返回带 `active_operation` 的 `E_CONFLICT` `unresolved_operation`；aicli 默认就这样做。Agent 的恢复步骤：`operation show --id <id>`，重新读取目标，`operation resolve --id <id> --outcome applied|not_applied|partial --evidence "<what the re-read showed>"`，然后继续。查询之前绝不重发。

原因：丢失的回应不能变成重复的写入；在有人查看之前阻塞目标，「重试碰运气」就不可能发生。

检查：aicli 的操作记录测试；Agent 评测中的回应丢失场景（VER-006）。

### CLI-061 操作类内置命令

aicli 添加了三个内置命令，它们只读取或写入操作记录：

- `operation show --id <id>` 返回记录：`operation_id`、`command`、`status`、`target`、`effects`、`scope_id`、`created_at`、`expires_at`、`dispatched_at`、`finished_at`、`preview`、`receipt`、`resolution`、`batch_id`、`items`。它遵守成功响应的大小预算（CLI-042）：批量操作大到一个响应放不下时，预览按 CLI-063 缩短，`items` 先列出需要处理的条目（状态不是 `applied` 或 `resolved`），再列其余条目，由 `items_omitted` 计数被省略的条目；一条通知给出分页命令。
- `operation list [--state <status>] [--limit <n>]` 返回一页记录（CLI-041），最新的在前，带 `total`；会超出成功响应大小预算（CLI-042）的页面提前结束，带 `truncated` 和 `next_offset`。`operation list --batch <batch id>` 按输入顺序分页列出一个批量操作的全部条目。
- `operation resolve --id <id> --outcome applied|not_applied|partial --evidence <text>` 在未解决的操作上记录 `resolution`（`{outcome, evidence, resolved_at}`），原始回执保持原样。它需要一个覆盖该操作效果的授权范围；在该操作的租约有效期间，它会被拒绝，返回 `E_CONFLICT` `lock_held`。

原因：回应丢失后，Agent 凭手上已有的操作 ID 就能查明发生了什么，不需要再发送任何东西。

检查：`R05`（在空的操作记录上运行 `operation list`）；aicli 的内置命令测试。

### CLI-062 幂等键

创建类命令应当接受 `--idempotency-key <key>`。aicli 把这个键绑定进令牌，并作为 `Op.IdempotencyKey` 传给 `Apply`；工具只在 API 遵守它的地方把它转发给上游（例如通过 `Idempotency-Key` 请求头）：Go 的 `net/http` 认为带这个请求头的请求可以安全重复，在复用的连接失败后会重新发送它；上游忽略这个请求头时，这就是一次隐蔽的重复写入（CLI-110）。aicli 在确认的第 12 步（CLI-057）跨所有命令在操作记录中查找这个键，在预演或裸调用时也会在 `Preview` 之前查找，所以即使工具的 `Preview` 会阻止重复的创建，重复请求也能被识别出来。它考虑所有使用这个键的较早操作，而不只是最新的一个，按下面的顺序以第一条适用的规则作答：

- 同一个键，但命令路径、输入摘要、目标、账号或主机有任何差异（包括命令不同）→ `E_CONFLICT` `idempotency_mismatch`；
- 有较早的操作尚未解决 → `unresolved_operation`；
- 有较早的操作被人工解决为 `applied` 或 `partial`，或者以 `partial` 结束 → `E_CONFLICT` `idempotency_resolved`，带 `operation_id`，什么都不发送；解决为 `not_applied` 的操作不妨碍再次发送；
- 有较早的操作为 `applied` → 返回原始回执和结果，带 `replayed: true`（重放的重放也返回最初的那一个），什么都不发送。

批量操作（CLI-063）也用它的键按同样的方式查找。每个条目的 `Op.IdempotencyKey` 是 `<key>/<hash16>`，其中 `<hash16>` 是条目目标键的 SHA-256 的前 16 个十六进制字符：在多次预演之间保持不变，所以上游能识别重复的批量请求；又因目标而异，所以遵守该键的上游绝不会把两个条目合并成一个。用户给的键仍然绑定在整批的令牌里。

原因：一个可能发生也可能没发生的创建操作，只有在重复请求能被识别出来时，才能安全地重复。许多上游会忽略 `Idempotency-Key`，所以在操作记录中查找往往是唯一的保护；在 2.0 的发布评审中，这里的每个漏洞都让一个创建操作发出了两次：回应丢失后被解决为 applied 的键，以及用同一个键重复的批量操作。

检查：aicli 的写入门控测试。

### CLI-063 批量操作

作用于多个目标的命令必须通过一个复数标志接收它们（`--ids 1,2,3`，或重复给出 `--ids`），去重并保持输入顺序；空列表返回 `E_USAGE`（`missing_parameter`）。它经由 `App.WriteBatch` 运行：

- **预览。** 列出每个目标：`targets`、`total` 和 `items`，每个条目为 `{target, operation_id, changes, can_apply, blocked_by}`，都挂在整批自己的 `opb_` ID 之下。任何一个条目不能执行，整批也就不能执行。一个令牌覆盖整批。如果完整预览会超出大小预算（CLI-042），`targets` 和 `items` 按输入顺序只保留前面的条目，由 `omitted` 计数其余的（`total` = 列出的目标数 + `omitted`）；输入和令牌仍然覆盖每个目标。预览的 `action` 取 `BatchSpec.Action`；它为空时取第一个条目的。
- **结果。** 每个目标一个条目 `{target, operation_id, ok}`，其中 `target` 是目标的键，另加 `receipt`、`error`（`{code, retryable, message}`）或 `result`；再加 `summary` `{total, succeeded, failed, unknown, skipped}`，其中每个目标恰好计数一次。
- **ok。** 只有每个条目都成功时，整批才是 `ok: true`。否则：有任何条目结果未知，以 `E_OUTCOME_UNKNOWN` 失败；不然，有任何条目已生效，以 `E_PARTIAL` 失败；再不然，以第一个失败条目的错误码失败。其 details 带 `operation_id`（批量 ID）、`items` 和 `summary`。如果完整的条目列表会超出大小预算（CLI-042），结果（无论在 `data` 还是 `details` 中）先去掉成功条目的 `receipt` 和 `result`，再减少列出的条目；所有 `ok: false` 的条目排在成功条目之前，由 `omitted` 计数被省略的条目；`summary` 仍然计数每个目标，`operation list --batch <batch id>` 则列出全部条目（CLI-061）。
- **停止。** `--continue-on-error` 默认为 true，危险命令默认为 false。停止之后，剩余条目为 `skipped`：`ok: false`，不带 `error`。
- **上游上限。** 对每次调用有数量上限的原生批量接口，在同一契约之下分块调用：一个令牌、一个结果，每一块的结果映射到它所含的条目上。批量操作绝不声称上游并不提供的原子性。使用 aicli 时，`BatchSpec.ApplyAll` 每块调用一次，每块最多 `MaxPerCall` 个条目（为 0 时一次全部发出），并按条目顺序为每个条目返回一个 `ItemOutcome{Result, Err}`，由写入门控映射到对应的条目上。少了或多出结果属于缺陷，这一块的条目都成为 `unknown`；整次调用出错时也是这样，除非这个错误是 `NotDispatched` 或 `Rejected`，那时它适用于每个条目。
- **共享的输入。** 每个条目共有的东西（要指派的用户、项目）只解析一次，放在 `BatchSpec.ResolveItems` 中，它在 `ResolveTarget` 运行的位置运行（CLI-057）：所有条目共有的输入问题于是只报一个错误，带上 `parameter` 和 `next_action`，而不是每个条目各有一个阻止项。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_PARTIAL","message":"Three of the four issues were labelled; one was refused.","retryable":false,"details":{
  "dispatch_state":"dispatched",
  "next_action":"Report the refused issue to the user; do not resend the batch.",
  "operation_id":"opb_7kq3zv5mxn2c4wd6ty3ehb7rpa",
  "items":[{"target":"acme/web#11","operation_id":"op_2bq7xk3mz5nd4tw6vy7hce2rpa","ok":true},
           {"target":"acme/web#12","operation_id":"op_6tn3wq2hz7ck5md4xb6vy2erpa","ok":true},
           {"target":"acme/web#13","operation_id":"op_3hv5mz7qk2wd4nc6tb2xy7erpa","ok":true},
           {"target":"acme/web#14","operation_id":"op_5wd2nk6tq3zm7hc4vb2xy6erpa","ok":false,"error":{"code":"E_FORBIDDEN","retryable":false,"message":"The issue is locked; only maintainers can label it."}}],
  "summary":{"total":4,"succeeded":3,"failed":1,"unknown":0,"skipped":0}}},
 "meta":{"duration_ms":930}}
```

原因：1.6.3 允许一个所有条目都失败的批量操作报告 `ok: true`；审计发现了只绑定目标列表的批量路径（archery 的一个 `workflow audit --ids` 令牌接受了不同的 `--action`）；评测中，逐个给四个 issue 打标签的 Agent 有时会把每个令牌都重新签发一遍，多花 4 次调用。在 gitlab-cli 试点（2026-10）中，`repo commit create` 用一次 GitLab 请求提交所有文件，第一个条目之后的每个条目都只能报告第一个条目的结果；批量指派时，一个未知的指派人让每个 issue 都被单独阻止。

检查：aicli 的批量测试；`G00`–`G10` 也在批量命令上运行；`R04`。

### CLI-064 长时间运行的工作

超出命令有界运行时间（CLI-049）的工作，应当拆成一次受写入门控管辖的提交，以及随后的读取。提交的回执就是一份普通回执，只针对提交本身：`execution: completed` 和 `change: applied` 表示上游接受了这个任务，`data.result.job` 带有 `{ref, status, poll}`，其中 `status` 取 `planned`、`running`、`succeeded`、`failed`、`partial`、`unknown`、`cancelled` 之一，`poll` 是确切的状态查询命令。状态命令是一个读命令，接受 `--wait <duration>`，绝不重新提交。只有上游报告 `succeeded` 或 `failed` 时它才这样报告；只有观察到任务确实停止，而不仅仅是请求了停止，才报告 `cancelled`。操作记录只记下这次提交，不跟踪任务。已完成任务的大结果由单独的命令获取（CLI-043）。状态读取和 `--wait` 由工具自己的代码实现；aicli 不提供任务辅助函数。

```json
{"job":{"ref":"acme/web/exports/81","status":"running","poll":"tool export get acme/web/exports/81 --wait 60s"}}
```

原因：运行框架会在 2 分钟后杀掉调用，被杀掉的调用什么也报告不了。状态读取可以随意重复，提交不行。

检查：人工评审。

### CLI-065 人工操作

需要人先操作时（登录、审批、完成验证挑战、扫二维码、安装组件），命令必须返回 `E_HUMAN_REQUIRED`（退出码 9），带上 `action`（`sign_in`、`approve`、`solve_challenge`、`scan_qr`、`install_component` 或 `other`）、`resume`（之后要运行的确切命令）和 `dispatch_state`，而不是等待或猜测。aicli 的 `HumanRequired(action, resume, message)` 负责构造它。用于同样目的的扩展错误码也使用退出码 9（CLI-018）。

```json
{"ok":false,"schema_version":"2.0","error":{"code":"E_HUMAN_REQUIRED","message":"Signing in needs a code from the account owner's phone.","retryable":false,"details":{"dispatch_state":"not_started","next_action":"Ask the user to run `tool auth login` in their own terminal, then run the resume command.","action":"sign_in","resume":"tool mr list --project acme/web"}},"meta":{"duration_ms":38}}
```

原因：等人的工具会让 Agent 一直挂起，直到运行框架把它杀掉；靠猜的工具则可能把账号锁住。

检查：`R04` 校验 details；人工评审。

## 自更新

### CLI-070 update

`update` 是 aicli 的内置命令。工具把 `App.Update` 设为一个 `UpdateConfig`（Python 为 `App(update=UpdateConfig(...))`）就能启用它：`Repo` 是发布这些版本的 GitHub `owner/name`；`Workflow` 是为 `checksums.txt` 签名的发布工作流（默认 `release.yml`）；其余是工具发布时使用的名称：`NPMPackage`（pnpm 和 yarn 也用它）、`GoPackage`、`PyPIPackage` 和 `BrewFormula`，工具不通过某个渠道分发时，对应的名称留空。没有 `App.Update`，工具就没有 `update` 命令。工具不得自己编写：命令树检查会拒绝工具自定义的根级 `update`（CLI-021）。

`update` 改变的是工具本身，而不是某个目标，所以不经过确认门控：直接运行 `update`，一次调用就完成整个更新，没有子命令，也没有令牌。和操作记录一样，工具自身的安装不是目标，所以它在注册表中的条目只声明 `observe`（CLI-025）。它的标志是 `--check`、`--target-version <version>` 和 `--allow-downgrade`。`--dry-run` 是 `--check` 的同义写法；`--confirm` 返回 `E_USAGE`（`invalid_value`），因为没有什么需要确认。aicli 的 `update` 必须：

1. 在做任何其他事之前，把用到的每个版本都按严格的 semver 校验，允许前面带一个可选的 `v`（`ParseVersion`），无论它来自 `--target-version` 还是发布的 `tag_name`。`--target-version` 不符合，或者没有对应的发布时，返回 `E_USAGE` `invalid_value`；`tag_name` 不符合的发布会被跳过，绝不使用。`CheckUpdateTarget(current, target, allowDowngrade)` 一次调用完成这一项和第 3 项。
2. 运行包管理器时，只使用 `PackageManagerArgs(manager, pkg, version)` 给出的参数向量（npm、pnpm、yarn、go、pip、brew），它由校验过的部分拼成，绝不经过 shell，并且只在 `PATH` 的绝对路径条目中查找，绝不在当前目录中查找（克隆下来的仓库里的 `npm.bat` 绝不能运行）。pip 用的是当前解释器的 `python -m pip`。在 Windows 上，`npm` 解析为 `npm.cmd`，后者在 cmd.exe 下运行，所以校验是唯一的防线。
3. 拒绝降级，除非同时给出 `--target-version` 和 `--allow-downgrade`（`E_USAGE`、`invalid_value`、`parameter: "--target-version"`）。
4. 在二进制安装路径上，先在进程内校验 `checksums.txt` 的 Sigstore 签名包，再校验归档文件的 SHA-256。签名绑定发布工作流在该发布 tag 上的确切身份 `https://github.com/<Repo>/.github/workflows/<Workflow>@refs/tags/v<version>`，签发者为 `https://token.actions.githubusercontent.com`。任何失败都返回 `E_INTEGRITY`；校验绝不跳过（SEC-050）。
5. 在包管理器安装路径上，驱动包管理器去执行（绝不修改它管理的文件，也绝不只打印它的命令），用 `<tool> --version` 确认装上的版本，并如实报告 `signature_status`：只有 aicli 自己校验过签名时才报 `verified`，否则报 `not_checked` 或 `unavailable`。
6. 同步 Skill 时，从已安装的包把它们复制到已经安装了它们的 Skill 目录中，绝不从任何地方拉取；只有同步没能完成时（`failed`，或在 `skill_sync` 阶段的 `E_PARTIAL`），`skill_sync_command` 才为 `<tool> update`。Skill 没有安装绝不构成给出它的理由：`update` 不安装 Skill。
7. 保持幂等：已经是目标版本时，返回 `status: up_to_date`，不运行包管理器，并补完之前某次运行没做完的 Skill 同步。

**安装方式。** aicli 判断正在运行的这份副本是怎样安装的；第一个匹配的规则生效。既不是冻结的二进制、也不在 `site-packages` 或 `dist-packages` 之下的 Python 副本，是从源码运行的，无法自我更新（`E_UNSUPPORTED`）。路径中有 `store/v<n>/links` 段时（pnpm 11 的全局存储），是 `pnpm`。路径中有 `node_modules` 段时，是 `pnpm`（有 `.pnpm` 段）、`yarn`（有 `.yarn` 段，或位于 `/yarn/global/` 之下）或 `npm`。有 `Cellar` 段，或位于 `/opt/homebrew/`、`/home/linuxbrew/` 之下，是 `brew`。位于 `go install` 目录（`GOBIN`，未设置时为 `GOPATH/bin`，再没有时为 `~/go/bin`）的 Go 构建是 `go`，位于 `site-packages` 或 `dist-packages` 之下的 Python 包是 `pip`，其他一律是 `binary`。某个包管理器的包名在 `UpdateConfig` 中留空时，在运行任何东西之前就返回 `E_CONFIG`。

**发现。** 给出 `--target-version` 时，aicli 读取带这个 tag 的发布。不给时，它在最近的 50 个发布中取最高的版本，跳过草稿、带 semver 预发布部分的 tag（无论发布的预发布标记怎么写）和不是 semver 的 tag；只有在没有任何发布带 semver tag 时，结果才是 `E_UNSUPPORTED`。预发布版本只能通过 `--target-version` 获得，也绝不出现在更新通知中（CLI-071）。目标版本不比正在运行的版本新、又没有 `--allow-downgrade` 时，就算已是最新：不下载任何东西，也不运行包管理器。`update --check` 到这里为止：它把结果记入通知缓存，返回 `status` 为 `available` 或 `up_to_date`，并附上 `command`，即包管理器安装时会运行的参数向量。

**包管理器安装路径。** 在 `replace` 阶段，aicli 运行 `PackageManagerArgs` 给出的参数向量，最长 10 分钟，然后从 `<tool> --version` 中读取 `data.version`，其中的 `<tool>` 重新在 `PATH` 的绝对路径条目中查找（pnpm 升级后会留下旧的存储路径）。由这个版本说了算：它等于目标版本，安装就算成功，即使包管理器报告了错误；否则结果是 `E_IO`，并给出需要手工运行的命令。

**二进制安装路径。** `download` 把 `<tool>-<version>-<os>-<arch>.tar.gz`（Windows 上为 `.zip`，其中 arm64 使用 amd64 构建）、`checksums.txt` 和 `checksums.txt.sigstore.json` 下载到可执行文件旁边的一个临时目录中。`verify_signature` 和 `verify_checksum` 校验它们（第 4 项）。在 `replace` 阶段，aicli 只解出二进制和 `skills/`，遇到不安全的条目（绝对路径、`..`、名称中有 `:`、链接、设备文件、重复的名称，或总计超过 10,000 个条目或 1 GiB）以 `E_INTEGRITY` 拒绝，并运行新二进制的 `--version`，它必须报告目标版本，可以用 2.0 的信封，也可以是一行以版本号结尾的纯文本（1.x 版本输出 `gitlab-cli version 1.2.17`）。然后它把二进制换上，并在 `<exe dir>/skills` 中只替换这次发布附带的 Skill，每次一个 `skills/<name>`：多个工具可能共用一个目录，所以其他名称保持不动；发布中没有 `skills/` 时，该目录原样保留（REPO-043）。

**Skill 同步。** `binary` 的来源是 `<exe dir>/skills` 中这次发布附带的 Skill，`npm`、`pnpm` 和 `yarn` 的来源是 npm 包根目录下的 `skills/`；其他安装方式没有来源。复制时绝不跟随链接。对来源中的每个 Skill 和 `App.SkillDirs`（默认为 `~/.agents/skills` 和 `~/.claude/skills`）中的每个目录：已安装的副本是真实目录的，用一份新副本替换；是符号链接或目录联接（junction）的，保持不动。没有安装在那里的 Skill 不会被创建：安装是 README 的事（REPO-010）。复制了任何东西时，`skill_sync_status` 为 `synced`；每份已安装的副本都已经是工具的版本时，为 `not_needed`；没有来源或没有已安装的 Skill 时，为 `skipped`。

**网络。** 每个请求都有 30 s 的空闲超时，下载另有 10 分钟的总时限，每个请求都遵循 CLI-110：代理取自 `HTTPS_PROXY`、`HTTP_PROXY` 和 `NO_PROXY`，TLS 始终校验，`<PREFIX>_CA_FILE` 中的证书加入系统根证书，并使用 aicli 的 User-Agent。在 Go 中，刷新 Sigstore 信任根时也使用同样的超时、代理、CA 文件和 User-Agent；在 Python 中，sigstore-python 自己的下载器遵守代理变量和超时，但不使用 `<PREFIX>_CA_FILE` 和 User-Agent，所以在做 TLS 检查的代理后面，`update` 会在 `verify_signature` 阶段以 `E_NETWORK` 安全地失败。`GITHUB_TOKEN`（没有时用 `GH_TOKEN`）只发给 API 主机。下载的资源和重定向都必须是 `https`。响应大小有上限：发布元数据 4 MiB，`checksums.txt` 和签名包各 1 MiB，归档文件及其中的任何文件 512 MiB。

结果的 `data` 包含 `status`（`up_to_date`、`updated` 或 `available`；`partial` 只出现在部分更新的 details 中，见下文）、`current_version`、`target_version`、`update_available`、`install_method`（`binary`、`npm`、`pnpm`、`yarn`、`go`、`pip` 或 `brew`）、`signature_status`（`verified`、`not_checked`、`unavailable`）和 `skill_sync_status`（`synced`、`not_needed`、`failed`、`skipped`），适用时再加上 `previous_version`、`command`、`binary_replaced`、`signature_verified`、`checksum_verified`、`skill_sync_command` 和 `stage`。这些值描述命令执行之后的状态：安装完成后，`current_version` 等于 `target_version`，`update_available` 为 false。之后 Agent 读取 `changelog --since <previous_version>`（CLI-037）。

```json
{"ok":true,"schema_version":"2.0","data":{"status":"updated","current_version":"2.1.0","target_version":"2.1.0","previous_version":"2.0.0","update_available":false,"install_method":"binary","signature_status":"verified","signature_verified":true,"checksum_verified":true,"skill_sync_status":"synced","binary_replaced":true},"meta":{"duration_ms":8412}}
```

更新分阶段进行：`discover`、`download`、`verify_signature`、`verify_checksum`、`replace`、`skill_sync`，只有一个原子提交点：`replace` 中的替换。每个失败都在 `error.details` 中带 `stage`、`current_version`、`binary_replaced` 和 `skill_sync_status`（同步运行之前为 `skipped`），一旦知道，还带 `target_version` 和 `install_method`。在包管理器运行或替换发生之前，`dispatch_state` 为 `not_started`，此后为 `dispatched`；替换失败且无法撤销时为 `unknown`。

| 阶段 | 失败情形 | 错误码（退出码） | 可重试 | 之后的状态 | `next_action` 的内容 |
|---|---|---|---|---|---|
| `discover`、`download` | 网络、超时、限流、重定向到明文 `http` | `E_NETWORK`、`E_RATE_LIMITED`（7）、`E_TIMEOUT`（8） | true | 旧版本，未改变 | 暂时性问题：重新运行 `update`，它是幂等的 |
| `discover` | `--target-version` 不是 semver 或没有对应的发布，或者没带标志就降级 | `E_USAGE`（2） | false | 未改变 | 修正版本号，或加上 `--allow-downgrade` |
| `discover` | 从源码运行的副本、开发构建（CLI-080），或者没有任何发布带 semver tag | `E_UNSUPPORTED`（2） | false | 未改变 | 用 git 更新源码检出，或把发布的 tag 报告给工具的维护者 |
| `discover` | `<PREFIX>_CA_FILE` 无法读取或不含证书 | `E_CONFIG`（4） | false | 未改变 | 修正或取消 `<PREFIX>_CA_FILE` |
| `download` | 没有适用于本操作系统和架构的构建，或者资源不是通过 `https` 提供的 | `E_UNSUPPORTED`（2） | false | 未改变 | 用 README 中的包管理器命令安装 |
| `download` | 找不到正在运行的可执行文件 | `E_IO`（1） | false | 未改变 | 用 README 中的安装命令重新安装 |
| `verify_signature` | 无法加载 Sigstore 信任根 | `E_NETWORK`（7） | true | 旧版本，未改变 | 检查网络，然后重新运行 `update` |
| `verify_signature`、`verify_checksum` | 签名缺失或无效、身份不匹配、校验和缺失或不符 | `E_INTEGRITY`（1） | false | 旧版本；拒绝安装 | 完整性失败：不要重试；报告它 |
| `replace` | 归档中有不安全的条目（绝对路径、`..`、链接、设备文件），或者新二进制报告的是另一个版本 | `E_INTEGRITY`（1） | false | 旧版本；拒绝安装 | 完整性失败：不要重试；报告它 |
| `replace` | 没有配置包名，或者 PATH 上没有该包管理器 | `E_CONFIG`（4） | false | 未改变 | 安装该包管理器，或用 README 中的安装命令重新安装 |
| `replace` | 包管理器无法安装目标版本（pip 与预发布版本；没有 pip 的环境） | `E_UNSUPPORTED`（2） | false | 未改变 | 适用安装器的命令（`pipx upgrade`、`uv tool upgrade`） |
| `replace` | 替换失败，且旧二进制无法放回 | `E_IO`（1），`dispatch_state: unknown` | false | 工具可能已缺失 | 把点名的 `<exe>.old` 文件改回可执行文件的名称，然后运行 `<tool> --version` |
| `replace` | 权限不足、磁盘已满、文件被锁、新二进制无法运行、包管理器运行失败 | `E_IO`（1） | false | 旧版本；什么都没提交 | 具体修法，或要手工运行的包管理器命令；然后重新运行 |
| `skill_sync` | 替换之后 Skill 同步失败或被中断 | `E_PARTIAL`（1），details 带 `status: partial`、`target_version`、`skill_sync_command` | false | 新二进制，旧 Skill | 二进制已是新版本；再运行一次 `update` 完成同步，然后运行 `changelog --since <previous>` |
| 替换之前 | SIGINT 或 SIGTERM | `E_INTERRUPTED`（130） | true | 旧版本，未改变 | 已取消；什么都没改变 |

以下规则保证每条消息都如实：

- 替换之前的一切都在可执行文件旁边、同一文件系统上的临时目录中进行。在这一阶段失败或被中断，已安装的工具保持原样（`binary_replaced: false`），临时目录被删除；之后的运行绝不信任下载了一半的文件。
- 替换是原子的。在 POSIX 上，校验过的二进制在同一目录内被重命名到原位置。在 Windows 上，正在运行的 `<exe>` 被重命名为 `<exe>.old`（删不掉旧的残留文件时改用 `<exe>.old-<n>`），新文件被重命名为 `<exe>`；第二次重命名失败时，撤销第一次；撤销也失败时，错误会说明这一点，并点出需要改回名称的文件。下一次 `update` 会删除残留的 `<exe>.old*` 文件。替换中途崩溃，留下的要么是旧二进制，要么是新二进制，绝不会是两者的混合。
- aicli 捕获 SIGINT 和 SIGTERM，仍在 stdout 上输出一个终态信封。正在运行的包管理器绝不会被杀掉：aicli 在其 10 分钟的上限内等它结束。替换之后的中断按实际情况报告：已完成的更新，或在 `skill_sync` 阶段的 `E_PARTIAL`，绝不报告为 `E_INTERRUPTED`。
- 每个终态信封都写明当前安装的版本。完整性失败绝不可重试。二进制已替换而 Skill 过时，绝不是 `ok: true`。

原因：更新路径审计发现，在 cnstock-cli 和 wechat-mp-cli 中，精心构造的 `--target-version`（`1.0.12&calc`）未经校验就到达了 `npm.cmd`；在 Windows 上，这等于一次不经门控的调用就能执行命令。另有五个 Go 工具以同样的方式传递发布的 `tag_name`，只有 cliproxyapi-cli 校验了 semver；还有四个工具把明确指定的旧版本当作「更新」接受。这些工具各自写了自己的更新；一个只需测试一次的内置命令取代了它们。1.6.3 本身就规定 npm 路径上报 `signature_status: not_checked`，并在每次更新时运行未钉住版本的 `npx skills add <repo> -y -g`。

检查：aicli 的更新测试针对模拟的发布服务器和模拟的校验器，覆盖每个阶段和每种安装方式；工具测试自己的 `UpdateConfig`（VER-004）。

### CLI-071 更新通知

aicli 只在两处访问发布源：`update`（包括 `--check`），以及设置了 `App.Update` 时的 `doctor`；后者只查询最新的发布，超时为 3 s，失败则忽略。两者都用 `RecordUpdateCheck(latest, severity)` 记下结果，该函数写入 `<state_dir>/update-check.json`。工具不得在其他任何地方访问发布源（SEC-022）。其他命令可以把缓存中的通知附加到 `meta.notices`，只读缓存，每天最多一次（CLI-047）；aicli 从该文件读取并附加它。通知是带类型的：

```json
{"type":"update_available","severity":"info","message":"tool 2.1.0 is available; this is 2.0.0.","current_version":"2.0.0","latest_version":"2.1.0","command":"tool update","checked_at":"2026-10-12T06:00:00Z"}
```

两个版本之间的某份发布说明含有 `Security` 标题，或主版本号发生变化时，`severity` 为 `warning`，否则为 `info`。已安装版本一旦达到缓存中的版本，aicli 就不再附加通知，所以任何响应都不会宣传刚刚安装好的那个版本。

原因：更新通知只在第一次有用，之后每次调用都在消耗 token；业务命令也不能为了产生通知而访问网络。

检查：aicli 的通知测试和 `doctor` 测试。

## 版本与性能

### CLI-080 版本

`schema_version`（`"2.0"`）是信封契约的版本，不是工具的版本；只有破坏性的信封变更才会改变它的主版本号，新增内容不改变它。`reference` 必须报告 `spec_version`（工具所遵循的本规范的版本，`2.0.0`）和 `schema_version`；`--version`、`context` 和 `reference` 报告工具自己的版本。没有语义化版本的构建（`dev`，不带 `-ldflags` 的构建就是这样）按其 CHANGELOG 中最新发布之后的下一个补丁版本、加预发布标记 `dev` 运行（`1.2.18-dev`）；`update`（包括 `--check`）以 `E_UNSUPPORTED` 拒绝它，`doctor` 的 `version` 检查会警告它的版本无法比较。已弃用的命令、标志和字段遵循 CLI-081。Skill 声明 `metadata.min_version`（SKL-001）；已安装的 Skill 需要更新版本的工具时，`doctor` 的 `version` 检查失败；Skill 缺失或比工具旧时，该检查给出警告（CLI-036）。

原因：为更新版本的工具编写的 Skill 会悄无声息地调用不存在的命令；版本检查把这种情况变成一个明确的 `doctor` 失败。

检查：`R01`（`spec_version`、`schema_version`）；`R05`（`version` 检查）。

### CLI-081 弃用

将要移除的命令或标志必须先标为弃用，并在移除之前一直可用。使用 aicli 时，`Meta.Deprecated` 存放命令的替代说明，例如「use `mr update --label`」，`Meta.DeprecatedFlags` 把标志名映射到它的替代说明；`Meta.RenamedFlags` 把改了名的标志的旧名映射到新名（`search` 映射到 `query`），aicli 在解析之前把旧名改写为新名，所以它按新名绑定；两个名字都给出时返回 `E_USAGE` `conflicting_flags`，`--help` 不列出旧名，`reference` 把它列为一个参数，说明以 ``Deprecated: use `--<new>`.`` 开头。随后 aicli 会：

- 在帮助和 `reference` 中，让命令的简介以 `Deprecated: <text>.` 开头，已弃用标志的描述也这样处理；
- 每次运行已弃用的命令，都附加一条 `deprecated` 通知（`severity: warning`，CLI-047），写明哪个版本会移除它；每次运行用到已弃用的标志，也附加一条；
- 在 `App.CommandMap` 中略去已弃用的命令，这样 Skill 和 README 只教当前的命令（SKL-002）。

```json
{"type":"deprecated","severity":"warning","message":"`mr label` is deprecated: use `mr update --label`. It will be removed in tool 3.0.0."}
```

已弃用的命令或标志至少要在一个 MINOR 版本中、并且至少在 90 天内保持可用，只能在 MAJOR 版本中移除（REPO-030）。`CHANGELOG.md` 在标为弃用的那个版本中把它列在 `Deprecated` 之下，在移除它的那个版本中列在 `Removed` 之下（CLI-037）。已弃用的输出字段在同样的期限内保留其键。

原因：Agent 会一直沿用 Skill、README 或更早的对话记录教给它们的用法。在 MINOR 版本中突然消失的命令，会让这些调用毫无预警地失败；每次使用都附带的通知，会在旧写法仍然可用时点出替代写法，更新之后 `changelog` 也会说明同样的内容。在 gitlab-cli 试点（2026-10）中，要让 `mr list --search` 在 `--query` 之外继续可用，工具自己的代码里得写一个隐藏标志、一次复制和一个冲突检查。

检查：aicli 的弃用测试；评审每项弃用都写进了 `CHANGELOG.md`，并且没有在 MAJOR 版本之外移除任何东西。

### CLI-090 启动时间

`PATH` 上的命令应当是原生二进制，或者是额外开销不超过 100 ms 的启动器；由 CI 测量（REPO-SPEC）。

原因：在一台 Windows 测试机上，经 npm 启动器每次调用耗时 1.1–2.3 s，单独运行二进制只要 0.4–0.7 s；Agent 每次调用都要付出这笔开销。

检查：CI 对已安装命令计时。

## 设置、账户与诊断

### CLI-100 配置

设置（主机、区域、默认项目）必须按以下来源解析，先有值的来源生效：当前所运行命令上的标志、环境变量、设置文件中当前生效的配置档（CLI-101）、默认值。设置 `<name>` 对应的环境变量默认是 `<PREFIX>_<NAME>`，其中 `<PREFIX>` 是工具名的大写蛇形写法（`tool` 的 `host` 设置对应 `TOOL_HOST`）。

设置文件是 `<PREFIX>_SETTINGS_FILE` 指定的文件（绝对路径），没有设置该变量时为 `<state_dir>/settings.json`；aicli 不从其他任何文件读取设置。它的解析与授权范围文件一样严格（SEC-011）：必须是普通文件，不能是链接；在 POSIX 上归当前用户所有，且其他人不可写；不带 BOM 的 UTF-8、只有一个 JSON 值、没有重复键、每个键的拼写完全一致、不超过 64 KiB，结构如下：

```json
{"default_profile": "work",
 "profiles": {"work": {"host": "https://gitlab.example.com", "group": "acme"},
              "personal": {"host": "https://gitlab.com"}}}
```

`default_profile` 是一个配置档名称或 `null`。配置档名称匹配 `^[a-z0-9][a-z0-9_-]{0,63}$`，每个配置档把声明过的设置映射到字符串、数字或布尔值。未知的顶层键、未知的设置、不是标量的值，或者任何声明为机密的设置，都会让整个文件无效：读取设置的命令以 `E_CONFIG`（退出码 4，`dispatch_state: not_started`）失败，消息点出文件和原因。文件不存在不算错误；其他来源照常生效。

设置文件不存机密：机密放在操作系统钥匙串中，或来自环境变量（SEC-040）。设置文件由人来写，或者由一个经过写入门控的 `configure` 命令来写（CLI-025、CLI-101）；除此之外什么都不写它。

使用 aicli 时，工具声明 `App.Settings`，每项为一个 `Setting{Name, Env, Flag, Default, Secret, Description}`。`Name` 用 snake_case；`Env` 默认为大写的 `Name`，按 `<PREFIX>_<Env>` 读取；`Flag` 指定一个命令标志，在当前运行的命令上给出这个标志时，由它覆盖该设置；`Secret` 让这个值不进入设置文件，也不出现在诊断输出和错误中（CLI-102、SEC-021），并且让 `Flag` 在所有地方都成为机密标志，如同它列在 `Meta.Secrets` 中。`App.Setting(cmd, name)`（Python 为 `app.setting(name)`）返回值及其来源：`flag`、`env`、`profile` 或 `default`。`App.Profile()`（Python 为 `app.profile()`）返回当前配置档的名称和来源，`App.Profiles()`（Python 为 `app.profiles()`）按文件中的顺序返回各配置档的名称，`App.SettingsPath()`（Python 为 `app.settings_path()`）返回设置文件，这样工具就能按配置档区分钥匙串条目，并写入配置档的设置。`context` 报告配置档（CLI-035），`doctor` 的 `settings` 检查校验设置文件（CLI-036）。

原因：只有每个工具都以同样的方式解析设置，Agent 和人才能预知某个命令会用哪个主机或账户；也只有由同一个解析器产生值，`context` 才能说明它从哪里来。2026-10 的工具群审计发现，有的写入路径里会从环境变量或配置文件读取账户、区域和时区（SEC-020）；把设置声明出来，这些读取就一目了然。设置文件拒收机密，是因为把它拷出本机，不该得到任何可用的东西（SEC-040）。它用了一个单独的名字，是因为工具群中的工具在同一个目录里保留着 1.x 的 `config.json`，而 aicli 不读取那个文件。

检查：aicli 的设置测试（来源顺序、严格解析、文件中出现机密）；评审工具只通过 `App.Setting` 读取设置。

### CLI-101 账户与配置档

能以多个账户、或针对多个主机操作的工具，必须在设置文件中提供命名的配置档（CLI-100）。当前生效的配置档按以下顺序取第一个：`--profile <name>`，即工具声明了设置时 aicli 注册的根级标志；`<PREFIX>_PROFILE`；文件中的 `default_profile`；恰好只有一个配置档时的那一个；否则没有。`context.profile` 报告它及其来源：`flag`、`env`、`config`、`single` 或 `none`（CLI-035）。`--profile ""` 视同没有给出。未知的名称在任何工具命令运行之前就返回 `E_CONFIG`，名称放在 `details.profile` 中（绝不放进 `message`，CLI-017），`did_you_mean` 列出最接近的配置档名称；内置命令（`context`、`doctor`、`reference`、`changelog`、`operation`、`update`、`help`、`version`）照常运行，`context` 会报告这个问题。当前配置档的名称（无论由什么选定）和每个已声明设置解析后的值都绑定进确认令牌（CLI-054），所以在一个配置档下、或以某个设置值做的预览，不能在另一个配置档或另一个设置值下确认（`token_mismatch`，`parameter` 为 `profile` 或 `setting.<name>`）；任何命令都不定义自己的 `--profile`（CLI-021）。

有登录类命令的工具，必须这样命名它们：

- `auth login` 保存凭证：机密放进操作系统钥匙串，配置档的其他设置放进设置文件（SEC-040）。它声明 `configure`（CLI-025）和 `Meta.AllowUnknownProfile`，所以能在文件中还没有的配置档下运行；它用 `App.SetProfileSettings`（Python 为 `app.set_profile_settings`）写入设置，这个函数像读取时一样校验设置，以原子方式写入，出错时什么都不写，所以 `Apply` 把这个错误作为 `NotDispatched` 返回；它建立文件中的第一个配置档时，也把这个配置档设为默认，但绝不在已有的配置档中替人选默认。对这样的命令，`App.Profile()` 返回所要求的配置档及其来源。需要人来操作时（浏览器登录、一次性验证码），它返回带 `action: sign_in` 的 `E_HUMAN_REQUIRED`（CLI-065）。
- `auth status` 是读命令：它向上游核验凭证，报告账户、主机和凭证的过期时间，绝不包含机密。
- `auth logout` 从钥匙串中删除凭证。它声明 `configure`，也声明 `Meta.AllowUnknownProfile`，这样配置档从设置文件中删掉之后，它仍能清理残留的凭证。

原因：人们会使用多个账户和主机，例如公司的 GitLab 和 gitlab.com。没有配置档，Agent 只能在两次调用之间改环境变量来切换，切错了也没有任何提示。把 `--profile` 和解析出的账户、主机一起绑定，就能防止为一个账户制定的计划以另一个账户的身份执行：2026-10 的工具群审计发现，有的工具自己的写入门控没有把从环境变量或配置中解析出的账户纳入绑定（CLI-054）。统一的 `auth` 命名，让在一个工具上登录过的 Agent 在下一个工具上也能找到同样的命令。在 gitlab-cli 试点（2026-10）中，第二次 `auth login` 之后没有任何配置档生效，直到有人手工设置 `default_profile`。

检查：aicli 的配置档测试（来源顺序、未知名称、绑定）；评审 `auth` 命令的效果。

### CLI-102 诊断

只有把 `<PREFIX>_DEBUG` 设为非空且不为 `0` 的值，诊断输出才会打开。它以 `debug:` 开头的行写到 stderr，绝不写到 stdout，也绝不改变信封或退出码。机密参数（`Meta.Secrets`，SEC-021）、机密设置（CLI-100），以及工具用 `App.AddSecret(value)`（Python 为 `app.add_secret(value)`，用于从钥匙串读出的令牌）登记的机密，其值及其 URL 转义形式都显示为 `***`；消息中的换行会被转义，所以每一行都以 `debug:` 开头。工具不得添加 `--verbose` 或 `--debug` 标志。aicli 通过 `App.Debugf(format, args...)`（Python 为 `app.debug(message)`）写出这些行，多个 goroutine 或线程可以同时调用它；它自己只记录状态目录、授权范围文件路径、设置文件和当前生效的配置档，以及 `update` 每个阶段的开始和它跳过的发布标签。除非用户要求，Skill 绝不设置 `<PREFIX>_DEBUG`（SKL-009）。

原因：人在排查故障时需要信封以外的信息，但 stderr 上的每一行在每次调用时都要消耗 Agent 的 token（CLI-047）。由人来设置的环境变量，默认把诊断输出挡在 Agent 会话之外；换成标志，它就会出现在 Skill 和对话记录里。脱敏让调试日志可以放心地贴进 issue。

检查：aicli 的测试（设置 `<PREFIX>_DEBUG` 后 stdout 和退出码不变，机密已脱敏）；评审没有命令定义 `--verbose` 或 `--debug`。

## 网络

### CLI-110 网络行为

每个上游请求都必须有超时：默认最长 30 s，并且绝不超过 CLI-049 对命令运行时间的限制所允许的长度。工具可以在连接失败或 HTTP 429、502、503、504 之后重试幂等的读取，最多两次，并遵守 `Retry-After`，但最多等 10 s；上游要求等得更久时，工具直接返回该错误，`E_RATE_LIMITED` 带上 `retry_after_ms`（CLI-016）。它不得重发可能已经派发出去的写入（CLI-015、CLI-060）。

工具必须遵守 `HTTPS_PROXY`、`HTTP_PROXY` 和 `NO_PROXY`。TLS 证书始终校验：私有 CA 来自 `<PREFIX>_CA_FILE`，这是一个加入系统根证书的 PEM 文件；没有任何标志、变量或设置能关掉校验。请求带上 `User-Agent: <tool>/<version> aicli/<spec version>`。`doctor` 的 `connectivity` 检查因证书而失败时，其消息要说明这一点，其 `fix` 要点出 `<PREFIX>_CA_FILE`（CLI-036）。aicli 自己的请求（`update`、`doctor` 的刷新）也遵守本规则；工具可以从 `App.HTTPTransport()` 得到同样的代理、CA 文件和 TLS 设置（它像读取 CA 文件一样，通过 `App.Env` 读取代理变量），从 `App.UserAgent()` 得到请求头（Python 为 `app.ssl_context()`、`app.user_agent()`）。

原因：运行框架会在 2 分钟后杀掉调用，被杀掉的调用什么也报告不了（CLI-049），所以一个没有超时的请求可能让整个回答落空。重发写入，正是同一个改动被执行两次的原因：gitlab-cli 1.x 的 HTTP 客户端对所有方法的 5xx 和网络错误都会重试，所以一个超时的 POST 可能被执行两次。Agent 运行在带代理和私有 CA 的企业网络中；一个 CA 文件就能让这样的网络正常工作，而不必关掉防止拦截的那道检查。

检查：评审上游客户端（超时、按方法区分的重试、代理、TLS）；VER-004 的上游失败行和 Apply 失败行。

## 检查清单

输出

- [ ] CLI-001 stdout 上只有一个 JSON 文档；其他一切写到 stderr；不带 BOM 的 UTF-8；没有交互提示
- [ ] CLI-002 信封键集精确；`schema_version` 为 `"2.0"`
- [ ] CLI-003 默认紧凑 JSON；`--pretty`；接受 `--compact`
- [ ] CLI-004 始终有 `meta.duration_ms`；此外只有 `notices` 和 `untrusted`
- [ ] CLI-005 默认 `json`；`text` 给人看；声明过才有 `raw`；不支持的格式 → 以 JSON 输出 `E_USAGE`
- [ ] CLI-006 流：每行一个带 `type` 的信封，以 `summary` 结尾
- [ ] CLI-007 snake_case 键、字符串 ID、`_at` 键下带 `Z` 的 UTC 时间、`_on` 键下的日期、snake_case 枚举

错误

- [ ] CLI-010 错误码、退出码和 `retryable` 来自 contract.json
- [ ] CLI-011 每个错误都带 `dispatch_state` 和 `next_action`
- [ ] CLI-012 `problem`，适用时带 `parameter`、`expected`、`did_you_mean`、`usage`、`available`
- [ ] CLI-013 `conflict`，按要求带 `parameter` 或 `active_operation`
- [ ] CLI-014 授权范围拒绝时带 `details.scope`；上游 403 时带 `required` 角色
- [ ] CLI-015 派发后没有回应 → 带 `operation_id` 和 `lookup` 的 `E_OUTCOME_UNKNOWN`
- [ ] CLI-016 唯一的 HTTP 映射函数，按状态码映射
- [ ] CLI-017 写给模型的简短错误信息，用英语；不回显输入，不含上游文本
- [ ] CLI-018 扩展错误码只通过 `contract-ext.json` 定义

命令模型

- [ ] CLI-020 唯一的注册表；帮助、`reference`、Skill 命令地图和覆盖率都从它生成
- [ ] CLI-021 命令树形状；保留名称和保留标志；`<noun> <verb>` 并使用标准动词
- [ ] CLI-022 只在一个父级下的名词有根级快捷方式、复数形式、猜测清单、全树范围的建议
- [ ] CLI-023 先匹配路径；拒绝未知标志和多余的位置参数；位置参数绑定到标志
- [ ] CLI-024 带类型的参数；`secret`；必填参数不设默认值
- [ ] CLI-025 如实的效果（评论是 `change`）；无法撤销的写操作是危险的，批量命令只在它做的事危险时才是；风险档不低于效果所要求的级别
- [ ] CLI-026 每个命令的错误码
- [ ] CLI-027 有证据支撑的成熟度
- [ ] CLI-028 标准标志名：`--limit`、`--cursor`、`--query`、`--since`、`--sort`、`--state`、`--wait`、`--output`、`--input`、`--profile`
- [ ] CLI-029 标准动词含义：没有匹配的 `list` 返回一页，`update` 只改给出的内容，`delete` 的目标不存在时是 `E_NOT_FOUND`，`search` 写明它实际的顺序

发现

- [ ] CLI-030 生成的帮助；根级列出所有命令；写命令的帮助给出操作步骤；没有补全命令
- [ ] CLI-031 `reference --index` 不超过 8,000 个字符，或已分组
- [ ] CLI-032 `reference --command` 不超过 6,000 个字符
- [ ] CLI-033 完整的 `reference` 只供工具链使用
- [ ] CLI-034 没有必经的第一步
- [ ] CLI-035 `context` 依据本地状态，带授权概况；有设置时还带配置档
- [ ] CLI-036 `doctor` 包含五项必需检查，工具声明了设置时再加 `settings`
- [ ] CLI-037 `changelog [--since]` 依据内嵌的 CHANGELOG
- [ ] CLI-038 会过期的凭证要续期，否则返回点明登录步骤的 `E_AUTH`

读取与输出大小

- [ ] CLI-040 按任务选定的默认字段；`--fields`
- [ ] CLI-041 `items`、`count`、`has_more`、续取信息、`truncated`、`scope`、`sort`
- [ ] CLI-042 默认成功响应不超过 8,000 个字符（完整 `reference` 和 `raw` 豁免）；错误不超过 2,000 个字符
- [ ] CLI-043 重型子资源由单独的结构化命令提供
- [ ] CLI-044 计数和过滤在工具内完成
- [ ] CLI-045 富文本用 Markdown
- [ ] CLI-046 `meta.untrusted` 路径，包括带着人写的文本的 ref，并跟随 `--fields`
- [ ] CLI-047 安静的 stderr；带类型的通知
- [ ] CLI-048 ref（子资源的 ref 接在父资源的 ref 之后）、URL、名称（有歧义时 `E_AMBIGUOUS`）、相对时间、`--input`、`--<param>-file`
- [ ] CLI-049 多 ref 读取、`--wait`、有界运行时间

写操作

- [ ] CLI-050 每个写操作都经过 aicli 的写入门控；工具没有自己的令牌代码
- [ ] CLI-051 预演返回预览、令牌、过期时间和操作 ID
- [ ] CLI-052 裸调用返回带预览和令牌的 `E_CONFIRMATION_REQUIRED`
- [ ] CLI-053 确认时重复同一请求
- [ ] CLI-054 令牌绑定解析后的整个请求
- [ ] CLI-055 规范格式、带密钥、单次使用的令牌；失败即拒绝；无法绕过
- [ ] CLI-056 危险命令或危险计划的两步都带 `--dangerous`
- [ ] CLI-057 确认时按固定顺序复查
- [ ] CLI-058 在发出任何东西之前写好操作记录
- [ ] CLI-059 回执；`ok` 由 `receipt_rules` 决定
- [ ] CLI-060 结果未知时阻塞目标，直到解决
- [ ] CLI-061 `operation show`、`list`、`resolve`
- [ ] CLI-062 创建类命令支持 `--idempotency-key`
- [ ] CLI-063 批量：一个复数标志、一个令牌、逐条目结果、任何失败都 `ok: false`
- [ ] CLI-064 长时间运行的工作：一次受门控的提交，加一个带 `--wait` 的状态读取
- [ ] CLI-065 带 `action` 和 `resume` 的 `E_HUMAN_REQUIRED`

自更新、版本、性能

- [ ] CLI-070 aicli 依据 `App.Update` 提供的内置命令：严格 semver、不经 shell、不静默降级、二进制经过校验、从包内复制 Skill、分阶段报告失败
- [ ] CLI-071 带类型的更新通知，从本地缓存读取；只有 `update` 和 `doctor` 访问发布源
- [ ] CLI-080 `schema_version`、`spec_version`、Skill 的 `min_version`
- [ ] CLI-081 弃用要标出、每次使用都附通知、至少保留一个 MINOR 版本和 90 天、只在 MAJOR 版本中移除
- [ ] CLI-090 启动器开销不超过 100 ms

设置、账户与诊断

- [ ] CLI-100 设置依次取自标志、环境变量、配置档、默认值；严格的设置文件，不含机密
- [ ] CLI-101 命名的配置档；`--profile` 绑定进令牌；`auth login`、`auth status`、`auth logout`
- [ ] CLI-102 `<PREFIX>_DEBUG` 的输出行写到 stderr，机密已脱敏；没有 `--verbose` 或 `--debug`

网络

- [ ] CLI-110 超时；读取最多重试两次，写入绝不重发；代理变量；TLS 始终校验，`<PREFIX>_CA_FILE`；User-Agent

本文是 [ai-native-cli-spec](https://github.com/fatecannotbealtered/ai-native-cli-spec) 的一部分。Copyright (c) 2026 Sean Guo。MIT 许可证；见规范仓库中的 `LICENSE`。
