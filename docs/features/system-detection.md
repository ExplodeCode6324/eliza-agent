# 启动系统探测

> 文件: system.go、main.go、agent.go  
> 版本: v0.5.0

ELIZA 每次启动重新读取运行系统信息，不使用持久缓存。

探测内容:

- OS 和 CPU architecture。
- Linux 发行版，优先读取 /etc/os-release。
- 麒麟、Red Hat、CentOS 等 release 文件作为兼容回退。
- Linux kernel version。
- hostname。

探测结果会:

- 打印到启动 profile 和 TUI Banner。
- 注入本次会话的 system prompt。

工具与命令以 Linux/POSIX 为主要目标，但不能假设 bash、systemd 或特定 GNU 工具版本必然存在。面对 2015—2025 年跨度的 Linux、麒麟等系统，Agent 应先确认命令和系统能力，再选择执行方案。

Windows 作为退化兼容目标：启动时识别为 windows 后，system prompt 会要求使用 cmd.exe 可执行的 Windows 命令，构建过程提供 windows/amd64 二进制。
