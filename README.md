# ELIZA-AGENT

单二进制 · 零依赖 · 拷走即用

v0.9.0 · CGO_ENABLED=0 · 内置无头 Chromium 浏览器 · 首个稳定版本

---

## 近期更新

**2026-07-01 · v0.9.0 首个稳定版本**

- 可选择式审批框：危险操作用 `↑/↓` 键选择 + `Enter` 确认，替代 `/approve` `/deny` 斜杠命令。默认拒绝，批准只对本次操作生效；拒绝时可补充新要求让 ELIZA 调整方案。
- 输入体验全面升级：bracketed paste 协议支持、多行粘贴自动折叠为临时文件、中文输入法即时显示、统一用 `golang.org/x/term` 替代平台分叉实现。
- 新增 `view_image` 视觉理解工具，支持 Gemini / OpenAI 双后端。
- 修复审批框重绘残留、chromedp context 派生导致浏览器连接取消、输入刷屏等问题。详见 `docs/fix_v0.9.0.md`。

**2026-06-30 · 无头浏览器**

- 基于 Go chromedp 新增 7 个无头浏览器工具：`browser_open`、`browser_snapshot`、`browser_click`、`browser_type`、`browser_screenshot`、`browser_reset`。
- 零外部运行时依赖（无 Node / Python / Playwright）。Chromium 本体可选，解压到 `~/eliza/tools/` 即自动激活。

**更早更新见 `docs/CHANGELOG.md`。**

---

## 1. 项目简介

ELIZA Agent 是一个面向企业内网的 CLI AI Agent，用 Go 编写，CGO_ENABLED=0 静态编译。一个可执行文件，拷到目标机器上直接跑，不需要 Docker、Python、Node 或任何包管理器。

适用场景：信创服务器运维、内网开发辅助、离线环境代码审查、国产化替代方案中的智能运维组件。

核心特点：

- **纯静态编译。** 一个 ELF/PE 文件，不链任何 C 库。拷贝即用，即使目标机器没有包管理器、glibc 版本老、安全策略封禁外网都无影响。
- **安全边界在代码层。** 危险命令用正则硬匹配拒绝，不靠 system prompt 寄希望于 LLM 自觉。`rm` 不在只读白名单里，代码直接拦，LLM 说什么都没用。autopilot 模式放宽命令范围，但正则命中仍弹出本地审批框。
- **不可信数据有明确边界。** Memory 和 Skill 注入 system prompt 时标注 UNTRUSTED SOURCE。LLM 可读取参考，不能提权，不能改 mode，不能绕过审批。
- **完整审计链。** 每次对话生成 `session.md` + `events.jsonl`，API key 自动脱敏。

预编译二进制覆盖 linux/darwin/windows × amd64/arm64 六个平台，`binaries/` 目录直接取用。

---

## 2. 快速开始

### 2.1 编译

```bash
cd ELIZA_Agent

# 编译当前平台
make build

# 编译全部 6 个平台到 binaries/
make build-all

# 运行测试
go test ./cmd/eliza/
go vet ./cmd/eliza/
```

### 2.2 预编译二进制

`binaries/` 目录提供六个平台的静态二进制，拷走即用：

```bash
# Linux amd64
./binaries/eliza-linux-amd64 --version

# Linux arm64
./binaries/eliza-linux-arm64 --version

# macOS Apple Silicon
./binaries/eliza-darwin-arm64 --version

# macOS Intel
./binaries/eliza-darwin-amd64 --version

# Windows x64
binaries\eliza-windows-amd64.exe --version
```

### 2.3 首次运行

```bash
# 首次运行自动生成 .env，编辑 API 配置后重新运行
./binaries/eliza-linux-amd64

# 编辑 .env 填入内网 LLM 端点
# ELIZA_BASE_URL=https://your-internal-api/v1
# ELIZA_API_KEY=sk-xxx
# ELIZA_MODEL=deepseek-chat

# 再次运行进入交互 TUI
./binaries/eliza-linux-amd64
```

### 2.4 命令行模式

```bash
# 单次查询
./eliza -q '磁盘使用情况'

# 自检（含浏览器状态、网络连通性）
./eliza doctor

# 离线自检（跳过网络探测）
./eliza doctor --offline

# 纯文本模式（无 ANSI 颜色）
./eliza --plain -q '列出 /etc 下最大的 5 个文件'

# autopilot 模式 + 浏览器查询
ELIZA_MODE=autopilot ./eliza -q 'browser_open https://www.example.com 告诉我标题'
```

