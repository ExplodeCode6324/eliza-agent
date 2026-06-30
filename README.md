# ELIZA-AGENT

单二进制 · 零依赖 · 拷走即用

v0.8.0 · CGO_ENABLED=0 · linux/amd64/arm64

---

一个面向企业内网的 CLI AI Agent。不装 Docker，不装 Python，不装 Node。
你只需要一个 6.9MB 的文件，拷过去就能跑。

```
$ scp eliza user@192.168.1.x:/opt/
$ ssh user@192.168.1.x '/opt/eliza --version'
ELIZA Agent v0.8.0
```

在内网环境里，"先装运行时再跑 Agent"是一条死路。生产服务器可能没有
包管理器。信创机器的 glibc 版本不可预测。安全策略禁止从公网拉镜像。
所以我们做了纯 Go、CGO_ENABLED=0 的静态编译——一个 ELF 文件，
不链任何 C 库，拔了网线一样跑。

---

Agent 的能力不等于大模型的能力。代码层才是最后的守门人。所以我们没有把
安全策略写在 system prompt 里靠 LLM 自觉遵守，而是写在代码的正则匹配里。
`rm` 不在 readonly 白名单里 → 代码直接拒绝，LLM 说什么都没用。
autopilot 模式放宽了命令范围，但正则命中照样弹 `/approve` 审批。

Memory 和 Skill 文件注入 system prompt 时会明确标注 UNTRUSTED SOURCE。
LLM 可以读取、参考、建议，但不能提权，不能改 mode，不能绕过审批。

每次对话生成完整审计链，API key 自动脱敏，出问题能回溯"谁让 Agent 做了什么"。

---

## 快速开始

```bash
# 编译
make build

# 编译全部预设平台到 binaries/
make build-all

# 首次运行生成 .env，编辑 API 配置后重新运行
./binaries/eliza-$(go env GOOS)-$(go env GOARCH)

# 单次查询
./eliza -q "磁盘使用情况"

# 自检
./eliza doctor --offline
```

## 工具

| 工具 | 说明 |
|------|------|
| `read_file` | 分段读取文件，受 FilePolicy 约束 |
| `write_file` | 写入文件，需审批 |
| `run_command` | 执行 Shell 命令，受 CommandPolicy 约束 |
| `skill_list` | 列出可用技能 |
| `skill_view` | 按需加载技能 |
| `memory` | 持久记忆（save/recall/forget，需审批） |
| `view_image` | 调用视觉模型理解图片（截图、终端输出等） |
| `browser_open` | 用无头 Chromium 打开 http/https 页面（可选） |
| `browser_snapshot` | 读取当前页面标题、URL、正文和主要控件 |
| `browser_screenshot` | 保存当前页面截图到 workspace；readonly 模式禁用 |
| `browser_click` | 点击页面元素；readonly 模式禁用 |
| `browser_type` | 输入页面文本；readonly 模式禁用 |
| `browser_reset` | 重置浏览器会话 |

## 环境变量

| 变量 | 说明 |
|------|------|
| `ELIZA_BASE_URL` | LLM API 端点 |
| `ELIZA_API_KEY` | API Key |
| `ELIZA_MODEL` | 模型名 |
| `ELIZA_MODE` | `readonly` / `autopilot` |
| `ELIZA_VISION_BASE_URL` | 视觉模型端点（可选） |
| `ELIZA_VISION_API_KEY` | 视觉模型 Key |
| `ELIZA_VISION_MODEL` | 视觉模型名 |
| `ELIZA_BROWSER_TOOLS_DIR` | 浏览器工具目录，默认 `~/eliza/tools` |
| `ELIZA_BROWSER_CHROMIUM_DIR` | 兼容目录，默认 `./plugins/chromium` |
| `ELIZA_BROWSER_EXEC_PATH` | 直接指定 Chrome/Chromium 可执行文件 |
| `ELIZA_WORKSPACE_ROOTS` | 工作区根路径 |
| `ELIZA_FILE_ALLOW_ABSOLUTE` | 允许绝对路径读取 |

