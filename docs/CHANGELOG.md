# CHANGELOG

## v0.8.0 (2026-06-25) — P1-02 through P3-03

- 所有 LLM 请求统一为 SSE streaming；支持 content/reasoning/tool_calls 增量组装、取消、有限重试、截断流和 estimated usage。
- Worklog 改为 `session.md`、append-only `events.jsonl` 与有界 `artifacts/` 的统一管理器。
- Memory 改为同目录 user/project/agent 三文件，修改逐次审批；Skill 增加严格校验、按需资源和 TUI 启停。
- Role 成为 Tool Policy 输入；Plan 升级为可持久化、暂停、恢复、retry/skip/cancel 的状态机。
- 新增无副作用 CLI help 和只读 doctor/--offline。
- 新增集中 Renderer、深红/白 pixel-art Banner、plain/NO_COLOR 退化、TUI `/help` 和 `/tools`。
- 增加 streaming、取消、重试、Worklog 恢复、memory/skill 边界、role 权限与 plain UI 自动化测试。

## v0.7.0 (2026-06-24) — Finite Context Compaction

### Rolling Checkpoint

- Context 达到 60% 时触发 compaction，目标约 45%
- 每个会话最多 3 次 compaction 尝试
- 保留最近 8 个语义组原文
- tool_calls 与全部 tool results 作为不可拆分原子组
- 只维护一个滚动 conversation checkpoint

### Emergency Summary

- 压缩机会用完且 context 达到 90% 时生成一次 *_summary.md
- summary 使用普通 worklog 格式并追加会话摘要
- 摘要 API 失败时使用确定性降级摘要
- TUI 提示用户保存关键信息并使用 /new 开启新会话

### Usage

- 主对话 usage 与 compaction/summary auxiliary usage 分离
- Context Bar 不再被辅助摘要调用覆盖

### Session

- /new 保存当前普通 worklog，并重置 checkpoint、压缩次数和 emergency 状态

## v0.6.0 (2026-06-24) — Agent State Machine + Single Instance

### Agent Loop

- 新增 preparing / calling_llm / executing_tools / finalizing / completed / failed 状态机
- step 和 tool-call 预算改为单请求独立预算
- 单个请求失败后，交互 TUI 可继续处理下一请求
- 增加 nil response、空 choices、空消息和 tool call 参数校验
- /status 与 Context Bar 显示请求数、累计步骤和上次任务状态

### 单实例

- 同一二进制真实路径只允许一个实例运行
- 使用跨平台原子锁目录和 PID 检测，不依赖 flock
- 复制到不同路径的二进制可独立运行

### Windows 退化兼容

- 新增 cmd.exe 命令执行适配
- 新增 Windows PID 存活检测
- 新增 windows/amd64 构建产物
- 首次生成 .env 时按系统写入 readonly 命令白名单

### Context

- 新增 P1-01-a Context Compaction Fix 提案，尚未修改现有压缩代码

## v0.5.0 (2026-06-24) — File Sandbox + Memory Guard

### 文件工具

- read_file 改为 Open + Seek + 有界 buffer 分段读取
- 新增 workspace roots、blocked paths、readonly paths 和绝对路径策略
- 读取和写入解析真实路径，阻止路径穿越和软链接逃逸
- 修复 write_file 相对路径写入
- offset / limit 增加严格参数校验

### 内存保护

- Linux 读取 MemTotal、MemAvailable 和进程 VmRSS
- 文件读取内存比例硬上限为系统总内存的 25%
- 单次读取大小和内存比例均可从 .env 向下调整

### Tool Result

- read_file 二次截断保留尾部 next_offset 元数据

### 系统探测

- 每次启动探测 OS、architecture、Linux distribution、kernel 和 hostname
- 系统信息打印到 profile / TUI，并注入 system prompt
- 命令生成遵循 Linux/POSIX 优先和老系统能力确认原则

