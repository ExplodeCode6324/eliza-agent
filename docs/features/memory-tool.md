# Memory 可信边界

首次正常启动只在文件不存在时创建二进制同目录的：

- `memory/user.md`
- `memory/project.md`
- `memory/agent.md`

已有内容永不被初始化逻辑覆盖。三个文件都作为 `UNTRUSTED MEMORY SOURCE` 注入，优先级低于 system prompt、mode、role、Tool Policy 与审批规则。单文件和总注入量分别受 `ELIZA_MEMORY_MAX_FILE_BYTES`、`ELIZA_MEMORY_MAX_TOTAL_BYTES` 限制。

`memory` 工具支持 `recall`、`save`、`forget`。save/forget 会展示目标文件和精确新增/删除内容，并只接受本次 TUI 审批；拒绝、取消或非交互 `-q` 模式都不会写盘。autopilot 和 `agent.md` 不能绕过审批。

`/memory` 只显示文件是否存在、大小和授权规则，不直接泄露全部内容。审批、拒绝和写入结果进入统一 Worklog。
