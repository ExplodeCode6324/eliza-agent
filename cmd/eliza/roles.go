package main

import (
	"fmt"
	"sort"
	"strings"
)

type Role struct {
	Name          string
	Label         string
	Description   string
	Prompt        string
	AllowedTools  map[string]bool // nil means all registered tools.
	ForceReadonly bool
}

var builtinRoles = map[string]Role{
	"default":  {Name: "default", Label: "默认助手", Description: "当前 mode 允许的常规工具", Prompt: defaultSystemPrompt},
	"coder":    {Name: "coder", Label: "程序员", Description: "代码读写与受控命令；不突破 workspace 或审批", Prompt: `你是 ELIZA 编程专家。进行代码审查、重构、调试和架构设计。使用中文，专业术语保留英文。所有工具仍受当前 mode、workspace 与审批策略约束。`},
	"ops":      {Name: "ops", Label: "运维工程师", Description: "系统诊断与受控运维；写操作仍受策略约束", Prompt: `你是 ELIZA 运维专家。进行系统诊断、部署建议、故障排查和监控分析。使用中文，生产操作说明风险。所有工具仍受当前 mode、workspace 与审批策略约束。`},
	"writer":   {Name: "writer", Label: "文档写手", Description: "文件读取与受控文档写入；不提供 run_command", Prompt: `你是 ELIZA 技术写作专家。输出结构清晰的中文 Markdown 文档。不可执行命令，写文件仍受当前 mode、workspace 与审批策略约束。`, AllowedTools: toolSet("read_file", "write_file", "skill_list", "skill_view", "memory")},
	"security": {Name: "security", Label: "安全审计", Description: "强制只读；不提供 write_file", Prompt: `你是 ELIZA 安全审计专家。按风险等级给出证据和修复建议，只审计不修改。任何命令都必须满足 readonly 策略。`, AllowedTools: toolSet("read_file", "run_command", "skill_list", "skill_view", "memory"), ForceReadonly: true},
}

func toolSet(names ...string) map[string]bool {
	result := map[string]bool{}
	for _, name := range names {
		result[name] = true
	}
	return result
}

func roleToolAllowed(roleName, tool string) (bool, string) {
	role, ok := builtinRoles[roleName]
	if !ok {
		return false, "unknown role"
	}
	if role.AllowedTools != nil && !role.AllowedTools[tool] {
		return false, fmt.Sprintf("role %s does not expose %s", roleName, tool)
	}
	return true, ""
}

func (a *Agent) switchRole(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	role, ok := builtinRoles[name]
	if !ok {
		available := make([]string, 0, len(builtinRoles))
		for key := range builtinRoles {
			available = append(available, key)
		}
		sort.Strings(available)
		return fmt.Errorf("未知角色 %q（可用: %s）", name, strings.Join(available, ", "))
	}
	if err := a.registry.SetRole(name); err != nil {
		return err
	}
	a.roleName = name
	scanSkills()
	if len(a.messages) > 0 && a.messages[0].Role == "system" {
		a.messages[0].Content = a.systemPrompt()
	}
	if a.worklog != nil {
		_ = a.worklog.RecordEvent("role.changed", "completed", "", "", map[string]any{"role": name, "description": role.Description})
	}
	a.ui.Status("PASS", "角色已切换为 %s — %s", role.Label, role.Description)
	return nil
}

func (a *Agent) listRoles() {
	a.ui.Title("角色")
	for _, name := range []string{"default", "coder", "ops", "writer", "security"} {
		role := builtinRoles[name]
		marker := " "
		if a.roleName == name {
			marker = "*"
		}
		tools := "all mode-permitted tools"
		if role.AllowedTools != nil {
			names := make([]string, 0, len(role.AllowedTools))
			for tool := range role.AllowedTools {
				names = append(names, tool)
			}
			sort.Strings(names)
			tools = strings.Join(names, ",")
		}
		fmt.Fprintf(a.ui.out, "%s %-10s %-12s %s | tools=%s", marker, role.Name, role.Label, role.Description, tools)
		if role.ForceReadonly {
			fmt.Fprint(a.ui.out, " | forced-readonly")
		}
		fmt.Fprintln(a.ui.out)
	}
	fmt.Fprintln(a.ui.out, "用法: /role <name>")
}
