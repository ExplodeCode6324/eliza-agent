package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── 工具定义 ──────────────────────────────────────────────────────

type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ─── 工具接口 ──────────────────────────────────────────────────────

// Tool implementations do not keep private logs. The Worklog manager is the
// only audit writer and records every call from the Agent execution layer.
type Tool interface {
	Definition() ToolDef
	Execute(args map[string]any) (string, error)
}

type ContextTool interface {
	ExecuteContext(context.Context, map[string]any) (string, error)
}

// ─── 命令策略 ─────────────────────────────────────────────────────

const (
	ModeReadonly  = "readonly"
	ModeAutopilot = "autopilot"
)

type CommandPolicy struct {
	mu               sync.RWMutex
	mode             string
	dangerous        []*regexp.Regexp
	readonlyCommands map[string]struct{}
}

func DefaultReadonlyCommands() []string {
	if runtime.GOOS == "windows" {
		return []string{
			"dir", "type", "findstr", "where", "whoami", "hostname",
			"systeminfo", "tasklist", "ipconfig", "netstat", "set", "ver", "echo",
		}
	}
	return []string{
		"ls", "pwd", "cat", "head", "tail", "grep", "rg", "find",
		"stat", "file", "wc", "du", "df", "free", "uptime", "uname",
		"whoami", "id", "ps", "ss", "netstat", "lsof", "systemctl",
		"journalctl", "dmesg", "printenv", "date", "hostname", "which",
		"whereis", "realpath", "readlink",
	}
}

func NewCommandPolicy(mode string, patterns []string, readonlyCommands []string) (*CommandPolicy, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = ModeReadonly
	}
	if mode != ModeReadonly && mode != ModeAutopilot {
		return nil, fmt.Errorf("未知模式 %q，可用: readonly / autopilot", mode)
	}

	policy := &CommandPolicy{
		mode:             mode,
		readonlyCommands: make(map[string]struct{}),
	}
	for _, name := range readonlyCommands {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			policy.readonlyCommands[name] = struct{}{}
		}
	}
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("非法危险命令正则 %q: %w", pattern, err)
		}
		policy.dangerous = append(policy.dangerous, re)
	}
	return policy, nil
}

func (p *CommandPolicy) Mode() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *CommandPolicy) SetMode(mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != ModeReadonly && mode != ModeAutopilot {
		return fmt.Errorf("未知模式 %q，可用: readonly / autopilot", mode)
	}
	p.mu.Lock()
	p.mode = mode
	p.mu.Unlock()
	return nil
}

// AuthorizeCommand 返回是否需要用户审批。
func (p *CommandPolicy) AuthorizeCommand(command string) (bool, error) {
	mode := p.Mode()
	if mode == ModeReadonly {
		return false, p.validateReadonly(command)
	}
	for _, re := range p.dangerous {
		if re.MatchString(command) {
			return true, nil
		}
	}
	return false, nil
}

func (p *CommandPolicy) validateReadonly(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("命令为空")
	}

	blockedSyntax := []string{";", "&&", "||", "&", ">", "<", string(rune(96)), "$(", "\n", "\r"}
	for _, token := range blockedSyntax {
		if strings.Contains(command, token) {
			return fmt.Errorf("readonly 模式禁止 shell 控制符 %q", token)
		}
	}

	for _, segment := range strings.Split(command, "|") {
		fields := strings.Fields(strings.TrimSpace(segment))
		if len(fields) == 0 {
			return fmt.Errorf("readonly 模式不允许空管道")
		}
		executable := strings.ToLower(filepath.Base(fields[0]))
		if _, ok := p.readonlyCommands[executable]; !ok {
			return fmt.Errorf("readonly 模式不允许命令 %q", executable)
		}
		if err := validateReadonlyArguments(executable, fields[1:]); err != nil {
			return err
		}
	}
	return nil
}

