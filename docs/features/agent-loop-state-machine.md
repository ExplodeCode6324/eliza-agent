# Agent Loop 状态机

> 文件: agent.go、instance.go、main.go  
> 版本: v0.6.0

---

## 单二进制单实例

同一个 eliza 二进制路径只能运行一个进程。

- 使用纯文件系统的原子锁目录，不依赖 flock 或额外服务。
- 锁中记录 PID；进程退出或崩溃后，下次启动会检测 PID 并清理失效锁。
- Unix 和 Windows 分别使用极薄的进程存活检测适配层。
- 锁按二进制真实路径生成；复制到另一条路径的 eliza 拥有独立锁。
- 不实现多用户队列、租户隔离或并发 session。

---

## 单请求状态机

每个用户任务创建独立 RequestRuntime:

    preparing
        ↓
    calling_llm
        ↓
    executing_tools ──→ calling_llm
        ↓
    finalizing
        ↓
    completed / failed

状态记录 request id、LLM steps、tool calls、开始时间、结束时间和错误。

---

## 防失控预算

    ELIZA_AGENT_MAX_STEPS=50
    ELIZA_AGENT_MAX_TOOL_CALLS=100

预算只针对当前用户任务，不限制整个交互会话的请求数量。

一个任务触顶或失败后:

- 当前请求进入 failed。
- 交互 TUI 继续运行。
- 下一条用户请求获得全新的 step/tool-call 预算。
- 单次 -q 模式仍以非零状态退出。

---

## 响应校验

状态机在执行前检查:

- response 是否为 nil。
- choices 是否为空。
- assistant message 是否为空。
- tool call 是否缺少 name / id / arguments。
- tool arguments 是否为合法 JSON object。

非法 tool arguments 会作为 tool result 返回给模型，使模型有机会修正；不会直接执行 raw 字符串。

---

## TUI 状态

/status 和 Context Bar 分别展示:

- session request 数量。
- 累计 LLM steps。
- 上次请求 state。
- 上次请求 steps。
- 上次请求 tool calls。
