# Context Compaction

> 文件: compress.go、llm.go、agent.go  
> 版本: v0.7.0

ELIZA 使用有限次数 rolling checkpoint 管理长上下文。

核心参数:

- 60% 触发。
- 45% 目标。
- 最多 3 次尝试。
- 保留最近 8 个语义组。
- 90% 生成 summary worklog 并提示开启新会话。

详细设计和 emergency 行为见 context-compaction-proposal.md。