func validateReadonlyArguments(executable string, args []string) error {
	lowerArgs := make([]string, len(args))
	for i, arg := range args {
		lowerArgs[i] = strings.ToLower(arg)
	}
	joined := " " + strings.Join(lowerArgs, " ") + " "

	switch executable {
	case "find":
		for _, token := range []string{
			" -delete ", " -exec ", " -execdir ", " -ok ", " -okdir ",
			" -fprint ", " -fprint0 ", " -fprintf ", " -fls ",
		} {
			if strings.Contains(joined, token) {
				return fmt.Errorf("readonly 模式禁止 find 写入参数 %s", strings.TrimSpace(token))
			}
		}
	case "systemctl":
		action := ""
		for _, arg := range lowerArgs {
			if !strings.HasPrefix(arg, "-") {
				action = arg
				break
			}
		}
		allowed := map[string]bool{
			"": true, "status": true, "show": true, "cat": true,
			"is-active": true, "is-enabled": true, "is-failed": true,
			"list-units": true, "list-unit-files": true, "list-dependencies": true,
		}
		if !allowed[action] {
			return fmt.Errorf("readonly 模式禁止 systemctl %s", action)
		}
	case "journalctl":
		for _, arg := range lowerArgs {
			if strings.HasPrefix(arg, "--vacuum") ||
				arg == "--rotate" || arg == "--flush" || arg == "--sync" ||
				arg == "--relinquish-var" || arg == "--smart-relinquish-var" ||
				arg == "--setup-keys" || arg == "--update-catalog" {
				return fmt.Errorf("readonly 模式禁止 journalctl 参数 %s", arg)
			}
		}
	case "dmesg":
		for _, arg := range lowerArgs {
			if arg == "-c" || arg == "--clear" ||
				arg == "-d" || arg == "-e" ||
				arg == "--console-off" || arg == "--console-on" ||
				arg == "-n" || arg == "--console-level" {
				return fmt.Errorf("readonly 模式禁止 dmesg 修改操作")
			}
		}
	case "ss":
		for _, arg := range lowerArgs {
			if arg == "-k" || arg == "--kill" {
				return fmt.Errorf("readonly 模式禁止 ss kill 操作")
			}
		}
	case "date":
		argumentValueAllowed := false
		for _, arg := range lowerArgs {
			if arg == "-s" || arg == "--set" || strings.HasPrefix(arg, "--set=") {
				return fmt.Errorf("readonly 模式禁止修改系统时间")
			}
			if !strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "+") {
				if !argumentValueAllowed {
					return fmt.Errorf("readonly 模式禁止 date 位置参数 %s", arg)
				}
				argumentValueAllowed = false
				continue
			}
			argumentValueAllowed = arg == "-d" || arg == "--date" ||
				arg == "-f" || arg == "--file" ||
				arg == "-r" || arg == "--reference"
		}
	case "hostname":
		allowed := map[string]bool{
			"-a": true, "--alias": true, "-d": true, "--domain": true,
			"-f": true, "--fqdn": true, "-i": true, "--ip-address": true,
			"-I": true, "--all-ip-addresses": true, "-s": true, "--short": true,
			"-y": true, "--yp": true, "--nis": true, "-v": true, "--verbose": true,
			"-h": true, "--help": true, "-V": true, "--version": true,
		}
		for _, arg := range args {
			if !allowed[arg] {
				return fmt.Errorf("readonly 模式禁止 hostname 参数 %s", arg)
			}
		}
	case "ipconfig":
		for _, arg := range lowerArgs {
			allowed := arg == "/all" || arg == "/displaydns" || arg == "/allcompartments"
			if !allowed {
				return fmt.Errorf("readonly 模式禁止 ipconfig 参数 %s", arg)
			}
		}
	}
	return nil
}

// ─── 工具注册表 ───────────────────────────────────────────────────

type ToolRegistry struct {
	tools     map[string]Tool
	policy    *CommandPolicy
	confirmFn func(string) bool
	mu        sync.RWMutex
	role      string
}

func NewToolRegistry(policy *CommandPolicy) *ToolRegistry {
	return &ToolRegistry{
		tools:     make(map[string]Tool),
		policy:    policy,
		confirmFn: defaultConfirm,
		role:      "default",
	}
}

