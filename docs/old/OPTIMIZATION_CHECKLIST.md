# ELIZA Agent 优化清单

> 日期: 2026-06-24  
> 目标: 将当前原型逐步打磨成适合公司内网使用的轻量级 Agent Runtime。  
> 核心约束: Go 单二进制优先、零第三方运行时依赖、OpenAI-compatible API、适配复杂 Linux 环境。

---

## 总体路线

当前优化分三步推进:

1. 源码可靠性与安全基线优化  
   先修配置、工具执行、文件读写、memory、skill、worklog、agent loop 等核心问题。
2. 导引菜单与 CLI 体验  
   给编译后的 eliza 增加清晰的 -help / --help 输出，以及面向新用户的启动说明。
3. 界面体验优化  
   在核心稳定后，再优化交互 UI、状态栏、菜单、输出样式，为后续转正做准备。

---

## 设计原则

- 内网优先: 默认面向内网 OpenAI-compatible API；公网 DeepSeek 仅作为开发测试 profile。
- 低依赖优先: 不依赖 Python、Node、数据库、Docker、系统包管理器；核心功能只用 Go 标准库。
- Linux 优先、Windows 可退化运行: 工具和命令主要面向 Linux/POSIX；每次启动重新探测 OS、架构、发行版和内核并注入 system prompt。Windows 使用 cmd.exe 适配，不使用 flock 等 Unix 专属机制。
- 可拷贝即用: 每个架构一个静态二进制，旁边可放 .env、skills/、memory/、worklogs/。
- 安全边界显式化: 工具是否能读写文件、执行命令、访问网络，必须由策略控制，而不是只靠提示词。
- 可审计: 关键操作要有日志，但日志不能泄露 API key、敏感文件内容或过多内网信息。

---

## P0 — 上线前必须处理

### P0-01 配置与 env 加载重构

状态: 已按“二进制同目录 .env”方案实施。

问题:

- 当前 env.txt 混合了真实值、示例值和 docker-compose 片段。
- API 地址经常变，需要保留可编辑 env。
- 单文件部署时，需要首次运行自动生成可编辑 .env。

实际方案:

- 只读取二进制同目录 .env，不做 env.txt 兼容。
- 如果 .env 不存在，自动生成默认模板。
- .env 沿用当前 KEY=VALUE + 注释格式。
- ELIZA_BASE_URL / ELIZA_API_KEY / ELIZA_MODEL 从 .env 注入。
- 启动时打印 profile、model、base_url、脱敏后的 api_key。
- skills、plans、worklogs 等运行目录随启动自动创建。

涉及文件:

- main.go
- config.json
- .env
- docs/README.md

验收标准:

- 无 .env 时运行 ./eliza 会在二进制同目录生成 .env。
- API key 仍为占位符时给出清晰错误。
- 启动日志打印脱敏 API key。

---

### P0-02 命令执行安全与稳定性

状态: 已完成第一阶段，两种运行模式和可编辑 .env 策略已实施。

问题:

- run_command 使用 sh -c 执行任意命令。
- timeout 字段存在但未生效。
- 危险操作只靠少量正则，容易绕过。
- 命令输出会先完整读入内存，再由 agent 截断。

建议修改:

- 使用 context.WithTimeout + exec.CommandContext 实现真实超时。
- 增加最大输出字节数限制，超过后截断并标记。
- 增加命令策略层:
  - allow_shell: 是否允许 shell 模式
  - blocked_patterns: 阻断型正则
  - confirm_patterns: 需确认正则
  - network_patterns: curl/wget/nc/ssh/scp 等网络命令单独控制
- 支持只读审计模式: 允许 ls/cat/grep/df/free/ps 等，阻断写操作。
- 命令执行日志记录 exit code、耗时、是否超时、是否被策略阻断。

实际实现:

- /mode readonly: 隐藏 write_file，run_command 仅允许白名单只读命令。
- /mode autopilot: 允许任意命令，危险正则命中时请求 TUI 审批。
- .env 可配置默认模式、命令超时、最大输出、readonly 白名单和危险正则。
- 使用 context timeout 和进程组终止，避免超时命令继续残留。
- stdout/stderr 使用有界缓冲区，超过上限返回截断标记。
- TUI Banner、输入提示符、/status、Context Bar 显示当前模式。

涉及文件:

- tools.go
- main.go
- worklog.go
- docs/features/

验收标准:

- sleep 999 会在超时后终止。
- 输出超限时不会占满内存。
- 明显危险命令被阻断或要求确认。
- worklog 能记录命令退出状态。

