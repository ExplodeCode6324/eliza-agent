# ELIZA Agent v0.9.0 — 首个稳定版本

> 日期: 2026-07-01  
> 版本: v0.9.0  
> 范围: v0.8.0 P1-03~P3-03 之后的所有功能增强与问题修复  
> 仓库状态: 基于 main 分支增量开发，本次为综合版本发布

---

## 版本说明

v0.9.0 是 ELIZA Agent 的第一个「稳定运行版本」。自 v0.1.0 起历经 8 次迭代、超过 20 个 fix 记录，核心架构、工具集、输入体验和安全策略已通过真实环境验证。本次版本整合了 v0.8.0 期间的所有功能增强与修复，标志着项目从快速迭代进入稳定维护阶段。

### 代码规模

| 指标 | 数值 |
|------|------|
| Go 源文件 | 29 |
| 总代码行数 | ~8500 |
| 新增代码 (P1-P3 后) | +1687 / -210 |
| 外部依赖 | chromedp v0.14.2, golang.org/x/term v0.44.0 |
| 预编译平台 | linux/darwin/windows × amd64/arm64 (6 个) |

---

## 一、新功能 (自 P1-P3 后)

### 1. 无头 Chromium 浏览器 — 零外部运行时

基于 Go `chromedp` v0.14.2 内置完整的无头浏览器控制层，不依赖 Node、Python、Playwright 或 Puppeteer。Chromium 本体为可选资产——解压到 `~/eliza/tools/` 即自动激活。

**7 个浏览器工具：**

| 工具 | 功能 | readonly | autopilot |
|------|------|:--------:|:---------:|
| `browser_open` | 打开 http/https 页面 | ✅ | ✅ |
| `browser_snapshot` | 读取页面标题、URL、正文和控件 | ✅ | ✅ |
| `browser_reset` | 重置浏览器会话 | ✅ | ✅ |
| `browser_click` | 点击页面元素 | ❌ | ✅ |
| `browser_type` | 输入页面文本 | ❌ | ✅ |
| `browser_screenshot` | 截图保存到 workspace | ❌ | ✅ |

**Chromium 发现优先级：**
1. `ELIZA_BROWSER_EXEC_PATH` 环境变量（直接路径）
2. `~/eliza/tools/` 下各架构子目录
3. `./plugins/chromium/`（兼容目录）
4. 系统 PATH（`chromium` / `google-chrome` 等）

启动时自动扫描并激活浏览器工具，找不到 Chromium 时仅禁用浏览器工具，不影响其他功能。

**关键实现细节：**
- 单个浏览器进程复用，跨工具调用共享会话
- 页面文本提取限制 24KB 防止 token 爆炸
- 操作超时 30s（可配置），超时后自动 `Reset()` 恢复干净状态
- `browser_open` 时自动导航 `about:blank` 初始化浏览器进程

**新增文件：** `cmd/eliza/browser.go` (769 行), `cmd/eliza/browser_test.go` (73 行)  
**新增依赖：** `github.com/chromedp/chromedp` (含 cdproto/sysutil 等传递依赖)  
**修改文件：** `go.mod`, `go.sum`, `cmd/eliza/tools.go`, `cmd/eliza/main.go`

---

### 2. 可选择式审批框

将危险操作的审批从斜杠命令 (`/approve` `/deny`) 改为本地 TUI 交互框：使用 `↑/↓` 键切换选项，`Enter` 确认。

**三种选项：**
1. **Deny**（默认选中）— 拒绝本次工具调用
2. **Approve once** — 批准本次调用，不跨请求复用
3. **Deny and tell ELIZA what to do** — 拒绝并补充调整指令

选项 3 会弹出第二行输入框，用户可输入引导信息（如"不要删文件，改用 mv 备份"），ELIZA 收到拒绝事件和用户指引后调整后续方案。