func (r *ToolRegistry) Register(t Tool) {
	def := t.Definition()
	r.tools[def.Function.Name] = t
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) Definitions() []ToolDef {
	defs := make([]ToolDef, 0, len(r.tools))
	for name, t := range r.tools {
		if !r.ToolAllowed(name) {
			continue
		}
		defs = append(defs, t.Definition())
	}
	return defs
}

func (r *ToolRegistry) Mode() string {
	return r.policy.Mode()
}

func (r *ToolRegistry) SetMode(mode string) error {
	return r.policy.SetMode(mode)
}

func (r *ToolRegistry) ToolAllowed(name string) bool {
	allowed, _ := r.ToolAllowedReason(name)
	return allowed
}

func (r *ToolRegistry) ToolAllowedReason(name string) (bool, string) {
	r.mu.RLock()
	role := r.role
	r.mu.RUnlock()
	if allowed, reason := roleToolAllowed(role, name); !allowed {
		return false, reason
	}
	if r.Mode() == ModeReadonly && name == "write_file" {
		return false, "readonly mode disables write_file"
	}
	if roleDef, ok := builtinRoles[role]; ok && roleDef.ForceReadonly && name == "write_file" {
		return false, "role forces readonly"
	}
	return true, ""
}

func (r *ToolRegistry) AuthorizeCall(name string, args map[string]any) error {
	if allowed, reason := r.ToolAllowedReason(name); !allowed {
		return fmt.Errorf("tool policy denied: %s", reason)
	}
	r.mu.RLock()
	role := r.role
	r.mu.RUnlock()
	if name == "run_command" {
		command, _ := args["command"].(string)
		if _, err := r.policy.AuthorizeCommand(command); err != nil {
			return fmt.Errorf("command policy denied: %w", err)
		}
		if roleDef, ok := builtinRoles[role]; ok && roleDef.ForceReadonly {
			if err := r.policy.validateReadonly(command); err != nil {
				return fmt.Errorf("role %s forced-readonly policy denied command: %w", role, err)
			}
		}
	}
	return nil
}

func (r *ToolRegistry) RequiresApproval(name string, args map[string]any) bool {
	r.mu.RLock()
	role := r.role
	r.mu.RUnlock()
	if role == "writer" && name == "write_file" {
		return true
	}
	if name != "run_command" || r.Mode() != ModeAutopilot {
		return false
	}
	command, _ := args["command"].(string)
	required, err := r.policy.AuthorizeCommand(command)
	return err == nil && required
}

func (r *ToolRegistry) SetRole(role string) error {
	role = strings.ToLower(strings.TrimSpace(role))
	if _, ok := builtinRoles[role]; !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	r.mu.Lock()
	r.role = role
	r.mu.Unlock()
	return nil
}

func (r *ToolRegistry) Role() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.role == "" {
		return "default"
	}
	return r.role
}

// ─── 文件策略 ─────────────────────────────────────────────────────

type FilePolicy struct {
	baseDir          string
	workspaceRoots   []string
	blockedPaths     []string
	readonlyPaths    []string
	allowAbsolute    bool
	maxReadBytes     int64
	memoryMaxPercent float64
}