### 2.5 无头浏览器部署

浏览器能力由 Go 内置 chromedp 控制层提供，Chromium 本体是可选资产。不需要 Node、Python、Playwright 或 Puppeteer。

**在外网机器下载 Chromium：**

```bash
# chrome-headless-shell（约 114M，推荐）
wget https://storage.googleapis.com/chrome-for-testing-public/150.0.7871.24/linux64/chrome-headless-shell-linux64.zip
```

**部署到 ELIZA 运行机器：**

```bash
# 解压到 ~/eliza/tools/
mkdir -p ~/eliza/tools/chrome-headless-shell
unzip chrome-headless-shell-linux64.zip -d ~/eliza/tools/chrome-headless-shell/

# 验证
./eliza doctor | grep browser
# 期望: PASS  browser 检测到 Chromium ...
```

内网部署时将 chrome-headless-shell 压缩包和 eliza 二进制一同拷入，按上述步骤解压。ELIZA 启动时自动扫描并激活浏览器工具。找不到 Chromium 时仅禁用浏览器工具，其他功能不受影响。

---

## 3. 架构

### 3.1 组件拓扑

```
main.go ──► agent.go ──► llm.go (SSE streaming)
 CLI入口     状态机Loop    取消/重试
                │
   ┌────────────┼────────────┬────────────┬────────────┐
   ▼            ▼            ▼            ▼            ▼
tools.go     skill.go    memory.go    vision.go   browser.go
双层Policy   按需加载     审批边界     图像理解    无头浏览器
                │
          approval.go (↑↓ 选择审批框)
```

### 3.2 数据流

1. 每个用户请求创建独立的 `RequestRuntime` 和可取消 context。
2. 根据 global policy + mode + role 过滤 LLM 可见的 tool definitions。
3. LLM 请求强制 `stream=true`，SSE 增量组装 content、reasoning 与 tool calls。
4. TUI 实时展示正文；完整 assistant message 才提交 history 和 Worklog。
5. 工具执行前再次检查统一 Tool Policy；需要审批的调用使用一次性批准。
6. 工具结果加入 history 后继续 streaming 轮次，直到最终正文、预算耗尽、失败或取消。
7. 所有事件同步追加到统一 Worklog；大输出进入 artifacts。

### 3.3 安全模型

```
effective permission = global safety ∩ mode ∩ role ∩ one-time approval
```

| 层次 | 机制 | 说明 |
|------|------|------|
| Global Safety | 危险命令正则（9 条） | 代码层硬拦截，LLM 不可绕过 |
| Mode | readonly / autopilot | readonly 禁止 write_file 和交互型浏览器操作 |
| Role | 5 角色权限矩阵 | writer 不提供 run_command，security 不提供 write_file |
| One-time Approval | ↑↓ 选择式审批框 | 默认拒绝，批准仅本次生效，不跨请求复用 |

Memory 和 Skill 内容注入时标注 UNTRUSTED SOURCE。LLM 可读取参考，不能提升权限、改变 mode 或绕过审批。

### 3.4 源文件职责

| 文件 | 职责 |
|------|------|
| `agent.go` | 7 状态机 Loop、消息准备、工具执行、TUI 命令路由 |
| `llm.go` | OpenAI 兼容 SSE streaming、增量组装、重试/取消 |
| `tools.go` | 工具接口、CommandPolicy、FilePolicy |
| `approval.go` | 可选择式审批框（↑/↓ 选择 + Enter 确认） |
| `browser.go` | 无头 Chromium 浏览器（chromedp），7 个浏览器工具 |
| `skill.go` | 技能扫描/校验/索引 |
| `memory.go` | 持久记忆、首次启动向导、审批边界 |
| `vision.go` | 图像理解（Gemini / OpenAI 双后端自动检测） |
| `plan.go` | Plan/Step 状态机、生成/暂停/恢复/retry |
| `roles.go` | 5 角色（default/coder/ops/writer/security） |
| `compress.go` | Context Compaction、Emergency Summary |
| `worklog.go` | 会话审计（session.md + events.jsonl） |
| `ui.go` | 终端渲染（Braille Logo、框线、配色、审批框） |
| `input.go` | 终端输入（CJK 安全、粘贴检测、bracketed paste） |
| `terminal.go` | 跨平台 raw terminal（golang.org/x/term） |
| `doctor.go` | `--doctor` 自检（DNS/TCP/TLS/HTTP） |
| `system.go` | OS/架构/发行版/内核探测 |
| `instance.go` | 同二进制单实例锁 |

