# fix_20260624_command_approval_tui.md

> **日期**: 2026-06-24  
> **类型**: 热修复（安全 + UX）  
> **影响**: 危险命令审批机制 — 从 `[y/N]` 改为 TUI 命令 `/approve` / `/deny`

---

## 触发条件

1. `rm file.txt` 等普通删除命令在 autopilot 模式下不触发确认，Agent 直接执行
2. 原审批弹框使用 `[y/N]` 单字符确认，不够显式，容易误操作

## 根因

当前 `dangerous_patterns` 只匹配少数极端场景：

| 模式 | 匹配 | 漏掉 |
|------|------|------|
| `rm\s+-rf\s+/` | `rm -rf /` | `rm file.txt` |
| `rm\s+-rf\s+\*` | `rm -rf *` | `rm -r dir/` |
| `sudo\s+rm` | `sudo rm` | 非 sudo 的 rm |
| `>\s*/dev/sd` | `> /dev/sda` | `> output.txt` |

`rm file.txt` 不匹配任何模式 → `AuthorizeCommand` 返回 `(false, nil)` → 视为普通命令直接执行。

## 修复方案

### A: 审批命令改为 TUI 命令

原机制：
```
Agent: 危险命令: rm file.txt
      确认执行? [y/N]: _
用户: y
```

新机制：
```
BLOCKED  危险命令: rm file.txt
BLOCKED  输入 /approve 确认执行，/deny 拒绝
USER> /approve
PASS     已批准: 危险命令: rm file.txt

USER> /deny
WARN     已拒绝: 危险命令: rm file.txt
```

输入其他内容时循环提示，直到输入 `/approve` 或 `/deny`。

### B: 扩展危险模式

新增两条：

| 新增模式 | 匹配范围 |
|----------|----------|
| `\brm\b` | 任何独立 `rm` 命令（含 `rm file.txt`、`rm -rf dir/` 等） |
| `>\s*\S` | 任何输出重定向（覆盖 `> file.txt`、`> /dev/sda` 等） |

### C: 统一审批入口

三个审批点全部改为调用 `Agent.approvalLoop()`：

| 审批点 | 之前 | 之后 |
|--------|------|------|
| `run_command` 危险命令 | `agent.ui.Confirm(prompt)` → `[y/N]` | `agent.approvalLoop(prompt)` → `/approve` |
| `write_file` | `agent.ui.Confirm(prompt)` → `[y/N]` | `agent.approvalLoop(prompt)` → `/approve` |
| `memory` save/forget | `MemoryTool.confirmFn(prompt)` → `[y/N]` | `agent.approvalLoop(prompt)` → `/approve` |

### 改动文件

| 文件 | 改动 |
|------|------|
| `agent.go` | 新增 `approvalLoop()` 方法（+31 行）；`executeToolCalls()` 审批调用改为 `approvalLoop` |
| `main.go` | `registry.confirmFn` 改为 `agent.approvalLoop`；`MemoryTool.confirmFn` 同；默认 dangerous_patterns +2；`.env` 模板同步 |
| `config.json` | `dangerous_patterns` +2 |

### 完整性矩阵

| 场景 | autopilot 模式 | readonly 模式 |
|------|---------------|---------------|
| `rm file.txt` | ✅ 弹 /approve | ✅ 白名单拒绝 |
| `rm -rf dir/` | ✅ 弹 /approve | ✅ 白名单拒绝 |
| `rm -rf /` | ✅ 弹 /approve | ✅ 白名单拒绝 |
| `echo hi > a.txt` | ✅ 弹 /approve | ✅ 白名单拒绝 |
| `cat file` | ✅ 直接执行 | ✅ 白名单允许 |
| `ls -la` | ✅ 直接执行 | ✅ 白名单允许 |

## 验证

| 项目 | 结果 |
|------|------|
| `go vet` | ✅ 无新增警告 |
| `go build` | ✅ CGO=0, 6.8M |