func NewFilePolicy(cfg FilePolicyConfig) (*FilePolicy, error) {
	baseDir := strings.TrimSpace(cfg.BaseDir)
	if baseDir == "" {
		baseDir = "."
	}
	if !filepath.IsAbs(baseDir) {
		baseDir = filepath.Join(appBaseDir(), baseDir)
	}
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("解析文件基础目录: %w", err)
	}
	baseDir = filepath.Clean(baseDir)
	if resolved, err := filepath.EvalSymlinks(baseDir); err == nil {
		baseDir = resolved
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("解析文件基础目录真实路径: %w", err)
	}

	maxReadBytes := cfg.MaxReadBytes
	if maxReadBytes <= 0 {
		maxReadBytes = 1048576
	}
	memoryPercent := cfg.MemoryMaxPercent
	if memoryPercent <= 0 {
		memoryPercent = 25
	}
	if memoryPercent > 25 {
		memoryPercent = 25
	}

	policy := &FilePolicy{
		baseDir:          baseDir,
		allowAbsolute:    cfg.AllowAbsolute,
		maxReadBytes:     maxReadBytes,
		memoryMaxPercent: memoryPercent,
	}

	roots := cfg.WorkspaceRoots
	if len(roots) == 0 {
		roots = []string{"."}
	}
	if policy.workspaceRoots, err = normalizePolicyPaths(baseDir, roots); err != nil {
		return nil, fmt.Errorf("解析 workspace roots: %w", err)
	}
	if policy.blockedPaths, err = normalizePolicyPaths(baseDir, cfg.BlockedPaths); err != nil {
		return nil, fmt.Errorf("解析 blocked paths: %w", err)
	}
	if policy.readonlyPaths, err = normalizePolicyPaths(baseDir, cfg.ReadonlyPaths); err != nil {
		return nil, fmt.Errorf("解析 readonly paths: %w", err)
	}

	if !pathAllowedByRoots(policy.baseDir, policy.workspaceRoots) {
		return nil, fmt.Errorf("文件基础目录 %s 不在 ELIZA_WORKSPACE_ROOTS 内", policy.baseDir)
	}
	return policy, nil
}

func normalizePolicyPaths(baseDir string, paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		absolute = filepath.Clean(absolute)
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		result = append(result, absolute)
	}
	return result, nil
}

func (p *FilePolicy) ResolveRead(path string) (string, error) {
	return p.resolve(path, false)
}

func (p *FilePolicy) ResolveWrite(path string) (string, error) {
	return p.resolve(path, true)
}

func (p *FilePolicy) resolve(path string, write bool) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("文件路径为空")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("文件路径包含非法空字符")
	}
	if filepath.IsAbs(path) {
		if !p.allowAbsolute {
			return "", fmt.Errorf("文件策略禁止绝对路径: %s", path)
		}
	} else {
		path = filepath.Join(p.baseDir, path)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析文件路径: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if write {
		absolute, err = canonicalizeWritePath(absolute)
	} else {
		absolute, err = filepath.EvalSymlinks(absolute)
	}
	if err != nil {
		return "", fmt.Errorf("解析真实文件路径: %w", err)
	}

	if !pathAllowedByRoots(absolute, p.workspaceRoots) {
		return "", fmt.Errorf("路径超出 workspace roots: %s", absolute)
	}
	for _, blocked := range p.blockedPaths {
		if pathWithinRoot(absolute, blocked) {
			return "", fmt.Errorf("路径被文件策略禁止: %s", absolute)
		}
	}
	if write {
		for _, readonly := range p.readonlyPaths {
			if pathWithinRoot(absolute, readonly) {
				return "", fmt.Errorf("路径为只读区域: %s", absolute)
			}
		}
	}
	return absolute, nil
}

func canonicalizeWritePath(path string) (string, error) {
	current := path
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("找不到可解析的父目录")
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func pathAllowedByRoots(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithinRoot(path, root) {
			return true
		}
	}
	return false
}

