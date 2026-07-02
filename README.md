# ELIZA-AGENT

单二进制 · 零依赖 · 拷走即用

v0.9.0 · CGO_ENABLED=0 · 内置无头 Chromium 浏览器 · 首个稳定版本

---

## 近期更新

**2026-07-01 · v0.9.0 TUI 重构**

- TUI 层重构为事件驱动架构：`ui_events.go` 定义 9 种 UIEvent + 3 种 UICommand，Agent 逻辑与渲染器通过事件解耦。
- 新增声明式 UI 组件（`ui_components.go`）：Message / Status / ToolCall / InputBar / Approval，每组件返回预包装行列表，支持窄终端自动换行。
- 新增 PTY 真实终端测试（`tui_pty_unix_test.go`）：覆盖光标移动、退格删除、CJK 输入、CRLF 保证。
- 新增 `edit_file` + `glob` 工具，对标 Claude Code/Codex CLI，工具集 15 个。

**2026-06-30 · 无头浏览器**

- 可选择式审批框：危险操作用 `↑/↓` 键选择 + `Enter` 确认，替代 `/approve` `/deny` 斜杠命令。默认拒绝，批准只对本次操作生效；拒绝时可补充新要求让 ELIZA 调整方案。
- 输入体验全面升级：bracketed paste 协议支持、多行粘贴自动折叠为临时文件、中文输入法即时显示、统一用 `golang.org/x/term` 替代平台分叉实现。
- 新增 `view_image` 视觉理解工具，支持 Gemini / OpenAI 双后端。
- 修复审批框重绘残留、chromedp context 派生导致浏览器连接取消、输入刷屏等问题。详见 `docs/fix_v0.9.0.md`。

**2026-06-30 · 无头浏览器**

- 基于 Go chromedp 新增 7 个无头浏览器工具：`browser_open`、`browser_snapshot`、`browser_click`、`browser_type`、`browser_screenshot`、`browser_reset`。
- 零外部运行时依赖（无 Node / Python / Playwright）。Chromium 本体可选，解压到二进制同目录 `tools/` 即自动激活。

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
# 同时自动创建 skills/ memory/ worklogs/ plans/ tools/ 目录
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

**自动发现：** ELIZA 启动时自动扫描以下位置，找到任意一个即激活浏览器工具：

1. `ELIZA_BROWSER_EXEC_PATH` 环境变量指定的路径
2. 二进制同目录 `tools/` 下各架构子目录
3. `./plugins/chromium/`
4. 系统 PATH（`chromium` / `google-chrome` / `chrome` 等）
5. macOS：`/Applications/Google Chrome.app/...`
6. Windows：`Program Files\Google\Chrome\...`

如果系统已安装 Chrome 或 Chromium，ELIZA 自动检测并使用，无需额外配置。

**内网离线部署：** 目标机器没有 Chrome 时，手动部署 chrome-headless-shell：

```bash
# 在外网机器下载（约 114M）
wget https://storage.googleapis.com/chrome-for-testing-public/150.0.7871.24/linux64/chrome-headless-shell-linux64.zip

# tools/ 目录首次运行自动创建
mkdir -p tools/chrome-headless-shell
unzip chrome-headless-shell-linux64.zip -d tools/chrome-headless-shell/

# 验证
./eliza doctor | grep browser
# 期望: PASS  browser  检测到 Chromium ...
```

---

## 3. 架构

### 3.1 组件拓扑

```
main.go ──► agent.go ──► llm.go (SSE streaming)
 CLI入口     状态机Loop    取消/重试
                │
          ToolRegistry (统一注册/授权/审批/执行)
                │
   ┌────────────┼────────────┬────────────┬────────────┐
   ▼            ▼            ▼            ▼            ▼
tools.go     skill.go    memory.go    vision.go   browser.go
read/write   按需加载     审批边界     图像理解    无头浏览器
 /edit/glob
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
| `ui_events.go` | 事件驱动层（UIEvent/UICommand/AgentUI），解耦 Agent 与 Renderer |
| `ui_components.go` | 声明式 UI 组件（Message/Status/ToolCall/InputBar/Approval） |
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
| `edit_file` | 精确替换文件中指定文本（仅首处） | ❌ | ✅ 审批 |
| `glob` | 按 glob 模式查找文件 | ✅ | ✅ |
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
| `ELIZA_BROWSER_TOOLS_DIR` | 浏览器工具目录 | `./tools`（二进制同目录） |
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
2. 二进制同目录 `tools/` 下各架构子目录
3. `./plugins/chromium/`（兼容旧版目录）
4. 系统 PATH（`chromium` / `google-chrome` / `chrome-headless-shell` 等）

### 4.2 推荐目录布局

```text
./tools/
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