完整列表见首次运行自动生成的 `.env`。

## TUI 交互命令

| 命令 | 说明 |
|------|------|
| `/help` | 命令列表 |
| `/status` | 当前状态 |
| `/clear` | 清空上下文 |
| `/new` | 新会话 |
| `/mode <name>` | 切换模式 |
| `/role <name>` | 切换角色 |
| `/plan <描述>` | 生成执行计划 |
| `/execute` | 执行计划 |
| `/compress` | 手动压缩 |
| `/approve` | 批准危险操作 |
| `/deny` | 拒绝危险操作 |

## 架构

```
main.go ──► agent.go ──► llm.go (SSE streaming)
 CLI入口     状态机Loop    取消/重试
                │
   ┌────────────┼────────────┬────────────┐
   ▼            ▼            ▼            ▼
tools.go     skill.go    memory.go    vision.go
双层Policy   按需加载     审批边界     图像理解
```

| 文件 | 职责 |
|------|------|
| `agent.go` | 7 状态机 Loop、消息准备、工具执行、TUI 命令路由 |
| `llm.go` | OpenAI 兼容 SSE streaming、增量组装、重试/取消 |
| `tools.go` | 工具接口、CommandPolicy（双层）、FilePolicy（沙箱） |
| `skill.go` | 技能扫描/校验/索引 |
| `memory.go` | 持久记忆、首次启动向导、审批边界 |
| `vision.go` | 图像理解（Gemini / OpenAI 双后端自动检测） |
| `plan.go` | Plan/Step 状态机、生成/暂停/恢复 |
| `roles.go` | 5 角色（default/coder/ops/writer/security） |
| `compress.go` | Context Compaction、Emergency Summary |
| `worklog.go` | 会话审计（session.md + events.jsonl） |
| `ui.go` | 终端渲染（Braille Logo、框线、配色） |
| `input.go` | 终端输入（CJK 安全、粘贴检测、Backspace） |
| `terminal.go` | 跨平台终端 mode 切换 |
| `doctor.go` | `--doctor` 自检（DNS/TCP/TLS/HTTP） |
| `system.go` | OS/架构/发行版/内核探测 |
| `instance.go` | 同二进制单实例锁 |

## 目录结构

```
ELIZA_Agent/
├── cmd/eliza/          # Go 源码（package main）
├── binaries/           # 预编译二进制与说明
├── docs/               # 文档与修复记录
├── plugins/chromium/   # 兼容浏览器目录
├── config.json         # 配置文件
├── Makefile            # 编译脚本
├── go.mod / go.sum     # Go 模块
├── skills/             # 技能（运行时生成）
├── memory/             # 持久记忆（运行时生成）
├── worklogs/           # 会话审计（运行时生成）
└── plans/              # 执行计划（运行时生成）
```

## 无头浏览器

浏览器能力由 Go 内置 `chromedp` 控制层提供，不需要 Node、Python、Playwright
或 Puppeteer。Chromium 本体是可选资产：正常启动会创建 `~/eliza/tools`，你可以
把 Chromium 或 `chrome-headless-shell` 解压到这里；旧的 `./plugins/chromium`
目录仍会被扫描。

常见布局：

```text
~/eliza/tools/
├── chrome-linux64/chrome
├── chrome-linux-arm64/chrome
├── chrome-headless-shell-linux64/chrome-headless-shell
└── chrome-headless-shell-linux-arm64/chrome-headless-shell
```

## 设计原则

- **内网优先** — 默认面向内网 OpenAI-compatible API
- **零运行时依赖** — 静态编译，单文件可拷贝即用
- **Linux 优先** — 工具面向 POSIX，Windows 可退化
- **安全边界显式化** — 双层 Policy，不可信数据不能提权
- **可审计** — events.jsonl 全量记录

## 许可证

MIT · Powered By MUY & ELIZA