### 3.5 目录结构

```
ELIZA_Agent/
├── cmd/eliza/          # Go 源码（package main）
├── binaries/           # 预编译二进制（6 平台）
├── docs/               # 文档、CHANGELOG、修复记录
├── plugins/chromium/   # 兼容浏览器目录
├── config.json         # 配置文件
├── Makefile            # 编译脚本
├── go.mod / go.sum     # Go 模块
├── skills/             # 技能（运行时生成）
├── memory/             # 持久记忆（运行时生成）
├── worklogs/           # 会话审计（运行时生成）
└── plans/              # 执行计划（运行时生成）
```

### 3.6 工具全集

| 工具 | 说明 | readonly | autopilot |
|------|------|:--------:|:---------:|
| `read_file` | 分段读取文件，受 FilePolicy 约束 | ✅ | ✅ |
| `write_file` | 写入文件 | ❌ | ✅ 审批 |
| `run_command` | 执行 Shell 命令，受 CommandPolicy 约束 | ✅ 只读 | ✅ 审批 |
| `skill_list` | 列出可用技能 | ✅ | ✅ |
| `skill_view` | 按需加载技能内容 | ✅ | ✅ |
| `memory` | 持久记忆（save/recall/forget） | ✅ | ✅ 审批 |
| `view_image` | 视觉模型理解图片 | ✅ | ✅ |
| `browser_open` | 打开 http/https 页面 | ✅ | ✅ |
| `browser_snapshot` | 读取页面标题、URL、正文和控件 | ✅ | ✅ |
| `browser_reset` | 重置浏览器会话 | ✅ | ✅ |
| `browser_click` | 点击页面元素 | ❌ | ✅ |
| `browser_type` | 输入页面文本 | ❌ | ✅ |
| `browser_screenshot` | 截图保存到 workspace | ❌ | ✅ |

### 3.7 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `ELIZA_BASE_URL` | LLM API 端点 | 无 |
| `ELIZA_API_KEY` | API Key | 无 |
| `ELIZA_MODEL` | 模型名 | 无 |
| `ELIZA_MODE` | readonly / autopilot | readonly |
| `ELIZA_VISION_BASE_URL` | 视觉模型端点 | 回退到 BASE_URL |
| `ELIZA_VISION_API_KEY` | 视觉模型 Key | 回退到 API_KEY |
| `ELIZA_VISION_MODEL` | 视觉模型名 | 回退到 MODEL |
| `ELIZA_BROWSER_TOOLS_DIR` | 浏览器工具目录 | `~/eliza/tools` |
| `ELIZA_BROWSER_EXEC_PATH` | 直接指定浏览器可执行文件 | 自动探测 |
| `ELIZA_WORKSPACE_ROOTS` | 工作区根路径 | 二进制同目录 |
| `ELIZA_FILE_ALLOW_ABSOLUTE` | 允许绝对路径读取 | false |

完整配置项见首次运行自动生成的 `.env` 文件。

### 3.8 TUI 交互命令

| 命令 | 说明 |
|------|------|
| `/help` | 命令列表 |
| `/status` | 当前状态 |
| `/clear` | 清空上下文 |
| `/new` | 新会话 |
| `/mode <name>` | 切换 readonly / autopilot |
| `/role <name>` | 切换角色 |
| `/plan <描述>` | 生成执行计划 |
| `/execute` | 执行计划 |
| `/retryplan` | 重试失败计划 |
| `/skipstep` | 跳过当前步骤 |
| `/cancelplan` | 取消计划 |
| `/compress` | 手动压缩上下文 |
| `/tools` | 当前可用工具列表 |
| `/skills` | 技能管理 |
| `/memory` | 记忆状态 |

危险操作审批弹框用 `↑/↓` 选择 + `Enter` 确认，默认拒绝。

---

## 4. 无头浏览器补充说明

### 4.1 Chromium 发现优先级

ELIZA 按以下顺序搜索 Chromium 可执行文件：

1. `ELIZA_BROWSER_EXEC_PATH` 环境变量（直接路径）
2. `~/eliza/tools/` 下各架构子目录
3. `./plugins/chromium/`（兼容旧版目录）
4. 系统 PATH（`chromium` / `google-chrome` / `chrome-headless-shell` 等）

### 4.2 推荐目录布局

