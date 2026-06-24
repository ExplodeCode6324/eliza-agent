# ELIZA-AGENT

单二进制 · 零依赖 · 拷走即用

v0.8.0 · CGO_ENABLED=0 · linux/amd64/arm64

---

一个面向企业内网的 CLI AI Agent。不装 Docker，不装 Python，不装 Node。
你只需要一个 6.8MB 的文件，拷过去就能跑。

```
$ scp eliza user@192.168.1.x:/opt/
$ ssh user@192.168.1.x '/opt/eliza --version'
ELIZA Agent v0.8.0
```

在内网环境里，"先装运行时再跑 Agent"是一条死路。生产服务器可能没有
包管理器。信创机器的 glibc 版本不可预测。安全策略禁止从公网拉镜像。
所以我们做了纯 Go 标准库、CGO_ENABLED=0 的静态编译——一个 ELF 文件，
不链任何 C 库，拔了网线一样跑。

---

Agent 的能力不等于大模型的能力。代码层才是最后的守门人。所以我们没有把
安全策略写在 system prompt 里靠 LLM 自觉遵守，而是写在 tools.go 的正则
匹配里。`rm` 不在 readonly 白名单里 → 代码直接拒绝，LLM 说什么都没用。
autopilot 模式放宽了命令范围，但正则命中照样弹 `/approve` 审批。

Memory 和 Skill 文件注入 system prompt 时会明确标注 UNTRUSTED SOURCE。
LLM 可以读取、参考、建议，但不能提权，不能改 mode，不能绕过审批。

每次对话生成完整审计链，API key 自动脱敏，出问题能回溯"谁让 Agent 做了什么"。

---

### 快速开始

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o eliza .
./eliza                    # 首次运行生成 .env，编辑后重新运行
./eliza -q "磁盘使用情况"    # 单次查询
./eliza doctor --offline    # 自检
```

### 工具

`read_file` · `write_file` · `run_command` · `skill_list` · `skill_view` · `memory`

### TUI 命令

`/mode readonly|autopilot` · `/role` · `/plan` · `/execute` · `/compress` · `/status` · `/clear` · `/new`

### 环境变量

`ELIZA_BASE_URL` · `ELIZA_API_KEY` · `ELIZA_MODEL` · `ELIZA_MODE` · `ELIZA_WORKSPACE_ROOTS` · `ELIZA_FILE_ALLOW_ABSOLUTE`

完整列表见 `.env`。

### 架构

16 个源文件，~3500 行 Go，没有框架。

```
main.go → agent.go → llm.go (SSE streaming)
              │
   ┌──────────┼──────────┐
   ▼          ▼          ▼
tools.go   skill.go   memory.go
双层Policy  按需加载   审批边界
```

### 许可证

MIT · Powered By MUY & ELIZA