**安全边界：**
- 每次审批独立，不跨工具调用复用
- 拒绝是默认选项，用户必须主动切换到 Approve
- 拒绝后可提供指引，但始终不改变 Tool Policy 的代码层规则
- `autopilot` 模式也不能绕过危险命令审批

**新增文件：** `cmd/eliza/approval.go` (80 行)  
**修改文件：** `cmd/eliza/ui.go`, `cmd/eliza/input.go`, `cmd/eliza/agent.go`, `cmd/eliza/memory.go`, `cmd/eliza/tools.go`, `cmd/eliza/plan.go`, `cmd/eliza/roles.go`, `cmd/eliza/compress.go`, `cmd/eliza/contextbar.go`

---

### 3. 视觉理解工具 view_image

内置 `view_image` 工具，支持调用视觉模型理解图片内容（截图、终端输出、图表等）。

**后端自动检测：**
- URL 包含 `generativelanguage` → Google Gemini 格式
- 其他 → OpenAI 兼容格式

**配置（.env）：**
| 变量 | 说明 |
|------|------|
| `ELIZA_VISION_BASE_URL` | 视觉 API 端点（回退到 ELIZA_BASE_URL） |
| `ELIZA_VISION_API_KEY` | Vision API Key（回退到 ELIZA_API_KEY） |
| `ELIZA_VISION_MODEL` | Vision 模型名（回退到 ELIZA_MODEL） |

支持 PNG / JPEG / GIF / WebP 格式。readonly 模式开放，autopilot 不受限。

**修改文件：** `cmd/eliza/vision.go` (314 行), `cmd/eliza/tools.go`, `cmd/eliza/main.go`

---

### 4. 输入体验全面优化

#### 4.1 Bracketed Paste 协议支持

进入 raw mode 后向终端写入 `ESC[?2004h`，启用 bracketed paste。支持该协议的终端会将粘贴内容包裹为 `ESC[200~ ... ESC[201~]`，`readLineRaw()` 识别后整块读取，不再把粘贴流里的换行误判为手动 Enter。

#### 4.2 多行/超长粘贴折叠为临时文件

粘贴内容含换行或超过 500 字节时，写入 `/tmp/eliza-input-N.txt`，输入框仅显示短占位符：

```
[Pasted text #1: 12 lines -> /tmp/eliza-input-xxxx.txt]
```

用户提交时展开占位符，读取真实内容。多行或超长内容自动包装为 `FILE:/tmp/eliza-input-N.txt` 进入 Agent 层，由 `prepareMessages()` 读取并清理。

#### 4.3 CJK 输入即时显示

移除普通字符路径上的 `5ms` 突发不重绘判断：
- ASCII 和 UTF-8 插入后立即 `redrawLine()`
- 中文输入法一次性提交多个汉字时，每个已解码 rune 都会立即显示
- 仅粘贴流中的换行延迟重绘（防止刷屏）

#### 4.4 Raw Terminal 实现统一

删除平台分叉文件 `terminal_unix.go` / `terminal_windows.go`，改用 `golang.org/x/term` 的 `MakeRaw()` / `Restore()`。

| 旧文件 | 处理 |
|--------|------|
| `terminal_unix.go` (syscall ioctl) | 删除 |
| `terminal_windows.go` | 删除 |
| `terminal.go` | 新文件，使用 `golang.org/x/term` |
| `input_pending_unix.go` | 新文件，Unix 平台输入未决检测 |
| `input_pending_windows.go` | 新文件，Windows 平台 stub |

**修改文件：** `cmd/eliza/input.go`, `cmd/eliza/input_test.go`, `cmd/eliza/terminal.go`  
**新增依赖：** `golang.org/x/term`

---

## 二、Bug 修复 (自 P1-P3 后)

### fix: chromedp context.WithTimeout 导致 browserCtx 被取消

**日期:** 2026-06-30 · **详情:** `docs/fix_20260630_browser_context_cancel.md`