## v0.4.0 (2026-06-24) — Runtime Profile + Command Policy

### .env 与运行目录

- 二进制同目录自动加载 .env，不存在时自动生成默认模板
- 自动创建 skills / plans / worklogs 目录
- 启动时打印 profile、model、base_url、脱敏 API key

### 命令策略

- 新增 readonly / autopilot 两种运行模式
- 新增 /mode、/mode readonly、/mode autopilot
- readonly 隐藏 write_file，并限制 run_command 为只读白名单
- autopilot 允许任意命令，危险正则命中时需要 TUI 审批
- 命令超时、最大输出、白名单、危险正则均可通过 .env 配置
- 命令执行增加真实 timeout、进程组终止和有界输出缓冲

### TUI

- Banner、输入提示符、/status、Context Bar 显示当前模式

## v0.3.0 (2026-06-24) — Skill + Memory

### 新增功能

#### Skill 系统 (`skill.go`)
- 启动时扫描 `skills/` + `~/.eliza/skills/` 目录
- YAML frontmatter 格式的 SKILL.md（name/description/version）
- `skill_list` 工具：列出所有可用技能
- `skill_view` 工具：按需加载完整技能内容
- 技能索引注入 system prompt（仅 name + description）
- 3 个内置示例技能：code-review / deploy-check / log-analysis

#### Memory 持久记忆 (`memory.go`)
- `memory` 工具：save / recall / forget
- 两个存储文件：`~/.eliza/user.md`（用户画像）+ `~/.eliza/memory.md`（Agent 笔记）
- 冻结快照模式：会话启动时注入 system prompt，运行时写磁盘
- 自动去重，§ 分隔符

### 改进
- Context bar 颜色编码（绿 <50% / 黄 50-80% / 红 >80%）
- 压缩改用百分比触发（>75%），消息数兜底（>30）
- `autoLoadEnv()` 自动加载同目录 env.txt

### 工具清单（截至 v0.3.0）
read_file / write_file / run_command / skill_list / skill_view / memory

---

## v0.2.0 (2026-06-24) — 功能增强版

### 新增功能

#### Context 压缩 (`compress.go`)
- 消息数超过 30 条时自动触发
- 保护 system prompt + 前 6 条 + 后 10 条不压缩
- 中间消息送给 LLM 做摘要，替换为一条 system 消息
- `/compress` 命令手动触发
- 压缩失败自动降级（不压缩，保证安全）

#### Tool 结果分段读取
- `read_file` 新增 `offset` / `limit` 参数
- 默认 limit=10000，offset=0
- 超限时返回 `[TRUNCATED total=X offset=Y returned=Z remaining=W]` 标记
- LLM 可再次调用 read_file 指定 offset 分段读取

#### Plan Mode (`plan.go`)
- `/plan <描述>` — 调用 LLM 生成 Markdown 执行计划
- 计划写入 `plans/plan_YYYYMMDD_HHmmss.md`
- `/showplan` — 查看计划
- `/execute` — 逐步骤自动执行
- `/cancelplan` — 取消计划

#### Role Alternation (`roles.go`)
- 5 个内置角色：default / coder / ops / writer / security
- `/role` — 列出所有角色
- `/role <name>` — 切换角色（更新 system prompt）
- 切换时保留会话历史

### 修复
- 修复 `content` 字段 `omitempty` 导致 DeepSeek API 400 错误

### 配置
- `env.txt` 重构为 docker-compose environment 格式
- 支持 DeepSeek 官方 API ↔ 内网大模型一行注释切换

---

## v0.1.0 (2026-06-24) — 初始版本

### 基础功能
- OpenAI 兼容 API 对话
- 三个工具：read_file / write_file / run_command
- 危险命令拦截（正则匹配 + 用户确认）
- 工作记录自动生成（Markdown）
- 交互模式 + 单次查询模式
- 纯静态编译，零运行时依赖
- 支持 linux/amd64 + linux/arm64