### 5.2 工具 Profile 按场景裁剪

v0.9.0 已完成工具注册表标准化（ToolRegistry），所有工具实现统一 Tool 接口，支持注册/授权/审批/执行管线。当前定义了 4 种工具 Profile（minimal_readonly / code_review / ops / browser_enhanced），下一步实现编译期按 Profile 裁剪工具集合，产出不同场景的专用二进制。

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

---

## 7. 已发现问题

### 7.1 老式终端和部分终端模拟器的 TUI 兼容性问题

当前 TUI 已经完成事件层和组件层解耦，默认模式在 macOS Terminal、iTerm2、Linux 原生终端、Windows CMD 直连 Linux 等现代终端中表现稳定。但在部分老式终端或终端模拟器中，仍可能出现输入框重绘异常，典型场景包括：

- MobaXterm 20.0 或类似较老版本的 MobaXterm SSH terminal
- PuTTY / KiTTY 的旧配置或非 UTF-8 配置
- 堡垒机自带 Web SSH terminal
- 跳板机中的 tmux / screen 嵌套终端
- 串口终端、精简 xterm、老式 Linux console
- `TERM=xterm` 但实际 ANSI / Unicode 行为并不完整的终端模拟器

已观察或可能观察到的症状：

- 输入框中删除字符后，屏幕上仍残留被删除字符的占位痕迹
- 中文、全角标点、emoji、Braille logo、box drawing 边框导致光标列计算和实际显示不一致
- 输入行到达终端右边界后触发自动换行，后续重绘时光标回退位置错误
- 状态消息、工具调用消息和底部输入框交错时，旧内容没有被完整清除
- 审批框或运行中输入框在刷新后出现边框错位、残影或多余空白
- Home / End / Delete / 方向键在某些终端里发送的 escape sequence 与标准 xterm 不完全一致

#### 初步原因判断

该问题主要不是 Agent loop、LLM 或工具执行逻辑导致的，而是终端渲染兼容性问题。ELIZA 当前 TUI 使用 raw terminal + ANSI 控制序列 + 自研输入 overlay。现代终端对以下行为支持较一致，但部分老式终端或终端模拟器会有差异：

1. **宽字符显示宽度不一致。**  
   ELIZA 使用 `displayWidth` 计算中文、全角标点、emoji、combining mark、ZWJ 等字符的终端 cell 宽度。但终端模拟器实际使用的字体、locale、Unicode 版本和 wcwidth 规则可能不同。例如程序认为某字符占 2 列，终端实际按 1 列显示，后续清理和光标定位就会偏移。

2. **清屏序列处理不完整。**  
   当前输入 overlay 主要通过移动光标后发送 `CSI 0J` / `CSI 2K` 类 ANSI 序列清除旧内容。部分终端对双宽字符右半 cell、自动换行后的虚拟列、scroll region、alternate screen 状态处理不一致，导致“字符删除了但视觉上还占位置”。

3. **右边界 autowrap 触发后状态不一致。**  
   终端在最后一列输出字符后可能进入 pending wrap 状态。此时再发送 `\r`、上移、清除到屏幕末尾，部分终端会先执行隐式换行，导致 ELIZA 以为自己在第 N 行，终端实际已经在第 N+1 行。

4. **终端类型声明和真实能力不匹配。**  
   一些终端会设置 `TERM=xterm` 或 `TERM=xterm-256color`，但 Home / End / Delete、bracketed paste、Unicode、clear line、cursor movement 等行为并不完全等价于现代 xterm。

5. **SSH / tmux / screen / 堡垒机多层转发放大差异。**  
   当用户从 Windows 终端模拟器进入 SSH，再进入 tmux/screen 或堡垒机 Web terminal 时，输入事件和输出控制序列可能被多层转译，最终表现为局部残影、错位或输入按键异常。

#### 当前可用临时规避方式

在正式兼容模式完成前，可以优先尝试以下方式：

```bash
# 关闭彩色和复杂 TUI 装饰，降低终端兼容风险
./eliza --no-color

# 纯文本模式，适合 CI、管道、日志采集和问题终端
./eliza --plain

# 确认远端 locale 为 UTF-8
echo $LANG
export LANG=en_US.UTF-8
```

如果使用 MobaXterm，建议检查：

