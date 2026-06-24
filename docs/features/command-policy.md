# 命令策略与运行模式

> 文件: main.go、tools.go、agent.go  
> 版本: v0.4.0

---

## 目标

ELIZA 提供两种运行模式，并允许通过二进制同目录 .env 设置默认模式和命令策略。

| 模式 | 行为 |
|------|------|
| readonly | 禁用 write_file；run_command 仅允许白名单内的只读命令 |
| autopilot | 允许全部命令；命中危险正则时必须由用户在 TUI 中审批 |

---

## TUI 命令

    /mode
    /mode readonly
    /mode autopilot

当前模式会显示在启动 Banner、输入提示符、/status 和 Context Bar 中。

模式切换只影响当前进程，不会自动修改 .env。重新启动后的默认模式由 ELIZA_MODE 决定。

---

## .env 配置

    ELIZA_MODE=autopilot
    ELIZA_COMMAND_TIMEOUT=60
    ELIZA_COMMAND_MAX_OUTPUT=65536
    ELIZA_READONLY_COMMANDS=ls,pwd,cat,head,tail,grep,rg,find,stat,file,wc,du,df,free,uptime,uname,whoami,id,ps,ss,netstat,lsof,systemctl,journalctl,dmesg,printenv,date,hostname,which,whereis,realpath,readlink
    ELIZA_DANGEROUS_PATTERNS=rm\s+-rf\s+/;rm\s+-rf\s+\*;dd\s+if=;mkfs;>\s*/dev/sd;chmod\s+777;sudo\s+rm

配置说明:

- ELIZA_MODE: readonly 或 autopilot。
- ELIZA_COMMAND_TIMEOUT: 单条命令超时秒数。
- ELIZA_COMMAND_MAX_OUTPUT: stdout + stderr 最大保留字节数。
- ELIZA_READONLY_COMMANDS: readonly 白名单，英文逗号分隔。
- ELIZA_DANGEROUS_PATTERNS: autopilot 危险正则，英文分号分隔。

---

## readonly 校验

readonly 模式采用保守策略:

- 拒绝分号、重定向、后台执行、命令替换等 shell 控制符。
- 管道两侧的每个命令都必须在白名单中。
- 阻止 find -delete / -exec。
- systemctl 仅允许 status、show、cat、is-active、is-enabled 等查询动作。
- 阻止 journalctl 清理、dmesg 清空、ss kill、date set、hostname 修改。
- write_file 不会提供给模型；即使模型尝试调用，也会被工具层拒绝。

不要把 sh、bash、env、python、perl、xargs 等可转执行任意命令的程序加入 readonly 白名单，否则可能绕过只读限制。

---

## autopilot 审批

autopilot 允许运行任意命令。命令命中 ELIZA_DANGEROUS_PATTERNS 时，TUI 会显示完整命令并要求输入 y 才执行；其他输入均取消。

命令执行使用真实超时，并限制输出大小。超时或截断会在工具结果中出现明确标记。