func pathWithinRoot(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func (p *FilePolicy) AllowedReadBytes(requested int64) (int64, string, error) {
	if requested <= 0 {
		return 0, "", fmt.Errorf("limit 必须大于 0")
	}

	allowed := requested
	var reasons []string
	if allowed > p.maxReadBytes {
		allowed = p.maxReadBytes
		reasons = append(reasons, fmt.Sprintf("max_read=%d", p.maxReadBytes))
	}

	if total, available, rss, ok := linuxMemorySnapshot(); ok {
		processBudget := int64(float64(total) * p.memoryMaxPercent / 100)
		if rss >= processBudget {
			return 0, "", fmt.Errorf(
				"当前进程内存 %d 已达到系统内存 %.1f%% 上限 %d",
				rss, p.memoryMaxPercent, processBudget,
			)
		}

		// 读取缓冲和返回 string 会短暂保留两份数据，因此再除以 2。
		safeByProcess := (processBudget - rss) / 2
		safeByAvailable := int64(float64(available)*p.memoryMaxPercent/100) / 2
		safe := safeByProcess
		if safeByAvailable < safe {
			safe = safeByAvailable
		}
		if safe < allowed {
			allowed = safe
			reasons = append(reasons, fmt.Sprintf("memory_%.1f%%", p.memoryMaxPercent))
		}
	}

	maxInt := int64(^uint(0) >> 1)
	if allowed > maxInt {
		allowed = maxInt
	}
	if allowed <= 0 {
		return 0, "", fmt.Errorf("系统可用内存不足，拒绝读取文件")
	}
	return allowed, strings.Join(reasons, ","), nil
}

func linuxMemorySnapshot() (totalBytes int64, availableBytes int64, rssBytes int64, ok bool) {
	totalKB, totalOK := readProcKB("/proc/meminfo", "MemTotal:")
	availableKB, availableOK := readProcKB("/proc/meminfo", "MemAvailable:")
	if !availableOK {
		freeKB, freeOK := readProcKB("/proc/meminfo", "MemFree:")
		buffersKB, buffersOK := readProcKB("/proc/meminfo", "Buffers:")
		cachedKB, cachedOK := readProcKB("/proc/meminfo", "Cached:")
		if freeOK && buffersOK && cachedOK {
			availableKB = freeKB + buffersKB + cachedKB
			availableOK = true
		}
	}
	rssKB, rssOK := readProcKB("/proc/self/status", "VmRSS:")
	if !totalOK || !availableOK || !rssOK {
		return 0, 0, 0, false
	}
	return totalKB * 1024, availableKB * 1024, rssKB * 1024, true
}

func readProcKB(path string, key string) (int64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != key {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

// ─── read_file ────────────────────────────────────────────────────

type ReadFileTool struct {
	policy *FilePolicy
}

func (t *ReadFileTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "read_file",
			Description: "按文件策略分段读取文件，不会将整个大文件载入内存。参数: path (必填), offset (可选，字节偏移，默认0), limit (可选，默认10000)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "要读取的文件路径（受 workspace roots 和 blocked paths 限制）",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "起始读取位置（字节偏移量），默认 0",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "最大返回字节数，默认 10000",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ReadFileTool) Execute(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return "", fmt.Errorf("缺少 path 参数")
	}

	offset, err := integerToolArg(args, "offset", 0)
	if err != nil {
		return "", err
	}
	if offset < 0 {
		return "", fmt.Errorf("offset 不能为负数")
	}

	requestedLimit, err := integerToolArg(args, "limit", 10000)
	if err != nil {
		return "", err
	}
	allowedLimit, limitReason, err := t.policy.AllowedReadBytes(requestedLimit)
	if err != nil {
		return "", err
	}

	resolvedPath, err := t.policy.ResolveRead(path)
	if err != nil {
		return "", err
	}

	file, err := os.Open(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("读取文件信息失败: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("path 是目录，不是文件: %s", resolvedPath)
	}
	if info.Mode().IsRegular() && offset >= info.Size() {
		if offset == 0 && info.Size() == 0 {
			return "[END total=0 offset=0 returned=0]", nil
		}
		return fmt.Sprintf("[EOF] offset=%d 超出文件大小 %d 字节", offset, info.Size()), nil
	}
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return "", fmt.Errorf("文件不支持 offset=%d: %w", offset, err)
		}
	}

	buffer := make([]byte, int(allowedLimit))
	n, readErr := io.ReadFull(file, buffer)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("分段读取失败: %w", readErr)
	}
	end := offset + int64(n)
	content := string(buffer[:n])

	if info.Mode().IsRegular() {
		if end < info.Size() {
			content += fmt.Sprintf("\n\n[TRUNCATED total=%d offset=%d returned=%d remaining=%d next_offset=%d]",
				info.Size(), offset, n, info.Size()-end, end)
		} else if offset > 0 || limitReason != "" {
			content += fmt.Sprintf("\n\n[END total=%d offset=%d returned=%d]", info.Size(), offset, n)
		}
	} else if int64(n) == allowedLimit {
		content += fmt.Sprintf("\n\n[TRUNCATED total=unknown offset=%d returned=%d next_offset=%d]",
			offset, n, end)
	}
	if limitReason != "" {
		content += fmt.Sprintf("\n[LIMIT_APPLIED requested=%d allowed=%d reason=%s]",
			requestedLimit, allowedLimit, limitReason)
	}

	return content, nil
}