- Session 的 Terminal charset 是否为 UTF-8
- 字体是否支持中文和 box drawing 字符
- 是否经过 tmux/screen/堡垒机二次转发
- 是否可以用 Windows CMD / PowerShell / Windows Terminal 直连同一台 Linux 对比复现

#### 计划修复方向：TUI Compatibility Mode

下一步计划新增正式的 **TUI Compatibility Mode**。目标不是针对某一个终端写死特殊逻辑，而是提供一个最大兼容渲染 profile，让老式终端、MobaXterm、PuTTY、堡垒机 Web terminal 等环境都能稳定使用。

建议新增配置：

```bash
ELIZA_TUI_PROFILE=auto      # 默认：自动判断，优先 modern，发现高风险环境时提示 compat
ELIZA_TUI_PROFILE=modern    # 当前默认体验：Unicode、颜色、组件化消息和输入 overlay
ELIZA_TUI_PROFILE=compat    # 最大兼容：ASCII UI + hard clear + 保守宽度
ELIZA_TUI_PROFILE=plain     # 无交互装饰，适合管道/CI/日志
```

建议同步增加 CLI 参数：

```bash
./eliza --tui auto
./eliza --tui modern
./eliza --tui compat
./eliza --plain
```

兼容模式的能力开关可以集中在 `UIConfig` / `UIComponentContext` 中：

```go
type UIProfile string

const (
    UIProfileAuto   UIProfile = "auto"
    UIProfileModern UIProfile = "modern"
    UIProfileCompat UIProfile = "compat"
    UIProfilePlain  UIProfile = "plain"
)

type UICapabilities struct {
    Color              bool
    UnicodeDecorations bool
    BrailleLogo        bool
    WideBoxes          bool
    InputOverlay       bool
    RunningInput       bool
    BracketedPaste     bool
    HardClearInput     bool
    ReserveRightMargin int
}
```

推荐 profile 行为：

| Profile | 用途 | 行为 |
| --- | --- | --- |
| `modern` | 现代终端默认体验 | 保留 Unicode logo、box drawing、颜色、组件化消息、运行中输入 |
| `compat` | 老式终端/终端模拟器 | 禁用复杂 Unicode，使用 ASCII UI，启用 hard clear，保守换行 |
| `plain` | 管道/CI/日志 | 不进入 raw terminal，不画 overlay，不使用颜色和边框 |
| `auto` | 默认入口 | 检测 TTY、TERM、COLORTERM、locale、tmux/screen 等，必要时提示用户切 compat |

#### 兼容模式的具体渲染策略

`compat` 模式应尽量避免依赖终端模拟器的复杂行为：

1. **禁用复杂 Unicode 装饰。**
   - 不显示 Braille logo
   - 不使用 `╭─╮│╰╯` 等 box drawing 边框
   - 不使用 emoji 或私有区图标
   - 用户消息、Agent 消息、工具消息和状态消息统一使用 ASCII 前缀

   示例：

   ```text
   USER> 请检查这个文件
   AGENT> 我会先读取文件并总结风险。
   TOOL run_command COMPLETED 123ms exit=0
   RUNNING 思考中...
   ```

2. **输入框降级为单行 ASCII prompt。**

   ```text
   INPUT [readonly/default]> 
   GUIDE [readonly/default]> 
   ```

   兼容模式仍可支持中文输入，但重绘时按更保守的宽度处理，并避免在最后 2 列输出内容。

3. **保守换行和右边界预留。**
   - `modern` 当前保留 1 列避免 autowrap
   - `compat` 建议保留 2-4 列：`effectiveWidth = terminalWidth - ReserveRightMargin`
   - 所有组件渲染、输入 buffer wrap、状态消息 wrap 都使用 `effectiveWidth`

4. **Hard clear input overlay。**
   当前清理方式主要依赖“移动到 overlay 起点 + 清除到屏幕末尾”。兼容模式应改为更重但更可靠的逐行清理：

   ```text
   1. 移动到输入 overlay 的第一行
   2. 对 overlay 占用的每一行执行：
      - \r
      - CSI 2K 清整行
      - 输出 effectiveWidth 个 ASCII 空格覆盖残留 cell
      - \r
      - 下移一行
   3. 回到输入 overlay 的第一行
   4. 重新绘制当前输入框
   5. 移动光标到计算后的输入位置
   ```

   即使某些终端对双宽字符右半 cell 清除不完整，ASCII 空格覆盖也能最大概率消除残影。