`ensureLocked()` 和 `run()` 使用 `context.WithTimeout(b.browserCtx, ...)` 派生子 context，`defer cancel()` 在函数返回时触发关闭，导致 chromedp 内部连接连带被取消。后续 browser 操作全部返回 `context canceled`。

**修复：** 改用 `b.browserCtx` 直接传给 `chromedp.Run()`，不派生子 context。`browserCtx` 仅在 `Reset()` 或进程退出时取消。

---

### fix: 审批框重绘稳定化

**日期:** 2026-07-01 · **详情:** `docs/fix_20260701_approval_redraw.md`

两处问题叠加导致 ↑/↓ 切换时审批框出现重绘错位、残留字符、边框断裂。

**修复 1 (input.go):** 重绘前加 `\r` 确保光标在行首，避免 ANSI 上移计算偏差。  
**修复 2 (ui.go):** 用 `strings.Builder` 收集全部输出行，一次 `fmt.Fprint` 写入，消除逐行输出的时序窗口。

---

### fix: 输入粘贴/CJK/重绘综合优化

**日期:** 2026-07-01 · **详情:** `docs/fix_20260701_input_paste_cjk_redraw.md`

修复三组输入问题：
1. **多行粘贴** — bracketed paste 协议 + 临时文件折叠，不再逐行发送或刷屏
2. **中文输入** — 移除 5ms 延迟，CJK 输入即时显示
3. **跨平台构建** — `golang.org/x/term` 替代 syscall，macOS 构建通过

---

## 三、架构与文档更新

### 文档

| 文件 | 内容 |
|------|------|
| `docs/CHANGELOG.md` | 增加 v0.8.0 三期变更（浏览器、审批框、输入优化） |
| `docs/fix_20260630_browser_context_cancel.md` | 浏览器 context 修复记录 |
| `docs/fix_20260701_approval_redraw.md` | 审批框重绘修复记录 |
| `docs/fix_20260701_input_paste_cjk_redraw.md` | 输入粘贴/CJK 修复记录 |

### 架构图更新

`README.md` 和 `ARCHITECTURE.md` 更新组件拓扑，新增 `approval.go` 和 `browser.go`。

---

## 四、验证结果

环境: `go 1.25.0 linux/amd64`

| 验证 | 结果 |
|------|------|
| `go test ./...` | PASS |
| `go vet ./cmd/eliza/` | PASS |
| `CGO_ENABLED=0 go build` | PASS |
| `make build-all` (6 平台) | PASS |
| 浏览器工具注册与操作 | PASS，linux/amd64 |
| 审批框 ↑↓ 切换 10+ 次稳定性 | PASS，plain + unicode |
| 多行粘贴（bracketed paste） | PASS，Linux TTY |
| 中文输入即时显示 | PASS，fcitx5 实测 |
| macOS arm64 构建 | PASS |
| `--version` 输出 | `ELIZA Agent v0.9.0` |

### 二进制大小

| 平台 | 大小 |
|------|------|
| linux/amd64 | ~9.7 MiB |
| linux/arm64 | ~9.2 MiB |
| darwin/amd64 | ~9.9 MiB |
| darwin/arm64 | ~9.4 MiB |
| windows/amd64 | ~10.2 MiB |

> 二进制增大约 3.5 MiB，主要来自 chromedp 依赖。CGO_ENABLED=0 静态编译，仍为单文件零运行时依赖。

---

## 五、工具全集 (v0.9.0)

| 工具 | 说明 | 权限 |
|------|------|------|
| `read_file` | 分段读取文件，受 FilePolicy 约束 | 受 workspace / readonly 限制 |
| `write_file` | 写入文件 | 需审批 |
| `run_command` | 执行 Shell 命令 | 受 CommandPolicy 双层限制 |
| `skill_list` | 列出可用技能 | 无限制 |
| `skill_view` | 按需加载技能 | 无限制 |
| `memory` | 持久记忆（save/recall/forget） | 需审批 |
| `view_image` | 视觉模型理解图片 | 无限制 |
| `browser_open` | 打开页面 | readonly 开放 |
| `browser_snapshot` | 读取页面快照 | readonly 开放 |
| `browser_reset` | 重置浏览器 | readonly 开放 |
| `browser_click` | 点击元素 | autopilot 专属 |
| `browser_type` | 输入文本 | autopilot 专属 |
| `browser_screenshot` | 截图保存 | autopilot 专属 |