func integerToolArg(args map[string]any, name string, defaultValue int64) (int64, error) {
	value, ok := args[name]
	if !ok {
		return defaultValue, nil
	}
	switch number := value.(type) {
	case float64:
		if number != float64(int64(number)) {
			return 0, fmt.Errorf("%s 必须是整数", name)
		}
		return int64(number), nil
	case int:
		return int64(number), nil
	case int64:
		return number, nil
	default:
		return 0, fmt.Errorf("%s 必须是整数", name)
	}
}

// ─── write_file ───────────────────────────────────────────────────

type WriteFileTool struct {
	policy *FilePolicy
}

func (t *WriteFileTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "write_file",
			Description: "写入内容到文件，自动创建父目录。参数: path (必填), content (必填)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "要写入的文件路径",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "要写入的文件内容",
					},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}

func (t *WriteFileTool) Execute(args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return "", fmt.Errorf("缺少 path 参数")
	}

	resolvedPath, err := t.policy.ResolveWrite(path)
	if err != nil {
		return "", err
	}

	// 自动创建父目录
	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("读取写入结果失败: %w", err)
	}
	return fmt.Sprintf("成功写入 %s (%d 字节)", resolvedPath, info.Size()), nil
}

// ─── run_command ──────────────────────────────────────────────────

type RunCommandTool struct {
	policy         *CommandPolicy
	confirmFn      func(string) bool
	timeout        time.Duration
	maxOutputBytes int
}

func (t *RunCommandTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "run_command",
			Description: "执行 Shell 命令并返回输出。危险命令需要用户确认。参数: command (必填), workdir (可选)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "要执行的 Shell 命令",
					},
					"workdir": map[string]any{
						"type":        "string",
						"description": "工作目录（可选，默认当前目录）",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func (t *RunCommandTool) Execute(args map[string]any) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *RunCommandTool) ExecuteContext(parent context.Context, args map[string]any) (string, error) {
	command, _ := args["command"].(string)
	workdir, _ := args["workdir"].(string)

	if command == "" {
		return "", fmt.Errorf("缺少 command 参数")
	}

	requiresConfirm, policyErr := t.policy.AuthorizeCommand(command)
	if policyErr != nil {
		return fmt.Sprintf("BLOCKED: 命令被 %s 策略阻止: %v", t.policy.Mode(), policyErr), nil
	}
	approvedByRegistry, _ := args["_eliza_approved"].(bool)
	if requiresConfirm && !approvedByRegistry && !t.confirmFn(command) {
		return "CANCELLED: 用户取消了该危险操作", nil
	}

	timeout := t.timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	maxOutput := t.maxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 65536
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := newShellCommand(ctx, command)
	if workdir != "" {
		cmd.Dir = workdir
	}

	var output cappedBuffer
	output.limit = maxOutput
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Run()
	result := output.String()
	if output.Truncated() {
		result += fmt.Sprintf("\n[TRUNCATED max_output=%d bytes]", maxOutput)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("[timeout=%s]\n%s", timeout, result), nil
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("执行失败: %w", err)
		}
	}

	if exitCode != 0 {
		result = fmt.Sprintf("[exit=%d]\n%s", exitCode, result)
	}

	return result, nil
}

type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	originalLen := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLen, nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return originalLen, nil
	}
	_, _ = b.buf.Write(p)
	return originalLen, nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *cappedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// ─── 默认确认函数 ─────────────────────────────────────────────────

var terminalMu sync.Mutex

func readTerminalLine() (string, error) {
	terminalMu.Lock()
	defer terminalMu.Unlock()
	return readLineInput()
}

func defaultConfirm(command string) bool {
	return defaultApproval(fmt.Sprintf("\n危险命令: %s\n确认执行? [y/N]: ", command))
}

func defaultApproval(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt)
	input, err := readTerminalLine()
	if err != nil && input == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(input), "y")
}
