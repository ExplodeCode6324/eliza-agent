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
	"Deny",
	"Approve once",
	"Deny and tell ELIZA what to do",
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
		return "CANCELLED: user denied the tool call that required approval"
	}
	return "CANCELLED: user denied the tool call that required approval\nUser guidance: " + result.Guidance + "\nAdjust the next step according to the user's guidance. Do not repeat the denied operation."
}

func cancelledMemoryMessage(result ApprovalResult) string {
	if strings.TrimSpace(result.Guidance) == "" {
		return "User denied or cancelled the memory change; files were not modified."
	}
	return "User denied the memory change; files were not modified.\nUser guidance: " + result.Guidance + "\nAdjust the next step according to the user's guidance. Do not repeat the denied memory change."
}

func approvalResultFromSelection(renderer *Renderer, selected int) ApprovalResult {
	switch selected {
	case 1:
		return approvalGranted()
	case 2:
		renderer.Status("BLOCKED", "Tell ELIZA what to do instead (empty = deny only)")
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
