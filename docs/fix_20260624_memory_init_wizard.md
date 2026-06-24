# fix_20260624_memory_init_wizard.md

> **日期**: 2026-06-24  
> **类型**: 功能增强  
> **影响**: 记忆系统 — 结构化模板 + 首次启动引导

---

## 触发条件

记忆系统初始化时只有无结构的一句话描述，LLM 缺乏分区引导，写入内容散乱。
同时首次启动无任何提示，用户不知道需要初始化记忆。

## 根因

旧模板：
```markdown
# User Memory
用于保存经用户明确批准的偏好与个人信息。
```
无分区、无引导、无时间戳。三个文件同构。

## 修复方案

### A: 结构化模板

每个记忆文件预设分区：

| 文件 | 分区 |
|------|------|
| `user.md` | 偏好 (Preferences) / 个人信息 (Personal Info) |
| `project.md` | 工作区 (Workspace) / 规范 (Conventions) / 约束 (Constraints) |
| `agent.md` | 经验 (Lessons Learned) / 环境 (Environment) / 知识 (Knowledge) |

### B: 首次启动引导

每个模板插入 `<!-- INIT_REQUIRED -->` HTML 注释标记，附带中文引导文本：

```markdown
<!-- INIT_REQUIRED -->
> **记忆系统初始化向导**
> 请向 ELIZA 描述关于你自己的信息，ELIZA 会整理并调用 `memory save` 写入。
> 每条写入都会弹审批确认，由你逐次批准。
> 完成全部初始化后，请告知 ELIZA，ELIZA 会删除此引导信息。
```

### C: 启动检测

`memoryInitStatus()` 扫描三个文件，任一含 `<!-- INIT_REQUIRED -->` 即视为未初始化。
`RunInteractive()` 在 Banner 后调用，显示：

```
WARN     记忆系统未初始化 — 首次启动向导
WARN     请向我描述你自己和项目背景，我会引导你完成初始化
```

### D: 审批提示简化

Memory 审批提示从 `[y/N]` 改为 `[/approve OR /deny]`，与命令审批统一。
目标路径从绝对路径改为文件名，避免 Agent 尝试用绝对路径调用 `read_file`。

### E: 文件策略放宽

`ELIZA_FILE_ALLOW_ABSOLUTE` 默认值从 `false` 改为 `true`。
读取绝对路径本身不危险 — workspace roots + blocked paths + readonly paths 已提供三层沙箱。

### 改动文件

| 文件 | 改动 |
|------|------|
| `memory.go` | 模板重写 (+44/-3)；新增 `memoryInitStatus()` (+15)；审批 prompt 改 3 处 |
| `agent.go` | `RunInteractive()` 增加启动检测 (+4) |
| `main.go` | `AllowAbsolute` 两处默认值 `false` → `true`；`.env` 模板同步 |

## 验证

| 项目 | 结果 |
|------|------|
| `go vet` | ✅ 无新增警告 |
| `go build` | ✅ CGO=0, 6.8M |
| 首次启动引导 | ✅ Banner 后显示 WARN 提示 |
| 结构化填充 | ✅ LLM 按分区调用 `memory save` |
| 审批流程 | ✅ `/approve` / `/deny` 正常 |
| 标记清理 | ✅ 初始化完成后 `memory forget` 删除 `<!-- INIT_REQUIRED -->` |
