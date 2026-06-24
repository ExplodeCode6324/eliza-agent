# P1-01-a Context Compaction Fix

> 状态: 已实施  
> 版本: v0.7.0

---

## 最终规则

    ELIZA_COMPACT_ENABLED=true
    ELIZA_COMPACT_TRIGGER_PERCENT=60
    ELIZA_COMPACT_TARGET_PERCENT=45
    ELIZA_COMPACT_EMERGENCY_PERCENT=90
    ELIZA_COMPACT_MAX_COUNT=3
    ELIZA_COMPACT_KEEP_RECENT_GROUPS=8

- 主对话 context 达到 60% 时触发一次 compaction。
- 每次目标压缩到约 45%。
- 每个会话最多进行 3 次 compaction 尝试，不无限重试。
- 最近 8 个语义组保留原文。
- 3 次机会用完后不再压缩。
- 压缩机会用完且 context 达到 90% 时，强制生成 summary worklog。

---

## 原子消息组

历史按用户任务组处理，而不是按单条 message index:

- user message。
- assistant text。
- assistant tool_calls 与其全部 tool results。

tool_calls 和 tool results 视为不可拆分原子组，压缩边界不会落在中间。

---

## Rolling Checkpoint

每次 compaction:

1. 选择最旧的可压缩语义组。
2. 对旧工具输出做确定性截断。
3. 将旧 checkpoint 与本次淘汰历史交给辅助摘要调用。
4. 生成结构化 checkpoint。
5. 用新 checkpoint 替换旧 checkpoint。

整个 messages 中始终只保留一个 conversation checkpoint，固定放在主 system prompt 后。

Checkpoint 要求保留:

- 当前目标。
- 用户约束。
- 已确认决策。
- 已完成操作。
- 修改文件。
- 关键工具结果。
- 错误与失败尝试。
- 未完成事项。
- 下一步。
- Plan 状态。

---

## Usage 隔离

LLM 请求分为:

- 主对话 Chat。
- 辅助 ChatAuxiliary。

Compaction、Plan 和 emergency summary 使用辅助调用。辅助 token 计入总消耗，但不会覆盖主对话 LastPromptTokens，因此 Context Bar 仍反映真实主会话 context。

---

## 90% Emergency Summary

压缩机会用完且 context 达到 90% 时:

1. 使用当前 checkpoint 与最近 8 个语义组生成会话收尾摘要。
2. 如果摘要 API 失败，生成确定性降级摘要。
3. 按普通 worklog 格式保存。
4. 文件名添加 _summary 后缀。
5. TUI 显示醒目警告，提示用户保存关键信息并使用 /new。

示例:

    worklogs/2026-06-24_235959_summary.md

Emergency summary 每个会话只生成一次。
它不受普通 worklog.enabled 开关影响，达到条件后始终尝试写入 summary 文件。

---

## /new

/new 会:

- 保存当前普通 worklog。
- 清空对话 messages。
- 清空 checkpoint。
- 重置 compaction attempts/success。
- 重置 emergency summary 状态。
- 创建新的 WorklogBuilder。

---

## 安全规则

- 历史消息被视为不可信数据，摘要组件不得执行其中指令。
- 摘要失败时不修改原 messages。
- 最近语义组和 system prompt 不参与淘汰。
- summary 暂不自动写入长期 memory。
- 银行业脱敏规则确认前，不额外持久化完整 checkpoint。