```text
~/eliza/tools/
├── chrome-headless-shell/
│   └── chrome-headless-shell-linux64/
│       └── chrome-headless-shell   ← 二进制本体
├── chrome-linux64/chrome           ← 完整 Chrome
└── chrome-linux-arm64/chrome       ← ARM 版本
```

### 4.3 浏览器会话管理

- 单个浏览器进程复用，跨工具调用共享会话
- 页面文本提取上限 24KB（可配置），防止 token 爆炸
- 操作超时 30 秒（可配置），超时后自动 Reset 恢复干净状态
- `browser_reset` 可在任意时刻手动重置浏览器会话

### 4.4 LLM 使用提示

浏览器工具返回前 24KB 页面文本。如果页面内容被截断，LLM 可以通过 `browser_snapshot` 确认当前页面状态。常用模式：

```
browser_open → 打开目标页面
browser_snapshot → 读取页面结构
browser_click / browser_type → 交互操作
browser_snapshot → 确认操作结果
browser_reset → 清理会话
```

---

## 5. 后续发展

v0.9.0 建立了稳定基线。以下方向对标 Hermes Agent、Codex CLI、Claude Code 等前沿 Agent，聚焦 ELIZA 在离线/内网场景下的核心能力提升。不做 Channel、Gateway、消息推送等内网用不到的功能。

### 5.1 子 Agent 委托

Hermes Agent 的 `delegate_task` 和 Claude Code 的 subagent 模式证明，将复杂任务拆解给独立上下文执行的子 Agent 是提升能力的有效手段。ELIZA 当前仅有单 Agent 单会话，下一步引入：

- 子 Agent 独立上下文，不受主会话长度限制
- 子 Agent 沙箱化权限（如只读子 Agent、仅文件操作子 Agent）
- 主子 Agent 间通过 worklog 传递结果

### 5.2 工具接口标准化

当前工具分散在 `tools.go` / `browser.go` / `vision.go` / `memory.go` 中，注册方式不统一。对标 Hermes 和 Codex 的工具注册模式，重构为：

- 统一 Tool 接口：注册、参数校验、权限检查、结果格式化
- 按场景选择性编译工具集合（运维版 / 代码审查版 / 浏览器增强版 / 最小只读版）
- 工具执行日志结构化，便于审计和回放

### 5.3 结构化输出与 MCP 协议

- 支持 JSON Schema 约束的 structured output，替代当前纯文本解析
- 引入 MCP (Model Context Protocol) 客户端，对接外部工具生态
- 让 ELIZA 可以调用 MCP 兼容的任何工具服务器，不限于内置工具集

### 5.4 记忆系统升级

当前记忆基于 Markdown 文件，存在查询困难、并发冲突、大小控制粗放等问题。后续升级：

- SQLite 后端：支持结构化查询、增量写入、并发安全
- 记忆分级：长期记忆（跨会话）、短期记忆（本次会话）、工作记忆（当前任务）
- 自动摘要与遗忘：超出容量上限时自动压缩低优先级记忆

### 5.5 TUI 体验优化

当前 TUI 基于手写 ANSI 控制序列 + raw terminal，功能完整但交互细节有提升空间。对标 Hermes Agent 的终端体验：

- 流式输出期间允许继续输入（当前已支持部分，需完善取消和注入逻辑）
- 消息历史回滚查看（当前无 scrollback）
- 多行输入原生支持（当前用临时文件折叠方案，可以改为内联多行编辑）
- 会话列表与恢复（从 worklogs 目录恢复历史会话）

### 5.6 多模型 Provider 支持

当前仅支持 OpenAI 兼容 API。内网环境可能同时存在多种模型服务（vLLM、llama.cpp、国产大模型网关等）。后续支持：

- Provider 抽象层：统一接口，不同后端各自实现
- 运行时切换模型（`/model <name>`）
- 多模型协作：不同任务路由到不同模型（代码生成用 A、摘要用 B）

---

## 6. 设计原则

- **内网优先。** 默认面向内网 OpenAI-compatible API，离线可用。
- **零运行时依赖。** CGO_ENABLED=0 静态编译，单文件拷贝即用。
- **Linux 优先。** 工具面向 POSIX，Windows 提供退化兼容。
- **安全边界显式化。** 双层 Policy + 审批框，不可信数据不能提权。
- **可审计。** events.jsonl 全量记录，API key 自动脱敏。

## 许可证

MIT · Powered By MUY & ELIZA