---

### P0-03 文件工具沙箱与大文件处理

状态: 已实施路径沙箱、流式分段读取和 25% 系统内存硬上限。

问题:

- read_file 当前先整文件读入内存，再做 offset/limit。
- write_file 对相对路径如 foo.txt 的父目录处理有 bug。
- limit < 0 等异常参数可能导致 panic。
- 文件工具没有 workspace 边界，能读写进程权限覆盖范围内的任意路径。

建议修改:

- 使用 os.Open + Seek + Read 分段读取，避免整文件载入内存。
- 校验 offset >= 0、limit > 0，并设置最大 limit。
- 修复相对路径写入。
- 新增文件策略:
  - workspace_roots
  - readonly_paths
  - blocked_paths
  - allow_absolute_path
- 对软链接解析后的真实路径做边界检查。

实际实现:

- read_file 使用 Open + Seek + 有界 buffer，不再整文件读取。
- offset / limit 严格校验，单次读取受 .env 最大字节数限制。
- Linux 读取 MemTotal、MemAvailable、进程 VmRSS，文件读取不得使进程超过系统内存的 25%。
- workspace roots、blocked paths、readonly paths、绝对路径开关均可通过 .env 修改。
- 读取和写入都会解析软链接真实路径，防止路径穿越和 symlink escape。
- write_file 已修复相对路径写入，并使用 canonicalized 父目录。
- agent 截断 read_file 结果时保留尾部 next_offset 元数据。

涉及文件:

- tools.go
- config.json
- docs/features/tool-segmented-read.md

验收标准:

- 能写入当前目录下的相对文件。
- 超大文件读取只返回指定片段。
- 负数 offset/limit 不 panic。
- 被禁止路径返回明确错误。

---

### P0-04 API key 与日志脱敏

状态: 暂缓。银行业脱敏规则需要业务与开发共同确认后再实施。

问题:

- worklog 记录 API 端点，未来可能记录更多敏感信息。
- tool 参数、用户 query、命令内容都可能包含 key、token、password。

建议修改:

- 增加 RedactSecrets 工具函数。
- 对 worklog 中的 query、命令、路径、API endpoint 做基础脱敏。
- 不记录完整 Authorization、API key、形如 sk-* 的 token。
- 增加配置项 worklog.redact = true。

涉及文件:

- worklog.go
- main.go
- docs/README.md

验收标准:

- worklog 不出现完整 key/token。
- 用户输入包含 password=xxx 时，日志中显示为 password=***。

---

## P1 — 核心能力完善

### P1-01 Agent Loop 状态机化

状态: 已实施单二进制单实例和单请求状态机。

问题:

- turnCount 是全局累计，长交互后可能耗尽。
- 每次用户请求和每轮工具调用没有独立 step budget。
- LLM 返回空 choices 时可能 panic。

建议修改:

- 区分 session turn、request step、tool call count。
- 每个用户请求单独限制最大 agent steps。
- LLM response 做完整校验: choices 为空、message 为空、tool call 参数非法都要优雅返回。

实际实现:

- 同一二进制路径通过跨平台原子锁目录限制为单实例；复制到另一条路径可独立运行。
- 每个用户任务创建独立 RequestRuntime 状态机。
- step / tool-call 预算只限制当前任务，不限制整个交互会话。
- 当前任务失败后 TUI 继续接收下一请求。
- 校验 nil response、空 choices、空 message 和 tool call 字段。
- 非法 tool arguments 不执行，作为错误 tool result 返回模型。
- /status 和 Context Bar 显示 request、step 和上次任务状态。

### P1-01-a Context Compaction Fix

状态: 已按最终规则实施。

提案摘要:

- 使用 token budget，不再使用消息条数作为主要触发依据。
- assistant tool_calls 与全部 tool results 作为不可拆分原子组。
- 先确定性裁剪旧工具输出，再生成结构化 rolling checkpoint。
- 保留最近 8 个语义组原文，60% 触发，目标 45%。
- 每个会话最多 3 次 compaction 尝试，不无限压缩或无限重试。
- 压缩机会用完且 context 达到 90% 时生成 *_summary.md 并在 TUI 警告。
- compaction usage 与主对话 usage 分开，避免污染 Context Bar。
- /new 保存普通 worklog 并重置 checkpoint、压缩计数和 emergency 状态。
- 详细实现见 docs/features/context-compaction-proposal.md。

涉及文件:

- agent.go
- llm.go

验收标准:

- 单次请求达到 step 上限后，交互会话仍可继续。
- 异常 API 响应不会 panic。

