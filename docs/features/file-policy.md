# 文件沙箱与内存保护

> 文件: main.go、tools.go、agent.go  
> 版本: v0.5.0

---

## 目标

read_file 和 write_file 必须在配置的 workspace roots 内工作，并防止路径穿越、软链接逃逸、大文件整块载入和异常参数导致的 panic。

---

## .env 配置

    ELIZA_FILE_BASE_DIR=.
    ELIZA_WORKSPACE_ROOTS=/
    ELIZA_FILE_BLOCKED_PATHS=.env;/proc/kcore;/dev/mem;/dev/kmem
    ELIZA_FILE_READONLY_PATHS=/proc;/sys;/dev
    ELIZA_FILE_ALLOW_ABSOLUTE=true
    ELIZA_FILE_MAX_READ_BYTES=1048576
    ELIZA_FILE_MEMORY_PERCENT=25

说明:

- ELIZA_FILE_BASE_DIR: 相对路径的解析基础目录；点号代表二进制目录。
- ELIZA_WORKSPACE_ROOTS: 允许访问的根目录，多个路径使用英文分号分隔。
- ELIZA_FILE_BLOCKED_PATHS: 禁止读取和写入的路径；默认包含运行时 .env。
- ELIZA_FILE_READONLY_PATHS: 允许读取但禁止 write_file 写入的路径。
- ELIZA_FILE_ALLOW_ABSOLUTE: 是否接受绝对路径参数。
- ELIZA_FILE_MAX_READ_BYTES: 单次 read_file 最大字节数。
- ELIZA_FILE_MEMORY_PERCENT: 文件读取可使用的系统内存比例，上限固定为 25。

新生成的默认 .env 仅允许访问二进制目录。当前开发测试 .env 为保持原有运维能力，将 workspace root 设置为根目录 /。

---

## 路径安全

文件访问前执行以下步骤:

1. 相对路径基于 ELIZA_FILE_BASE_DIR 解析。
2. 解析绝对路径并清理点号与父目录跳转。
3. 解析软链接真实目标。
4. 校验真实目标是否位于 workspace roots。
5. 校验 blocked paths。
6. write_file 额外校验 readonly paths。

对于尚不存在的写入目标，会从最近存在的父目录开始解析软链接，防止通过软链接父目录逃出 workspace。

---

## 大文件读取

read_file 使用 os.Open、Seek 和有界 buffer，只读取请求片段，不再使用 os.ReadFile 载入整个文件。

- 默认 limit: 10000 字节。
- offset 和 limit 必须是非负/正整数。
- limit 超出配置或内存预算时自动降低，并返回 LIMIT_APPLIED。
- 未读完时返回 TRUNCATED、remaining 和 next_offset。
- agent 二次截断时保留输出尾部，确保 next_offset 不会丢失。

---

## 25% 内存硬上限

Linux 上每次读取前获取:

- /proc/meminfo 的 MemTotal 和 MemAvailable。
- /proc/self/status 的 VmRSS。

ELIZA 进程的文件读取内存预算不得超过系统总内存的 25%。由于读取 buffer 转为 string 时会短暂存在两份数据，实际允许读取字节数按剩余预算再除以 2，并同时受可用内存约束。

ELIZA_FILE_MEMORY_PERCENT 可以设置得更低，但大于 25 时会自动收紧为 25。

非 Linux 开发环境缺少 /proc 指标时，使用 ELIZA_FILE_MAX_READ_BYTES 作为降级上限；Linux 部署会启用完整内存保护。