5. **审批框降级为简单菜单。**

   ```text
   APPROVAL REQUIRED
   Dangerous command: rm file.txt

   > Deny
     Approve once
     Deny and tell ELIZA what to do

   Up/Down: select, Enter: confirm
   ```

   避免 Unicode 边框和过宽行，所有行都显式截断或换行。

6. **运行中输入可降级关闭。**
   如果 `compat` 下仍发现终端错位，可以进一步提供：

   ```bash
   ELIZA_TUI_RUNNING_INPUT=off
   ```

   关闭流式输出期间的输入 overlay，只在请求完成后读取下一条用户输入。这会降低交互性，但对堡垒机和老终端最稳。

#### 自动检测建议

`auto` 模式不应该强行猜测所有终端，但可以做风险提示：

- `TERM=dumb`：自动进入 `plain`
- stdout 不是 TTY：自动进入 `plain`
- `LANG` / `LC_CTYPE` 不包含 UTF-8：提示或进入 `compat`
- 检测到 `TMUX` / `STY`：保留 modern，但提示 `/doctor tui`
- `TERM` 为 `xterm` 但没有 `COLORTERM`：提示可尝试 `compat`
- 检测到 MobaXterm 相关环境变量或 X11 forwarding 特征时提示 `compat`

注意：MobaXterm 经 SSH 后不一定稳定暴露专有环境变量，因此不能依赖自动识别。必须保留显式开关：

```bash
ELIZA_TUI_PROFILE=compat ./eliza
```

#### 诊断工具建议

新增：

```bash
./eliza doctor tui
```

或在交互中新增：

```text
/doctor tui
```

诊断输出建议包含：

- `TERM`
- `COLORTERM`
- `LANG` / `LC_ALL` / `LC_CTYPE`
- terminal width / height
- 是否 TTY
- 是否处于 tmux / screen
- 当前 TUI profile
- Unicode 装饰是否启用
- bracketed paste 是否启用
- hard clear 是否启用

可选增加一个视觉测试：

```text
ELIZA TUI DIAGNOSTIC
1. 打印中文/全角标点/ASCII 混合字符串
2. 打印宽度标尺
3. 模拟输入框从长文本缩短为短文本
4. 询问用户是否看到残影
```

#### 测试和验收标准

修复完成后至少需要新增以下测试：

- 纯函数测试：`compat` profile 下所有组件输出仅包含 ASCII UI 装饰
- 纯函数测试：`compat` profile 下每一行 `displayWidth <= terminalWidth - ReserveRightMargin`
- renderer 测试：长输入变短输入时必须输出 hard clear 序列和空格覆盖
- PTY 集成测试：模拟中文输入、左移、删除、插入、提交，确认最终 buffer 正确
- 快照测试：`modern` 和 `compat` 下 user/agent/tool/status/approval/input bar 均有稳定输出

人工验收建议覆盖：

- macOS Terminal / iTerm2：`modern` 正常
- Linux GNOME Terminal / xterm：`modern` 正常
- Windows CMD SSH 直连 Linux：`modern` 或 `compat` 正常
- MobaXterm 20.0：`ELIZA_TUI_PROFILE=compat` 下无删除残影、无错位、可正常输入中文
- 堡垒机 Web terminal：`compat` 或 `plain` 可稳定使用

#### 预期落地顺序

1. 扩展 `UIConfig`：加入 `Profile` 和 `UICapabilities`
2. 让 `Renderer` 初始化时根据 CLI/env/auto detection 计算 capabilities
3. 将 `UIComponentContext` 使用 capabilities 渲染 modern / compat 两套外观
4. 实现 `clearInputLocked` 的 hard clear 分支
5. 为 input wrap 增加 `ReserveRightMargin`
6. 审批框、状态消息、工具消息、用户消息、Agent 消息全部支持 compat 输出
7. 增加 `--tui` CLI 参数和 `ELIZA_TUI_PROFILE`
8. 增加 `doctor tui` 诊断输出
9. 增加单元测试、PTY 测试和 README 使用说明

这条路线不会放弃当前较好的现代 TUI 体验，而是在同一套组件和事件模型下增加一个保守渲染 profile。遇到问题终端时，用户可以直接切换：

```bash
ELIZA_TUI_PROFILE=compat ./eliza
```

如果兼容模式仍有问题，用户可以继续退到：

```bash
./eliza --plain
```

这样 ELIZA 可以同时服务现代开发终端和内网常见的老式终端/堡垒机环境。

## 许可证

MIT · Powered By MUY & ELIZA
