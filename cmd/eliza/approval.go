package main

import (
	"fmt"
	"strings"
)

type ApprovalDecision string

const (
	ApprovalRejected             ApprovalDecision = "rejected"
	ApprovalApproved             ApprovalDecision = "approved"
	ApprovalRejectedWithGuidance ApprovalDecision = "rejected_with_guidance"
)

var approvalOptions = []string{
	"拒绝",
	"批准执行一次",
	"拒绝，并告诉 ELIZA 应该怎么做",
}

type ApprovalResult struct {
	Decision ApprovalDecision
	Guidance string
}

func (r ApprovalResult) Approved() bool {
	return r.Decision == ApprovalApproved
}

func (r ApprovalResult) Status() string {
	if r.Decision == "" {
		return string(ApprovalRejected)
	}
	return string(r.Decision)
}

func approvalDenied() ApprovalResult {
	return ApprovalResult{Decision: ApprovalRejected}
}

func approvalGranted() ApprovalResult {
	return ApprovalResult{Decision: ApprovalApproved}
}

func approvalGuidance(guidance string) ApprovalResult {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return approvalDenied()
	}
	return ApprovalResult{Decision: ApprovalRejectedWithGuidance, Guidance: guidance}
}

func cancelledApprovalMessage(result ApprovalResult) string {
	if strings.TrimSpace(result.Guidance) == "" {
		return "CANCELLED: 用户拒绝了需要审批的工具调用"
	}
	return "CANCELLED: 用户拒绝了需要审批的工具调用\n用户补充要求: " + result.Guidance + "\n请根据用户补充要求调整方案，不要重复请求已拒绝的操作。"
}

func cancelledMemoryMessage(result ApprovalResult) string {
	if strings.TrimSpace(result.Guidance) == "" {
		return "用户拒绝或取消了 memory 修改；文件未变化。"
	}
	return "用户拒绝了 memory 修改；文件未变化。\n用户补充要求: " + result.Guidance + "\n请根据用户补充要求调整方案，不要重复请求已拒绝的 memory 修改。"
}

func approvalResultFromSelection(renderer *Renderer, selected int) ApprovalResult {
	switch selected {
	case 1:
		return approvalGranted()
	case 2:
		renderer.Status("BLOCKED", "请输入希望 ELIZA 改怎么做（留空则仅拒绝）")
		fmt.Fprint(renderer.out, "\nUSER> ")
		line, err := readTerminalLine()
		if err != nil && line == "" {
			return approvalDenied()
		}
		return approvalGuidance(line)
	default:
		return approvalDenied()
	}
}