---

### P1-02 LLM Client 健壮性

状态: 已完成（2026-06-25）。统一 SSE streaming、增量组装、取消、有限重试、URL 规范化、截断流诊断和 usage 估算均已实施。

目标:

- LLM 的所有正文响应统一采用 streaming 模式，不保留非流式输出路径。
- 在 OpenAI-compatible API 差异、网络抖动和用户中断下保持可恢复、可诊断。

问题:

- 当前没有 retry/backoff 和完整的 request cancellation。
- base URL 拼接较脆弱。
- 流式与非流式并存会形成两套解析、工具调用和错误处理路径，增加维护成本。
- SSE 可能出现半包、空行、未知字段、错误事件、连接中断和未收到 [DONE] 等情况。

最终方案:

- 所有对话、工具调用续轮、context compaction 和 summary 请求都使用 stream=true。
- 删除非流式 chat completion 的实现、配置开关和回退逻辑；服务端不支持 streaming 时直接给出兼容性错误。
- 使用统一的 SSE 增量解析器，正确拼接 assistant content、reasoning 字段和 tool_calls arguments。
- 只有在一个 assistant message 完整结束后，才把它提交给 Agent Loop 和 Worklog；TUI 可以实时展示增量文本。
- 使用 context cancellation 支持超时、Ctrl+C 和当前请求取消，取消后不得继续执行尚未开始的工具。
- 对连接建立阶段的 429、408 和 5xx 做有限指数退避并加入抖动；一旦已经向用户输出正文或收到 tool call 增量，不自动重放请求，避免重复执行。
- 使用 strings.TrimRight(baseURL, "/") 规范拼接 /chat/completions。
- 请求超时、连接超时、最大重试次数和退避上限通过 .env 配置。
- API 错误、非 SSE 响应、截断流和无效 tool call 参数返回明确错误，不 panic。
- usage 缺失时允许 Context Bar 显示估算值，并明确标记 estimated。

涉及文件:

- llm.go
- agent.go
- main.go
- .env

验收标准:

- 代码中不存在非 streaming 的 LLM 请求分支。
- 普通回答和 tool_calls 都能边接收边展示并正确组装。
- base URL 带或不带末尾 / 都能正常请求。
- 用户取消后流立即关闭，后续工具不会误执行。
- 建连前临时失败会有限重试；已开始输出的请求不会自动重放。
- 流在中途损坏时给出清晰错误，已收到内容可在 Worklog 中标记为 incomplete。

---

### P1-03 Worklog 统一管理对话与工具调用

状态: 已完成（2026-06-25）。已实施 session.md + events.jsonl + artifacts/ 的单管理器事件流。

设计结论:

不把完整对话和工具审计记录硬塞进同一个文件，也不建立两套互不关联的日志。采用“一个 Worklog 管理器、一个会话目录、两种表示”的混合方案:

    worklogs/YYYY-MM-DD/session_<session_id>/
      session.md
      events.jsonl
      artifacts/

- session.md: 面向用户阅读，保存会话元数据、完整的用户/ELIZA 对话、关键状态变化和最终 summary。
- events.jsonl: 面向审计和程序恢复，使用 append-only 事件记录全部操作。
- artifacts/: 保存超过事件大小上限的工具输出或其他大结果，events.jsonl 只记录相对路径、大小和校验哈希。
- 三者共享 session_id、request_id、event_id 和 tool_call_id，可从对话追到工具，也可从工具追到触发它的对话。

统一记录范围:

- 用户输入和最终组装完成的 ELIZA 回复；streaming token 不逐 token 落盘，避免日志膨胀。
- LLM 请求开始、完成、取消、重试、usage 和 incomplete 状态。
- 工具名、规范化参数摘要、审批结果、开始/结束时间、耗时、exit code、timeout、error 和截断状态。
- TUI 命令、mode/role 切换、策略允许/拒绝/需审批结果。
- plan 创建、步骤状态变化、重试、跳过、取消和恢复。
- memory 修改申请、用户审批结果和实际写入结果。
- skill 索引刷新、加载、拒绝和校验错误。
- compaction、checkpoint、summary 生成和 90% 阈值告警。
- 启动 profile、OS/arch、版本和配置来源；API key、Authorization 等认证值不得进入事件正文。

实现约束:

- Worklog 是唯一日志写入入口；工具、memory、skill、plan 不再维护各自孤立的私有日志。
- 会话开始即创建目录并追加写入，每个关键事件及时 Flush；不能只在正常退出时一次性保存。
- 每行 JSONL 都包含 schema_version、timestamp、sequence、session_id、request_id、event_id、event_type、status 和 payload。
- 事件按单调 sequence 排序；异常退出后能够识别最后一个完整事件并恢复可读历史。
- session.md 由同一事件流持续投影生成，不作为第二套事实来源。
- 单条事件和单个 artifact 均设大小上限；大输出流式写入，不能先整体载入内存。
- P0-04 实施前先预留 sensitivity、redaction_status 等字段；具体银行业脱敏规则仍按 P0-04 后续评审执行。
- /new 正常结束旧会话并创建新 session_id；崩溃重启不得覆盖旧目录。
- 普通运行日志不写 ANSI 颜色或界面控制符。

涉及文件:

- worklog.go
- agent.go
- tools.go
- memory.go
- skill.go
- plan.go

验收标准:

- read_file、write_file、run_command 的成功、失败、拒绝和审批都能准确记录。
- session.md 能按顺序阅读完整对话，events.jsonl 能重建工具和状态时间线。
- 任一工具事件都可关联到触发它的 request 和 assistant tool_call。
- 强制终止进程后，已完成事件仍存在且 JSONL 最后一行不破坏此前记录。
- 超大工具输出进入 artifacts/，主进程内存和日志文件单条记录均受限。
- 同一操作不会被多个日志模块重复或矛盾地记录。

---

### P1-04 Memory 可信边界重构

状态: 已完成（2026-06-25）。三个同目录 memory 文件、加载上限、不可信边界和逐次审批已实施。

目标:

首次运行时在二进制同目录的 memory/ 下创建以下三个初始文件:

- user.md
- project.md
- agent.md

授权边界:

- 文件仅在不存在时创建，已有文件永不被初始化逻辑覆盖。
- 初始模板只包含用途说明和空白内容，不自动推断或写入用户信息。
- Agent 可以读取并引用 memory，但未经用户明确授权不得新增、覆盖、删除或改写任何内容。
- save、forget、覆盖和批量整理都视为修改；每次修改前在 TUI 展示目标文件、精确内容或 diff，并取得本次操作的明确批准。
- 用户拒绝或取消后不得写入，也不得在后续轮次沿用旧批准。
- agent.md 同样受保护，Agent 不得以“自我优化”为理由自行修改。
- 非交互 -q 模式默认禁止 memory 修改；未来如需自动化，必须另行设计显式授权机制。
- 用户直接用编辑器修改这些文件不受 Agent 审批限制，下一次读取或显式 reload 后生效。

可信边界与加载规则:

- memory 是不可信的参考资料，优先级低于 system prompt、mode、role 和 Tool Policy，不能借助文件内容提升权限。
- 三个文件分别注入清晰的来源标签和边界标签，禁止把其中的命令文本当系统指令执行。
- 每个文件和总注入量均设置上限；超限时仅加载受控片段并在 TUI/Worklog 明确提示。
- 所有修改申请、批准、拒绝和落盘结果都交由 Worklog 记录。
- memory 目录路径以二进制所在目录为基准，保持单二进制加同目录数据的部署方式。

涉及文件:

- memory.go
- agent.go
- main.go
- worklog.go
- docs/features/memory-tool.md

验收标准:

- 首次运行生成 memory/user.md、memory/project.md、memory/agent.md。
- 再次启动不会覆盖三个文件的已有内容。
- 未经 TUI 授权的任何 memory 修改都失败且文件哈希不变。
- 授权界面展示的内容与最终写入内容完全一致。
- /mode autopilot 也不能绕过 memory 审批。
- memory 中的越权指令不能覆盖 mode、role 或工具策略。

---

### P1-05 Skill 系统可控加载

状态: 已完成（2026-06-25）。本地索引、按需加载、校验隔离、启停与 Worklog 事件已实施。

目标:

Skill 作为本地可扩展模块，由用户自行添加和维护，不引入外部运行时、远程市场或自动下载。

最终方案:

- 首次运行时在二进制同目录创建 skills/；仅创建空目录，不自动写入示例 skill，不打包外部 skill。
- 一个 skill 使用独立子目录和 SKILL.md；可选 references/、scripts/、assets/ 作为该 skill 的本地资源。
- 启动时只扫描元数据并构建轻量索引，不把全部 SKILL.md 和 references 注入 context。
- 模型明确选择某个 skill 后再加载完整 SKILL.md；关联资源按需读取并受文件沙箱、大小限制和 25% 内存规则约束。
- 校验目录穿越、软链接逃逸、SKILL.md 大小、frontmatter、名称唯一性和允许的资源类型；错误 skill 隔离，不影响其他 skill 或主程序启动。
- 通过 .env 配置全局启用、禁用名单、单文件上限和总索引上限。
- 增加 TUI 的 /skills、/skills reload、/skills enable <name>、/skills disable <name>；启停只影响加载策略，不修改用户的 skill 文件。
- role 切换后重建可见 skill 索引，但不得突破当前 role、mode 和 Tool Policy。
- Agent 不得自行创建、改写、删除、联网安装或同步 skill；这些扩展由用户在二进制同目录手工维护。
- skill 内容视为不可信扩展指令，不能覆盖 system prompt、安全策略、memory 审批和工作区限制。
- skill 加载、拒绝、刷新和校验错误统一写入 Worklog。

涉及文件:

- skill.go
- agent.go
- roles.go
- main.go
- worklog.go
- docs/features/skill-system.md

验收标准:

- 单独复制 eliza 后首次运行会创建空的 skills/ 目录。
- 用户放入合法 skill 后，/skills reload 无需重启即可发现。
- /role coder 等角色切换后仍能看到被允许的 skill index。
- 超大、损坏或越界 skill 会被拒绝并给出具体原因，不会静默截断。
- 禁用某个 skill 后模型拿不到其正文和资源。
- skill 无法提升工具权限，也不会被 Agent 自行修改。

---

### P1-06 Role 与 Tool Policy 绑定

状态: 已完成（2026-06-25）。模型侧过滤和执行层复核均使用 mode + role 权限交集。

目标:

Role 不再只是提示词，而是 Tool Policy 的一个真实输入；最终权限取全局安全策略、运行 mode、role 和单次审批的交集。

角色默认策略:

| Role | 默认能力 | 默认限制 |
|---|---|---|
| default | 当前 mode 允许的常规工具 | 受全部全局策略约束 |
| coder | 文件读写、受控命令、代码相关 skill | 不得绕过 workspace 和危险命令审批 |
| ops | 系统诊断和受控运维命令 | 写操作与危险操作仍需策略判断/审批 |
| writer | 文件读取、经批准的文档写入 | 默认不暴露 run_command |
| security | 文件读取、只读诊断 | 强制只读，不暴露 write_file 和写命令 |

实现规则:

- 每次调用 LLM 前按当前有效权限过滤 tool definitions，模型看不到无权使用的工具。
- 每次真正执行工具时再次由统一 Tool Policy 校验，不能只信模型侧过滤。
- 权限计算遵守“只收紧、不放宽”: role 不能把 readonly 提升为 autopilot，skill 和 prompt 也不能提权。
- /mode 切换后立即重算当前 role 的有效权限并在 TUI 显示变化。
- /role 列表展示角色说明、可用工具和限制；切换结果写入 Worklog。
- 未知 role、无权工具和策略冲突返回结构化拒绝原因，不执行工具。
- Tool Policy 集中实现，避免 read_file、write_file、run_command 各自复制不一致的权限逻辑。

涉及文件:

- roles.go
- tools.go
- agent.go
- worklog.go

验收标准:

- /role security 后模型拿不到 write_file，命令执行层也拒绝写操作。
- /role writer 默认拿不到 run_command。
- readonly + coder 仍然只能执行只读能力。
- role 切换时 tool definitions、TUI 状态和实际执行权限保持一致。
- 伪造 tool call 或 skill 指令无法绕过执行层策略。

---

### P1-07 Plan Mode 状态追踪

状态: 已完成（2026-06-25）。Plan/Step 状态机、原子持久化、暂停恢复、retry/skip/cancel 已实施。

目标:

把 Plan Mode 从“把 Markdown 注入 system message”升级为可暂停、可恢复、可审计的执行状态机。

状态模型:

- Plan: draft、ready、running、paused、completed、failed、cancelled。
- Step: pending、running、completed、failed、skipped、cancelled。
- 每个 plan 和 step 都有稳定 ID、创建/更新时间、尝试次数和最后结果摘要。
- 同一 ELIZA 会话同一时间只允许一个 active plan。

实现规则:

