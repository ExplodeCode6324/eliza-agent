# Role 与 Tool Policy

Role 同时影响 prompt、LLM 可见工具和执行层授权。有效权限始终是全局策略、mode、role 与单次审批的交集，只能收紧，不能放宽。

| Role | 能力 |
|---|---|
| default | 当前 mode 允许的常规工具 |
| coder | 代码读写和受控命令；readonly 下仍只读 |
| ops | 系统诊断和受控运维；写操作仍受策略约束 |
| writer | 不暴露 run_command；write_file 每次审批 |
| security | 不暴露 write_file；run_command 强制 readonly 校验 |

每次 LLM 请求前过滤 tool definitions，真正执行前再次调用统一策略校验。`/mode` 或 `/role` 切换后立即重建 system 边界和可见工具；`/tools` 展示启用/禁用工具及原因。伪造 tool call、memory 或 skill 指令都无法绕过执行层。
