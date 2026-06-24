# 测试说明

> 日期: 2026-06-25  
> 版本: v0.8.0

## 自动化命令

```bash
go test ./...
go test -race ./...
go vet ./...
make build
make build-all
```

覆盖范围：

- Agent 单请求状态机、step/tool budget 和异常响应。
- SSE content/reasoning/tool_calls 增量组装、base URL、建连重试、截断流不重放、取消和非 streaming 兼容性错误。
- Worklog sequence、session/request/tool 关联、脱敏、大输出 artifact 和损坏尾行恢复。
- readonly/autopilot + role 权限交集与 execution-time 复核。
- memory 初始化不覆盖、非交互禁写、批准/拒绝文件不变性。
- skill frontmatter、资源沙箱、损坏隔离和 disable 后拒绝加载。
- context compaction、checkpoint、emergency summary 与辅助 usage 隔离。
- help aliases、未知参数建议、plain Banner 无 ANSI 与窄宽度换行。
- 文件沙箱、软链接逃逸、分段读和命令策略既有回归测试。

## 手工只读验收

```bash
./eliza -h > h.txt
./eliza -help > help.txt
./eliza --help > long-help.txt
cmp h.txt help.txt
cmp h.txt long-help.txt

./eliza doctor --offline
```

对 doctor 前后文件路径、mtime 和大小做快照 diff，应无变化。网络环境允许时去掉 `--offline`，分别查看 DNS、TCP、TLS 与 HTTP 分类结果。

## 构建目标

`make build-all` 生成：

- `eliza-linux-amd64`
- `eliza-linux-arm64`
- `eliza-windows-amd64.exe`

本机 `make build` 生成 `eliza`。全部使用 `CGO_ENABLED=0`。