- /plan <任务描述> 先生成结构化步骤并保存为 draft，用户确认后进入 ready。
- /execute 从首个 pending 步骤开始；执行前把步骤设为 running，结束后原子更新状态和结果。
- 每步完成后立即持久化 plan 文件并写 Worklog 事件，不能等整个计划结束后统一保存。
- 步骤失败时自动进入 paused，不盲目继续；TUI 提供 retry、skip、cancel 三种明确选择。
- retry 增加尝试次数并保留历史结果；skip 需要用户确认；cancel 保留已完成步骤和取消原因。
- /showplan 展示当前状态、进度、正在执行的 step 和失败原因。
- /execute 可恢复 paused/ready 计划；已 completed/cancelled 的计划不可误执行。
- /cancelplan 取消当前 plan，但不删除计划文件和审计记录。
- /new 前提示仍有 active plan；用户可选择保留供下个会话恢复或明确取消。
- plan 的工具执行仍受 mode、role、Tool Policy、memory 审批和危险命令审批约束。
- 计划文件是状态投影，Worklog events.jsonl 是执行审计时间线；二者通过 plan_id/step_id 关联。

涉及文件:

- plan.go
- agent.go
- tools.go
- worklog.go

验收标准:

- /execute 后 plan 和 step 状态会按实际结果变化。
- 任一步失败都会暂停，未经用户选择不会执行下一步。
- 进程退出再启动后可识别 paused plan 并继续。
- retry/skip/cancel 都保留历史记录。
- 已完成步骤不会在恢复时重复执行。
- plan 不能绕过工具或 memory 的审批边界。

---

## P2 — CLI 导引菜单与帮助系统

### P2-01 -help / --help 导引菜单

状态: 已完成（2026-06-25）。三种 help 别名、CLI/TUI 分区、早返回和未知参数提示已实施。

目标:

让编译后的 ./eliza -help、./eliza -h 和 ./eliza --help 输出一致，并明确区分“系统终端命令”和“进入 TUI 后的交互命令”。

系统终端 / CLI 部分:

- ./eliza: 启动交互 TUI。
- ./eliza -q "问题": 执行一次 streaming 查询后退出。
- ./eliza -c <path> 或 --config <path>: 指定配置文件，如保留该入口。
- ./eliza -m <model> 或 --model <model>: 临时覆盖模型。
- ./eliza -v 或 --version: 输出版本。
- ./eliza doctor 或 ./eliza --doctor: 执行只读自检。
- ./eliza -h、-help、--help: 输出帮助后退出。
- .env 不是命令参数；程序自动读取二进制同目录 .env，不存在时在正常启动流程中生成默认模板。

TUI 交互命令部分:

- /help
- /status
- /clear
- /new
- /mode [readonly|autopilot]
- /role [name]
- /plan <任务描述>
- /showplan
- /execute
- /cancelplan
- /compress
- /tools
- /skills 及其 reload/enable/disable 子命令
- /memory
- exit、quit、/q、/exit

输出规则:

- 两部分使用醒目的纯文本标题，例如“系统终端命令”和“TUI 交互命令”。
- 每项展示语法、用途和必要的安全说明；不要把 TUI 命令伪装成 shell 命令。
- 说明配置优先级和二进制同目录 .env、skills/、memory/、worklogs/、plans/ 的位置。
- 帮助路径必须在配置加载和运行时初始化之前返回：不生成 .env，不创建目录/锁，不访问 API，不写 Worklog。
- 帮助输出不使用 emoji；支持无颜色管道输出。
- 未知参数给出简短错误、最接近的正确用法和非零退出码。

涉及文件:

- main.go
- docs/README.md

验收标准:

- -h、-help、--help 的内容和退出码一致。
- 输出中系统终端命令与 TUI 命令严格分区。
- 新目录中执行 --help 不产生任何文件或网络请求。
- 帮助列出的参数和交互命令均与实际实现一致。
- 输出在窄终端和重定向到文本文件时仍清晰可读。

---

### P2-02 doctor / 自检命令

状态: 已完成（2026-06-25）。doctor/--doctor、--offline、分级输出、稳定退出码和只读探测已实施。

目标:

增加 ./eliza doctor 和 ./eliza --doctor，用一次只读、可重复的检查快速定位复杂内网环境中的部署问题。

检查项:

- 二进制版本、Go 构建信息、OS、架构、内核/发行版、工作目录和二进制目录。
- 同目录 .env 是否存在、是否可读、必需字段是否存在、API key 是否仍是占位符；只显示脱敏值。
- base URL 格式、DNS/TCP/TLS/HTTP 可达性，使用短超时且不发起 chat completion。
- model、streaming 强制状态、请求超时和重试配置。
- 默认 mode、命令策略正则、readonly 白名单、文件沙箱、25% 内存规则和 compaction 阈值是否合法。
- 单实例锁状态及残留锁识别，不长期占用正常启动锁。
- 默认 shell/命令解释器是否可用；Linux 关键 /proc 指标能否读取，Windows 回退能力是否可用。
- skills/、memory/、worklogs/、plans/ 是否存在以及现有路径的读写权限。
- memory 三个文件和 skill 索引是否可解析，但不加载其内容执行任何指令。

