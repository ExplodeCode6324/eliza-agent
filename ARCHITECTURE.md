# ELIZA Agent 架构

> 版本: v0.8.0  
> 日期: 2026-06-25  
> 目标: 内网 OpenAI-compatible API 的 Go 静态单二进制 Agent Runtime

## 组件

```text
CLI main.go
  ├─ early help/version
  ├─ read-only doctor.go
  └─ runtime initialization
       ├─ ui.go / contextbar.go
       ├─ agent.go request state machine
       │    ├─ llm.go SSE-only client
       │    ├─ tools.go Tool Policy + file/command tools
       │    ├─ roles.go role permission input
       │    ├─ memory.go approved persistent memory
       │    ├─ skill.go controlled local extensions
       │    ├─ plan.go resumable plan state machine
       │    └─ compress.go bounded context compaction
       └─ worklog.go unified event stream
```

## 请求数据流

1. 为每个用户请求创建独立 `RequestRuntime` 和 cancellable context。
2. 根据 global policy + mode + role 过滤 LLM 可见的 tool definitions。
3. LLM 请求强制 `stream=true`，SSE 增量组装 content、reasoning 与 tool calls。
4. TUI 实时展示正文；完整 assistant message 才提交 history 和 Worklog。
5. 工具执行前再次检查统一 Tool Policy；需要审批的调用使用一次性批准。
6. 工具结果加入 history 后继续 streaming LLM 轮次，直到最终正文、预算耗尽、失败或取消。
7. 所有事件同步追加到统一 Worklog；大输出进入 artifacts。

## 安全权限

```text
effective permission = global safety ∩ mode ∩ role ∩ one-time approval
```

- readonly 禁止 write_file，命令限制为只读白名单与参数校验。
- autopilot 仍受 workspace、blocked path、危险命令审批和 role 约束。
- writer 不提供 run_command，写文件逐次审批。
- security 不提供 write_file，run_command 强制 readonly。
- memory 与 skill 均标为不可信数据，不能改变上述交集。

## 持久化

所有路径以二进制目录为基准：

```text
.env
memory/{user,project,agent}.md
skills/<name>/SKILL.md
plans/plan_<id>.json
plans/plan_<id>.md
worklogs/YYYY-MM-DD/session_<id>/
  session.md
  events.jsonl
  artifacts/
```

Plan JSON 是执行状态投影；Worklog JSONL 是审计时间线。Memory 用原子替换写入，Plan 每步前后原子持久化，Worklog 每个事件追加并 Sync。

## LLM 兼容与恢复

- URL 规范化为 `TrimRight(baseURL, "/") + "/chat/completions"`。
- 非 SSE endpoint 返回兼容性错误，不回退到非流式实现。
- 建连阶段 408/429/5xx 和传输错误有限指数退避；收到正文或工具增量后不重放。
- 无 `[DONE]`、损坏 JSON、SSE error 与连接中断都返回带 incomplete 状态的诊断。
- Ctrl+C 关闭当前 stream；Agent 在每个尚未开始的工具前再次检查取消状态。

## CLI 生命周期

`--help`、`--version` 和 doctor 在 env 自动生成、目录创建、实例锁、Worklog 和网络对话初始化之前执行。正常启动才创建缺失运行目录与 memory 模板。doctor 只读检查本地配置和可选网络层，使用 0/1/2 退出码。

## 跨平台

- `CGO_ENABLED=0`。
- 构建目标：本机、linux/amd64、linux/arm64、windows/amd64。
- POSIX 使用 `sh` 与进程组终止；Windows 使用 `cmd.exe` 和平台进程适配。
- ANSI/Unicode 不可用时 Renderer 自动退化为纯文本。
