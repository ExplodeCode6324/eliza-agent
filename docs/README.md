# ELIZA-AGENT (DRC Bank ver.)

> **版本**: v0.8.0  
> **语言**: Go 1.23+  
> **目标**: 内网轻量级 AI Agent，单二进制部署，零运行时依赖

---

## 架构总览

```
┌─────────────┐    ┌──────────────┐    ┌─────────────────┐
│   main.go   │───▶│   agent.go    │───▶│    llm.go        │
│  CLI 入口    │    │  状态机 Loop  │    │  SSE streaming   │
│  配置加载    │    │  Plan/Memory  │    │  重试/取消        │
└─────────────┘    └──────┬───────┘    └─────────────────┘
                          │
         ┌────────────────┼────────────────┐
         ▼                ▼                ▼
   ┌──────────┐   ┌──────────┐   ┌──────────────┐
   │ tools.go │   │ skill.go │   │  memory.go    │
   │ 命令/文件 │   │ 技能系统  │   │  持久记忆      │
   │ 双层策略  │   │ 按需加载  │   │  审批边界      │
   └──────────┘   └──────────┘   └──────────────┘
```

### 核心模块

| 文件 | 职责 |
|------|------|
| `main.go` | CLI 入口、配置加载、`.env` 自动生成、实例锁 |
| `agent.go` | Agent 状态机 Loop、Plan/Role/Mode 管理 |
| `llm.go` | OpenAI 兼容 SSE streaming、重试、取消、token 统计 |
| `tools.go` | read_file / write_file / run_command + 双层 Policy |
| `skill.go` | 技能扫描/索引/skill_list/skill_view |
| `memory.go` | 持久记忆 (user/project/agent.md) + 审批 |
| `plan.go` | Plan/Step 状态机、持久化、恢复 |
| `roles.go` | 角色定义 + Tool Policy 绑定 |
| `compress.go` | Context Compaction (token budget 驱动) |
| `contextbar.go` | Context 用量条 |
| `ui.go` | 终端 UI (Braille Logo、框线、颜色) |
| `doctor.go` | `--doctor` 诊断 |
| `system.go` | OS/架构/发行版/内核探测 |
| `instance.go` | 同二进制单实例锁 |
| `worklog.go` | 会话审计 (session.md + events.jsonl + artifacts/) |

### 数据流

```
用户输入 → prepareMessages() → processLoop()
              │                      │
              ▼                      ▼
       system prompt            manageContext()
       + skill index            (compaction)
       + memory snapshot              │
       + system info                  ▼
                              LLM Chat (SSE)
                                   │
                         ┌─────────┴─────────┐
                         ▼                   ▼
                    tool_calls            text reply
                         │                   │
                         ▼                   ▼
                  executeToolCalls()    EndAssistant()
                  (Policy 校验 + 审批)    (drawBox)
                         │
                         ▼
                  回传 tool result → 继续 Loop
```

---

## 快速开始

```bash
# 编译
make build                    # linux/amd64
make build-all                # 全平台

# 运行
./eliza                       # 交互模式
./eliza -q "你好"             # 单次查询
./eliza --doctor              # 诊断模式
./eliza --version             # 版本号
```

首次运行自动生成 `.env` 模板，编辑后重新运行即可。

## 环境变量 (`.env`)

| 变量 | 说明 |
|------|------|
| `ELIZA_BASE_URL` | API 端点 |
| `ELIZA_API_KEY` | API Key |
| `ELIZA_MODEL` | 模型名 |
| `ELIZA_MODE` | 运行模式: readonly / autopilot |
| `ELIZA_COMMAND_TIMEOUT` | 命令超时 (秒) |
| `ELIZA_READONLY_COMMANDS` | 只读命令白名单 |
| `ELIZA_WORKSPACE_ROOTS` | 工作区根路径 |

完整配置见 `.env` 注释。

## 交互命令

| 命令 | 说明 |
|------|------|
| `/help` | 命令列表 |
| `/status` | 状态（轮次/消息/Context/角色） |
| `/clear` | 清空上下文 |
| `/new` | 新会话 |
| `/mode <name>` | 切换模式 |
| `/role <name>` | 切换角色 |
| `/plan <描述>` | 生成执行计划 |
| `/execute` | 执行计划 |
| `/skills` | 技能管理 |
| `/memory` | 记忆状态 |
| `/compress` | 手动压缩 |
| `/tools` | 工具权限 |

## 工具

| 工具 | 说明 |
|------|------|
| `read_file` | 读取文件（支持 offset/limit） |
| `write_file` | 写入文件（需审批） |
| `run_command` | 执行命令（受 Policy 约束） |
| `skill_list` | 列出技能 |
| `skill_view` | 加载技能 |
| `memory` | 持久记忆 |

## 目录结构

```
ELIZA_Agent/
├── *.go               # 源代码
├── .env               # 环境变量（自动生成）
├── config.json        # 配置文件
├── Makefile           # 编译脚本
├── skills/            # 技能（用户维护）
├── memory/            # 持久记忆（自动生成）
├── docs/              # 文档
│   ├── README.md      # 本文档
│   ├── CHANGELOG.md   # 变更记录
│   ├── features/      # 功能设计文档
│   └── old/           # 历史中间文件
├── worklogs/          # 会话审计（运行时生成）
└── plans/             # 执行计划（运行时生成）
```

---

## 热修复规则

**从 v0.8.0 起，所有代码改动视为热修复。**

每次修复必须产出文档：

```
docs/fix_YYYYMMDD_changeinfo.md
```

**格式要求**：
- 标题：修复内容简述
- 记录：触发条件、根因、修复方案、改动文件、验证结果

示例：`docs/fix_20260625_streaming_timeout.md`

---

## 设计原则

- **内网优先**：默认面向内网 OpenAI-compatible API
- **零依赖**：纯 Go 标准库，单二进制
- **Linux 优先**：工具面向 POSIX，Windows 可退化
- **安全边界显式化**：双层 Policy，不可信数据不能提权
- **可审计**：events.jsonl 全量记录
- **可拷贝即用**：二进制 + 同目录 .env 即可运行