只读约束与输出:

- doctor 不创建 .env 或目录，不修改配置/memory/skill/plan，不写普通 Worklog，不启动 TUI。
- 网络检查有独立短超时；失败应区分 DNS、连接、TLS、HTTP 状态和认证问题。
- 输出使用对齐的 PASS / WARN / FAIL 文本，不使用 emoji。
- API key、Authorization 和敏感配置只允许脱敏展示。
- 建议退出码: 0 表示全部通过，1 表示存在警告，2 表示存在阻断性失败。
- 提供 --offline 跳过网络探测，以便完全隔离环境先检查本地部署。

涉及文件:

- main.go
- llm.go
- config.go
- skill.go
- memory.go
- tools.go

验收标准:

- 新机器运行 doctor 能定位配置、权限、网络或系统兼容问题。
- 连续运行两次不改变任何文件内容、时间戳或目录结构。
- 无网络时不会长时间卡住，错误分类明确。
- 输出中不存在完整 API key。
- doctor 的退出码可被部署脚本稳定判断。

---

## P3 — 界面与交互体验优化

### P3-01 启动界面与 Banner 优化

状态: 已完成（2026-06-25）。集中 Renderer、标准粉色主题、高精度 Braille 点阵少女、自适应宽窄屏 Banner 和纯文本退化已实施。

视觉方向:

- 整体沿用项目定义的深红、粉色和白色主色；人物只使用集中定义的肤色、阴影、制服和电脑辅助色，不引入 emoji。
- 少女基于 `docs/logo.png` 转为 40×20 Braille 单元（80×80 可表达点）的高精度 8-bit 点阵，保留光环、粉色双马尾、制服和老式笔记本电脑。
- 不显示占据大量纵向空间的顶部 ELIZA-AGENT 大字标；原有标题与署名文字保持不变。
- Logo 使用编译进二进制的 ANSI/Unicode 字符画，不依赖外部图片、字体、终端图形协议或运行时资源。
- ANSI 256 色终端优先使用深红色和白色；不支持颜色或 Unicode 时自动切换纯 ASCII 紧凑版。
- 110 列及以上终端在同一边框内左侧展示启动参数、右侧展示完整少女；40–109 列终端将完整少女居中置顶，下方使用无右侧预留区的独立参数框；更窄终端省略点阵以避免裁切和溢出。

启动信息排版:

- Logo 下方使用固定标签宽度对齐 version、profile、model、base_url、api_key、mode、role、OS/arch、skills、memory、worklog 和 workspace。
- API key 继续仅显示前后少量字符，中间用 *** 脱敏。
- endpoint 展示 host 和必要路径，不打印 Authorization。
- 最后一行提供纯文本提示: 输入 /help 查看交互命令，输入 /status 查看当前状态。
- 根据终端宽度自适应；无法可靠取得宽度时使用保守紧凑布局。
- Banner 只描述当前状态，不替代错误信息或安全审批。

兼容性:

- 支持 NO_COLOR、--no-color、--plain 和 TERM=dumb。
- 非 TTY、输出重定向或 plain 模式不输出 ANSI 控制码和大 Logo，只输出紧凑、对齐的启动信息。
- Windows 不支持 ANSI 时自动退化，不因渲染失败影响核心功能。
- Logo 和颜色常量集中定义，业务输出不得散落硬编码 ANSI 序列。

涉及文件:

- agent.go
- main.go
- ui.go 或等价的集中界面模块

验收标准:

- 宽终端可在参数框右侧看到高精度 Braille 点阵少女，顶部不再显示大字标。
- 80 列及更窄终端不会截断参数值或破坏输入区域。
- 所有参数标签视觉对齐，深红/白配色一致。
- --plain 和重定向输出完全不含 ANSI escape。
- 不支持 Unicode/颜色的终端仍能正常启动和交互。

---

### P3-02 交互内 /help

状态: 已完成（2026-06-25）。TUI 专用分组帮助、状态展示、/tools、/skills、/memory 和未知命令拦截已实施。

目标:

/help 只解释当前 TUI 中可执行的命令，并根据当前 mode、role 和功能状态显示真实可用性。

建议修改:

- 按“会话”“安全与角色”“计划”“工具”“扩展与记忆”“退出”分组展示。
- 覆盖 /status、/clear、/new、/mode、/role、/plan、/showplan、/execute、/cancelplan、/compress、/tools、/skills、/memory 和退出命令。
- /tools 展示当前 mode + role 交集后的可用工具、被禁用工具和简短原因。
- /skills 展示索引、启用状态和校验错误；/memory 只展示文件状态、大小和授权规则，不直接泄露全部内容。
- 命令参数使用明确占位符并给出最短示例。
- 顶部显示当前 mode、role、active plan 和 streaming 状态。
- CLI 参数不混入命令列表，只在底部提示“系统终端用法请退出后运行 ./eliza --help”。
- 输出遵循深红/白主题，无颜色模式使用纯文本分组，不使用 emoji。
- 未知 /command 时提示运行 /help，并给出相近命令，但不交给 LLM 当普通对话处理。

涉及文件:

- agent.go
- roles.go
- plan.go
- skill.go
- memory.go
- ui.go 或等价模块

验收标准:

- /help 内容只包含 TUI 命令，且与当前程序实际支持一致。
- readonly/security 等状态下能准确反映受限工具。
- 窄终端、--plain 和无色环境均可读。
- 未知斜杠命令不会被发送到 LLM。

---

### P3-03 输出样式统一

状态: 已完成（2026-06-25）。集中样式、单一身份圆点、stderr 诊断、plain/NO_COLOR 与中文宽度处理已实施。

视觉规范:

- 系统默认不使用 emoji。
- ELIZA 消息使用一个深红色圆点: ● ELIZA。
- 用户消息使用一个白色圆点: ● USER。
- 两者使用同一个圆点字形，只通过颜色区分；无色模式保留 ELIZA / USER 文本标签，避免失去身份信息。
- 主界面、状态栏、审批框、帮助和错误信息只使用深红与白；必要的严重级别通过文字而不是新增颜色表达。

输出规则:

- 建立集中 renderer/theme，统一标题、标签、消息、工具调用、审批、警告、错误和 Context Bar 的样式。
- PASS、WARN、FAIL、RUNNING、BLOCKED 等状态使用对齐的纯文本标签，不使用图标或 emoji。
- streaming 内容只在消息开头打印一次身份圆点，后续 chunk 连续追加，避免重复前缀和界面闪烁。
- 工具调用以紧凑块展示 name、status、duration、exit、truncated；默认不把巨大原始输出直接刷满 TUI。
- 错误包含稳定错误类型、简短说明和可执行的下一步，不打印 Go panic/stack trace 给普通用户。
- stdout 用于正常回答和用户请求的结果；stderr 用于诊断、警告和错误，便于 shell 重定向。
- 支持 --no-color、--plain、NO_COLOR、TERM=dumb 和非 TTY 自动退化。
- Worklog、summary、JSONL 和管道输出永远不写 ANSI 控制符。
- 输入提示、Context Bar 和多行回答保持列宽计算一致，正确处理中文宽字符。

涉及文件:

- agent.go
- contextbar.go
- main.go
- worklog.go
- ui.go 或等价模块

验收标准:

- 默认界面只有深红、白两种主色，不出现 emoji。
- 每条 ELIZA/用户消息分别只出现一个红点/白点身份标记。
- streaming、工具调用和审批插入后，消息前缀不重复、不串行。
- --plain 与日志文件中无 ANSI 控制符。
- 中文、英文和窄终端下对齐稳定。
- 所有错误样式和退出码在交互/非交互模式中保持一致。

---

## 后续暂缓项

这些能力等核心稳定后再做:

- 无头浏览器搜索。
- 多模型路由。
- 多 Agent 并行。
- Web UI。
- 插件市场、远程 skill 下载或自动同步。
- 企业微信、Telegram 等外部通信渠道；当前目标明确排除，不纳入核心路线。

---

## 剩余改进项建议实施顺序

下一轮 Goal 建议按依赖关系一次推进:

1. P1-02 LLM Client 全 streaming 化与健壮性。
2. P1-03 Worklog 统一事件模型和会话目录。
3. P1-04 Memory 授权边界。
4. P1-05 Skill 可控加载。
5. P1-06 Role 与 Tool Policy 绑定。
6. P1-07 Plan Mode 状态机。
7. P2-01 CLI help 与 P2-02 doctor。
8. P3-01 至 P3-03 统一界面与交互。

每完成一项都应运行 Go 单元测试、go test ./...、go vet ./...，并用编译后的 eliza 在 Linux 优先、Windows 退化路径下做对应验收。
