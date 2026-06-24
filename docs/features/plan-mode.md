# Plan 状态机

Plan 状态：`draft`、`ready`、`running`、`paused`、`completed`、`failed`、`cancelled`。Step 状态：`pending`、`running`、`completed`、`failed`、`skipped`、`cancelled`。

每个 plan/step 有稳定 ID、时间戳、attempt 计数、历史结果和最后摘要。同一会话只允许一个 active plan。

流程：

1. `/plan <任务>` 通过 streaming 辅助请求生成 checklist，保存 draft。
2. `/execute` 是用户确认，逐步把 pending step 原子更新为 running。
3. 每步结束立即写入 `plans/plan_<id>.json` 和 Markdown 投影，并追加 Worklog 事件。
4. 失败 step 进入 failed，plan 自动 paused，后续步骤不执行。
5. `/retryplan`、`/skipstep`、`/cancelplan` 保留尝试历史；skip 需确认。

启动会发现最近的 active plan。异常退出时仍为 running 的计划会恢复成 paused，running step 标为 failed；已完成步骤不会重跑。`/new` 会询问保留或取消 active plan。所有执行继续受 mode、role、Tool Policy、memory 与危险命令审批约束。