**共计 13 个工具。** readonly 模式可用 10 个，autopilot 模式全开放。

---

## 六、安全模型

```
effective permission = global safety ∩ mode ∩ role ∩ one-time approval
```

**权限层次：**
1. **Global Safety** — 危险命令正则（`rm -rf` 等 9 条），代码层硬拦截
2. **Mode** — `readonly` 禁止 write_file 和交互型浏览器操作
3. **Role** — `writer` 不提供 run_command，`security` 不提供 write_file
4. **One-time Approval** — ↑↓ 选择式审批框，默认拒绝，批准仅本次生效

**不可信边界：** Memory / Skill 内容标注 `UNTRUSTED MEMORY SOURCE`，注入 system prompt 时保留边界标记。LLM 可读取参考，但不能提升权限、改变 mode 或绕过审批。

**审计：** Worklog 事件流（`events.jsonl`）全量记录，API key 自动脱敏。

---

## 七、下一步

v0.9.0 标志着稳定基线的确立。后续工作方向：

1. **标准化工具接口** — 将各工具拆成统一注册/权限/参数校验的单独封装，支持按场景选择性编译
2. **Agent 间协作** — 主子 Agent 生命周期、权限边界和记忆继承
3. **SQLite 记忆后端** — 替代 Markdown 文件，支持查询和结构化存储
4. **MCP 协议支持** — Model Context Protocol 集成，对接外部工具生态

---

## 变更文件清单

```
cmd/eliza/browser.go               — 新增 (769 行)
cmd/eliza/browser_test.go          — 新增 (73 行)
cmd/eliza/approval.go              — 新增 (80 行)
cmd/eliza/terminal.go              — 重写 (golang.org/x/term)
cmd/eliza/input.go                 — 修改 (bracketed paste + CJK + 粘贴折叠)
cmd/eliza/input_test.go            — 新增 (粘贴/CJK 测试)
cmd/eliza/input_pending_unix.go    — 新增 (输入未决检测)
cmd/eliza/input_pending_windows.go — 新增 (Windows stub)
cmd/eliza/ui.go                    — 修改 (审批框渲染 + 输入浮层)
cmd/eliza/ui_logo_test.go          — 新增 (Logo 布局测试)
cmd/eliza/agent.go                 — 修改 (审批流程 + 引导注入)
cmd/eliza/main.go                  — 修改 (版本号 + 浏览器配置)
cmd/eliza/tools.go                 — 修改 (浏览器工具 + 审批策略)
cmd/eliza/vision.go                — 修改 (view_image 工具)
cmd/eliza/memory.go                — 修改 (审批框适配)
cmd/eliza/plan.go                  — 修改 (审批框适配)
cmd/eliza/roles.go                 — 修改 (审批框适配)
cmd/eliza/compress.go              — 修改 (审批框适配)
cmd/eliza/contextbar.go            — 修改 (输入浮层适配)
go.mod / go.sum                    — 修改 (chromedp + x/term 依赖)
README.md                          — 修改 (工具表 + 更新说明)
ARCHITECTURE.md                    — 新版本信息
binaries/README.md                 — 新版本 + 浏览器说明
docs/CHANGELOG.md                  — 新增 v0.9.0 条目
docs/fix_20260630_browser_context_cancel.md         — 新增
docs/fix_20260701_approval_redraw.md                — 新增
docs/fix_20260701_input_paste_cjk_redraw.md         — 新增
```

---

**Powered By MUY & ELIZA — v0.9.0 首个稳定版本**
