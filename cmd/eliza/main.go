package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ─── 配置结构 ──────────────────────────────────────────────────────

type Config struct {
	Command           CommandPolicyConfig
	File              FilePolicyConfig
	System            SystemInfo
	Compression       CompressConfig
	LLM               LLMRuntimeConfig
	Memory            MemoryConfig
	Skills            SkillConfig
	UI                UIConfig
	Model             ModelConfig   `json:"model"`
	Agent             AgentConfig   `json:"agent"`
	DangerousPatterns []string      `json:"dangerous_patterns"`
	Worklog           WorklogConfig `json:"worklog"`
	Plugins           PluginsConfig `json:"plugins"`
}

type ModelConfig struct {
	Name          string `json:"name"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	ContextWindow int    `json:"context_window"` // 模型上下文窗口上限 (token)
}

type AgentConfig struct {
	MaxTurns     int    `json:"max_turns"`
	MaxToolCalls int    `json:"max_tool_calls"`
	SystemPrompt string `json:"system_prompt"`
}

type WorklogConfig struct {
	Enabled          bool   `json:"enabled"`
	Dir              string `json:"dir"`
	MaxEventBytes    int    `json:"max_event_bytes"`
	MaxArtifactBytes int64  `json:"max_artifact_bytes"`
}

type LLMRuntimeConfig struct{ RequestTimeoutSeconds, ConnectTimeoutSeconds, MaxRetries, BackoffMaxSeconds int }
type MemoryConfig struct{ MaxFileBytes, MaxTotalBytes int }
type SkillConfig struct {
	Enabled                     bool
	Disabled                    []string
	MaxFileBytes, MaxIndexBytes int
}

type PluginsConfig struct {
	Browser BrowserPluginConfig `json:"browser"`
}

type BrowserPluginConfig struct {
	ChromiumDir    string `json:"chromium_dir"`
	ToolsDir       string `json:"tools_dir"`
	ExecPath       string `json:"exec_path"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxTextBytes   int    `json:"max_text_bytes"`
}

type CommandPolicyConfig struct {
	Mode             string
	TimeoutSeconds   int
	MaxOutputBytes   int
	ReadonlyCommands []string
}

type FilePolicyConfig struct {
	BaseDir          string
	WorkspaceRoots   []string
	BlockedPaths     []string
	ReadonlyPaths    []string
	AllowAbsolute    bool
	MaxReadBytes     int64
	MemoryMaxPercent float64
}

// ─── 版本 ─────────────────────────────────────────────────────────

const version = "0.9.0"

// ─── 入口 ─────────────────────────────────────────────────────────

type CLIOptions struct {
	ConfigPath, Query, Model                       string
	Help, Version, Doctor, Offline, Plain, NoColor bool
}

func main() { os.Exit(runMain(os.Args[1:])) }

func runMain(args []string) int {
	opts, err := parseCLI(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL     %v\nRun ./eliza --help for usage.\n", err)
		return 2
	}
	if opts.Help {
		printCLIHelp(os.Stdout)
		return 0
	}
	if opts.Version {
		fmt.Printf("ELIZA Agent v%s\n", version)
		return 0
	}
	if opts.Doctor {
		return runDoctor(opts)
	}

	envInfo, err := autoLoadEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN     .env 处理失败: %v\n", err)
	}
	cfg, err := loadConfig(opts.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL     加载配置失败: %v\n", err)
		return 2
	}
	applyEnvironment(cfg)
	if opts.Model != "" {
		cfg.Model.Name = opts.Model
	}
	cfg.UI = UIConfig{Plain: opts.Plain, NoColor: opts.NoColor}
	if err := validateCompressConfig(cfg.Compression); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL     Context Compaction 配置错误: %v\n", err)
		return 2
	}
	cfg.System = DetectSystemInfo()
	resolveRuntimePaths(cfg)
	if err := ensureRuntimeLayout(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL     初始化运行目录失败: %v\n", err)
		return 2
	}
	if isPlaceholderAPIKey(cfg.Model.APIKey) {
		fmt.Fprintln(os.Stderr, "FAIL     请编辑二进制同目录 .env 并设置 ELIZA_API_KEY")
		return 2
	}
	if strings.TrimSpace(cfg.Model.BaseURL) == "" {
		fmt.Fprintln(os.Stderr, "FAIL     请设置 ELIZA_BASE_URL")
		return 2
	}
	commandPolicy, err := NewCommandPolicy(cfg.Command.Mode, cfg.DangerousPatterns, cfg.Command.ReadonlyCommands)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL     命令策略配置错误: %v\n", err)
		return 2
	}
	filePolicy, err := NewFilePolicy(cfg.File)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL     文件策略配置错误: %v\n", err)
		return 2
	}
	instanceLock, err := AcquireInstanceLock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL     %v\n", err)
		return 2
	}
	defer instanceLock.Release()
	llmCfg := LLMConfig{RequestTimeout: time.Duration(cfg.LLM.RequestTimeoutSeconds) * time.Second, ConnectTimeout: time.Duration(cfg.LLM.ConnectTimeoutSeconds) * time.Second, MaxRetries: cfg.LLM.MaxRetries, BackoffMax: time.Duration(cfg.LLM.BackoffMaxSeconds) * time.Second}
	llm := NewLLMClientWithConfig(cfg.Model.BaseURL, cfg.Model.APIKey, cfg.Model.Name, llmCfg)
	registry := NewToolRegistry(commandPolicy)
	agent := NewAgent(cfg, llm, registry)
	registry.confirmFn = func(command string) ApprovalResult {
		return agent.approvalLoop(fmt.Sprintf("Dangerous command: %s", command))
	}
	registry.RegisterMany(
		&ReadFileTool{policy: filePolicy},
		&WriteFileTool{policy: filePolicy},
		&EditFileTool{policy: filePolicy},
		&GlobTool{policy: filePolicy},
		&RunCommandTool{policy: commandPolicy, confirmFn: registry.confirmFn, timeout: time.Duration(cfg.Command.TimeoutSeconds) * time.Second, maxOutputBytes: cfg.Command.MaxOutputBytes},
		&SkillListTool{},
		&SkillViewTool{},
		&MemoryTool{approvalFn: agent.approvalLoop, allowWrite: opts.Query == "", worklog: agent.worklog},
		NewViewImageTool(
			getEnvWithDefault("ELIZA_VISION_BASE_URL", ""),
			getEnvWithDefault("ELIZA_VISION_API_KEY", ""),
			getEnvWithDefault("ELIZA_VISION_MODEL", ""),
			filePolicy,
		),
	)
	registerBrowserTools(registry, cfg.Plugins.Browser, filePolicy)
	configureSkills(cfg.Skills, agent.worklog)
	scanSkills()
	if envInfo.Generated {
		agent.ui.Status("WARN", "已生成默认 .env，请编辑后重新运行")
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if sig == syscall.SIGINT && agent.CancelCurrent() {
				agent.ui.Status("WARN", "当前请求已取消")
				continue
			}
			agent.saveWorklog()
			instanceLock.Release()
			os.Exit(0)
		}
	}()
	if opts.Query != "" {
		if err := agent.RunQuery(opts.Query); err != nil {
			agent.ui.Status("FAIL", "%v", err)
			agent.saveWorklog()
			return 1
		}
		agent.saveWorklog()
		return 0
	}
	if err := agent.RunInteractive(); err != nil {
		agent.ui.Status("FAIL", "%v", err)
		agent.saveWorklog()
		return 1
	}
	agent.saveWorklog()
	return 0
}

func parseCLI(args []string) (CLIOptions, error) {
	var opts CLIOptions
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "doctor" {
			opts.Doctor = true
			continue
		}
		filtered = append(filtered, arg)
	}
	fs := flag.NewFlagSet("eliza", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.ConfigPath, "c", "", "config")
	fs.StringVar(&opts.ConfigPath, "config", "", "config")
	fs.StringVar(&opts.Query, "q", "", "query")
	fs.StringVar(&opts.Model, "m", "", "model")
	fs.StringVar(&opts.Model, "model", "", "model")
	fs.BoolVar(&opts.Help, "h", false, "help")
	fs.BoolVar(&opts.Help, "help", false, "help")
	fs.BoolVar(&opts.Version, "v", false, "version")
	fs.BoolVar(&opts.Version, "version", false, "version")
	fs.BoolVar(&opts.Doctor, "doctor", opts.Doctor, "doctor")
	fs.BoolVar(&opts.Offline, "offline", false, "offline")
	fs.BoolVar(&opts.Plain, "plain", false, "plain")
	fs.BoolVar(&opts.NoColor, "no-color", false, "no color")
	if err := fs.Parse(filtered); err != nil {
		message := err.Error()
		if strings.Contains(message, "flag provided but not defined") {
			parts := strings.Split(message, ":")
			bad := strings.TrimSpace(parts[len(parts)-1])
			for _, original := range filtered {
				if strings.HasPrefix(original, "-") && strings.Contains(strings.TrimLeft(original, "-"), strings.TrimLeft(bad, "-")) {
					bad = original
					break
				}
			}
			known := []string{"-h", "--help", "-v", "--version", "-q", "-c", "--config", "-m", "--model", "--doctor", "--offline", "--plain", "--no-color"}
			best := ""
			distance := 999
			for _, candidate := range known {
				if value := editDistance(bad, candidate); value < distance {
					distance = value
					best = candidate
				}
			}
			if best != "" {
				return opts, fmt.Errorf("未知参数 %q；你是否想使用 %s", bad, best)
			}
		}
		return opts, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		return opts, fmt.Errorf("未知参数 %q", rest[0])
	}
	return opts, nil
}

func printCLIHelp(out io.Writer) {
	fmt.Fprint(out, `ELIZA-AGENT

系统终端命令
  ./eliza                         启动交互 TUI
  ./eliza -q "问题"              执行一次 streaming 查询后退出；memory 修改禁用
  ./eliza -c <path>               指定配置文件（同 --config）
  ./eliza -m <model>              临时覆盖模型（同 --model）
  ./eliza -v                      输出版本（同 --version）
  ./eliza doctor [--offline]      只读自检；--offline 跳过网络
  ./eliza -h|-help|--help         输出本帮助且不创建文件
  --plain | --no-color            紧凑纯文本 | 禁用颜色

TUI 交互命令
  /help  /status  /clear  /new
  /mode [readonly|autopilot]  /role [name]
  /plan <任务>  /showplan  /execute  /retryplan  /skipstep  /cancelplan
  /compress  /tools  /skills [reload|enable <name>|disable <name>]  /memory
  exit | quit | /q | /exit

配置优先级
  CLI model override > 已导出的环境变量 > 二进制同目录 .env > config.json > 默认值。
  正常启动会在二进制同目录创建缺失的 .env、skills/、memory/、worklogs/、plans/；
  --help、--version 和 doctor 不创建或修改这些文件。
`)
}

func applyEnvironment(cfg *Config) {
	if v := os.Getenv("ELIZA_BASE_URL"); v != "" {
		cfg.Model.BaseURL = v
	}
	if v := os.Getenv("ELIZA_API_KEY"); v != "" {
		cfg.Model.APIKey = v
	}
	if v := os.Getenv("ELIZA_MODEL"); v != "" {
		cfg.Model.Name = v
	}
	if v := os.Getenv("ELIZA_MODE"); v != "" {
		cfg.Command.Mode = strings.ToLower(strings.TrimSpace(v))
	}
	setEnvInt("ELIZA_COMMAND_TIMEOUT", &cfg.Command.TimeoutSeconds)
	setEnvInt("ELIZA_COMMAND_MAX_OUTPUT", &cfg.Command.MaxOutputBytes)
	if v := os.Getenv("ELIZA_READONLY_COMMANDS"); v != "" {
		cfg.Command.ReadonlyCommands = splitEnvList(v, ",")
	}
	if v := os.Getenv("ELIZA_DANGEROUS_PATTERNS"); v != "" {
		cfg.DangerousPatterns = splitEnvList(v, ";")
	}
	setEnvInt("ELIZA_AGENT_MAX_STEPS", &cfg.Agent.MaxTurns)
	setEnvInt("ELIZA_AGENT_MAX_TOOL_CALLS", &cfg.Agent.MaxToolCalls)
	setEnvInt("ELIZA_LLM_REQUEST_TIMEOUT", &cfg.LLM.RequestTimeoutSeconds)
	setEnvInt("ELIZA_LLM_CONNECT_TIMEOUT", &cfg.LLM.ConnectTimeoutSeconds)
	setEnvNonnegativeInt("ELIZA_LLM_MAX_RETRIES", &cfg.LLM.MaxRetries)
	setEnvInt("ELIZA_LLM_BACKOFF_MAX", &cfg.LLM.BackoffMaxSeconds)
	if v := os.Getenv("ELIZA_COMPACT_ENABLED"); v != "" {
		if value, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			cfg.Compression.Enabled = value
		} else {
			warnEnv("ELIZA_COMPACT_ENABLED", v)
		}
	}
	if v := os.Getenv("ELIZA_COMPACT_TRIGGER_PERCENT"); v != "" {
		cfg.Compression.TriggerPct = parsePercentEnv(v, cfg.Compression.TriggerPct, "ELIZA_COMPACT_TRIGGER_PERCENT")
	}
	if v := os.Getenv("ELIZA_COMPACT_TARGET_PERCENT"); v != "" {
		cfg.Compression.TargetPct = parsePercentEnv(v, cfg.Compression.TargetPct, "ELIZA_COMPACT_TARGET_PERCENT")
	}
	if v := os.Getenv("ELIZA_COMPACT_EMERGENCY_PERCENT"); v != "" {
		cfg.Compression.EmergencyPct = parsePercentEnv(v, cfg.Compression.EmergencyPct, "ELIZA_COMPACT_EMERGENCY_PERCENT")
	}
	setEnvInt("ELIZA_COMPACT_MAX_COUNT", &cfg.Compression.MaxCount)
	setEnvInt("ELIZA_COMPACT_KEEP_RECENT_GROUPS", &cfg.Compression.KeepRecentGroups)
	if v := os.Getenv("ELIZA_FILE_BASE_DIR"); v != "" {
		cfg.File.BaseDir = strings.TrimSpace(v)
	}
	if v := os.Getenv("ELIZA_WORKSPACE_ROOTS"); v != "" {
		cfg.File.WorkspaceRoots = splitEnvList(v, ";")
	}
	if v := os.Getenv("ELIZA_FILE_BLOCKED_PATHS"); v != "" {
		cfg.File.BlockedPaths = splitEnvList(v, ";")
	}
	if v := os.Getenv("ELIZA_FILE_READONLY_PATHS"); v != "" {
		cfg.File.ReadonlyPaths = splitEnvList(v, ";")
	}
	if v := os.Getenv("ELIZA_FILE_ALLOW_ABSOLUTE"); v != "" {
		if value, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			cfg.File.AllowAbsolute = value
		} else {
			warnEnv("ELIZA_FILE_ALLOW_ABSOLUTE", v)
		}
	}
	if v := os.Getenv("ELIZA_FILE_MAX_READ_BYTES"); v != "" {
		if value, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && value > 0 {
			cfg.File.MaxReadBytes = value
		} else {
			warnEnv("ELIZA_FILE_MAX_READ_BYTES", v)
		}
	}
	if v := os.Getenv("ELIZA_FILE_MEMORY_PERCENT"); v != "" {
		if value, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && value > 0 {
			if value > 25 {
				value = 25
			}
			cfg.File.MemoryMaxPercent = value
		} else {
			warnEnv("ELIZA_FILE_MEMORY_PERCENT", v)
		}
	}
	setEnvInt("ELIZA_MEMORY_MAX_FILE_BYTES", &cfg.Memory.MaxFileBytes)
	setEnvInt("ELIZA_MEMORY_MAX_TOTAL_BYTES", &cfg.Memory.MaxTotalBytes)
	if v := os.Getenv("ELIZA_SKILLS_ENABLED"); v != "" {
		if value, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			cfg.Skills.Enabled = value
		} else {
			warnEnv("ELIZA_SKILLS_ENABLED", v)
		}
	}
	if v := os.Getenv("ELIZA_SKILLS_DISABLED"); v != "" {
		cfg.Skills.Disabled = splitEnvList(v, ",")
	}
	setEnvInt("ELIZA_SKILL_MAX_FILE_BYTES", &cfg.Skills.MaxFileBytes)
	setEnvInt("ELIZA_SKILL_MAX_INDEX_BYTES", &cfg.Skills.MaxIndexBytes)
	if v := os.Getenv("ELIZA_BROWSER_CHROMIUM_DIR"); v != "" {
		cfg.Plugins.Browser.ChromiumDir = strings.TrimSpace(v)
	}
	if v := os.Getenv("ELIZA_BROWSER_TOOLS_DIR"); v != "" {
		cfg.Plugins.Browser.ToolsDir = strings.TrimSpace(v)
	}
	if v := os.Getenv("ELIZA_BROWSER_EXEC_PATH"); v != "" {
		cfg.Plugins.Browser.ExecPath = strings.TrimSpace(v)
	}
	setEnvInt("ELIZA_BROWSER_TIMEOUT", &cfg.Plugins.Browser.TimeoutSeconds)
	setEnvInt("ELIZA_BROWSER_MAX_TEXT_BYTES", &cfg.Plugins.Browser.MaxTextBytes)
}
func getEnvWithDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}
func setEnvInt(name string, target *int) {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			*target = n
		} else {
			warnEnv(name, v)
		}
	}
}
func setEnvNonnegativeInt(name string, target *int) {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			*target = n
		} else {
			warnEnv(name, v)
		}
	}
}
func warnEnv(name, value string) {
	fmt.Fprintf(os.Stderr, "WARN     忽略非法 %s=%q\n", name, value)
}

// ─── 配置加载 ─────────────────────────────────────────────────────

func loadConfig(specifiedPath string) (*Config, error) {
	cfg := &Config{
		Model: ModelConfig{ContextWindow: 131072},
		Agent: AgentConfig{
			MaxTurns:     50,
			MaxToolCalls: 100,
		},
		Worklog: WorklogConfig{
			Enabled: true, Dir: "./worklogs", MaxEventBytes: 16384, MaxArtifactBytes: 8388608,
		},
		LLM:    LLMRuntimeConfig{RequestTimeoutSeconds: 120, ConnectTimeoutSeconds: 10, MaxRetries: 2, BackoffMaxSeconds: 4},
		Memory: MemoryConfig{MaxFileBytes: defaultMemoryFileLimit, MaxTotalBytes: defaultMemoryTotalLimit},
		Skills: SkillConfig{Enabled: true, MaxFileBytes: 128 * 1024, MaxIndexBytes: 64 * 1024},
		Command: CommandPolicyConfig{
			Mode:             ModeReadonly,
			TimeoutSeconds:   60,
			MaxOutputBytes:   65536,
			ReadonlyCommands: DefaultReadonlyCommands(),
		},
		File: FilePolicyConfig{
			BaseDir:          ".",
			WorkspaceRoots:   []string{"."},
			BlockedPaths:     []string{".env", "/proc/kcore", "/dev/mem", "/dev/kmem"},
			ReadonlyPaths:    []string{"/proc", "/sys", "/dev"},
			AllowAbsolute:    true,
			MaxReadBytes:     1048576,
			MemoryMaxPercent: 25,
		},
		Plugins:     PluginsConfig{Browser: defaultBrowserPluginConfig()},
		Compression: DefaultCompressConfig(),
		DangerousPatterns: []string{
			`rm\s+-rf\s+/`,
			`rm\s+-rf\s+\*`,
			`\brm\b`,
			`dd\s+if=`,
			`mkfs`,
			`>\s*/dev/sd`,
			`>\s*\S`,
			`chmod\s+777`,
			`sudo\s+rm`,
		},
	}

	path := specifiedPath
	if path == "" {
		// 尝试默认路径
		candidates := []string{
			filepath.Join(appBaseDir(), "config.json"),
			"./config.json",
			"./config.yaml",
			filepath.Join(os.Getenv("HOME"), ".eliza", "config.json"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", path, err)
		}

		// 兼容 JSON 和 YAML
		if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
			cfg, err = parseYAMLConfig(data)
		} else {
			err = json.Unmarshal(data, cfg)
		}
		if err != nil {
			return nil, fmt.Errorf("解析 %s: %w", path, err)
		}
	}

	return cfg, nil
}

// ─── 简易 YAML 解析（仅一级键 + 字符串值，覆盖默认值）─────────────

func parseYAMLConfig(data []byte) (*Config, error) {
	cfg := &Config{
		Model:       ModelConfig{ContextWindow: 131072},
		Agent:       AgentConfig{MaxTurns: 50, MaxToolCalls: 100},
		Worklog:     WorklogConfig{Enabled: true, Dir: "./worklogs", MaxEventBytes: 16384, MaxArtifactBytes: 8388608},
		LLM:         LLMRuntimeConfig{RequestTimeoutSeconds: 120, ConnectTimeoutSeconds: 10, MaxRetries: 2, BackoffMaxSeconds: 4},
		Memory:      MemoryConfig{MaxFileBytes: defaultMemoryFileLimit, MaxTotalBytes: defaultMemoryTotalLimit},
		Skills:      SkillConfig{Enabled: true, MaxFileBytes: 128 * 1024, MaxIndexBytes: 64 * 1024},
		Command:     CommandPolicyConfig{Mode: ModeReadonly, TimeoutSeconds: 60, MaxOutputBytes: 65536, ReadonlyCommands: DefaultReadonlyCommands()},
		File:        FilePolicyConfig{BaseDir: ".", WorkspaceRoots: []string{"."}, AllowAbsolute: true, MaxReadBytes: 1048576, MemoryMaxPercent: 25},
		Plugins:     PluginsConfig{Browser: defaultBrowserPluginConfig()},
		Compression: DefaultCompressConfig(),
	}

	lines := strings.Split(string(data), "\n")
	var currentSection string

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)

		// 跳过注释和空行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 一级键
		if !strings.HasPrefix(rawLine, " ") && !strings.HasPrefix(rawLine, "\t") && strings.HasSuffix(line, ":") {
			currentSection = strings.TrimSuffix(line, ":")
			continue
		}

		// 二级键值对
		if strings.HasPrefix(rawLine, "  ") || strings.HasPrefix(rawLine, "\t") {
			parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, "\"'")

			switch currentSection {
			case "model":
				switch key {
				case "name":
					cfg.Model.Name = val
				case "base_url":
					cfg.Model.BaseURL = val
				case "api_key":
					cfg.Model.APIKey = val
				}
			case "agent":
				switch key {
				case "max_turns":
					fmt.Sscanf(val, "%d", &cfg.Agent.MaxTurns)
				case "system_prompt":
					cfg.Agent.SystemPrompt = strings.Trim(val, "|")
				}
			case "dangerous_patterns":
			case "worklog":
				switch key {
				case "enabled":
					cfg.Worklog.Enabled = strings.ToLower(val) == "true"
				case "dir":
					cfg.Worklog.Dir = val
				}
			case "plugins":
				switch key {
				case "chromium_dir":
					cfg.Plugins.Browser.ChromiumDir = val
				case "tools_dir":
					cfg.Plugins.Browser.ToolsDir = val
				case "exec_path":
					cfg.Plugins.Browser.ExecPath = val
				case "timeout_seconds":
					fmt.Sscanf(val, "%d", &cfg.Plugins.Browser.TimeoutSeconds)
				case "max_text_bytes":
					fmt.Sscanf(val, "%d", &cfg.Plugins.Browser.MaxTextBytes)
				}
			}
		}
	}

	return cfg, nil
}

// ─── 运行时目录与 .env ────────────────────────────────────────────

type EnvLoadInfo struct {
	Path      string
	Generated bool
}

var appBaseDirOverride string

func appBaseDir() string {
	if appBaseDirOverride != "" {
		return appBaseDirOverride
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// autoLoadEnv 只读取二进制同目录 .env；不存在则生成默认模板。
// 文件格式沿用当前 env.txt 风格: 注释 + KEY=VALUE。
// 已显式 export 的环境变量优先级更高，不会被 .env 覆盖。
func autoLoadEnv() (EnvLoadInfo, error) {
	envFile := filepath.Join(appBaseDir(), ".env")
	info := EnvLoadInfo{Path: envFile}

	if _, err := os.Stat(envFile); err != nil {
		if !os.IsNotExist(err) {
			return info, fmt.Errorf("检查 .env: %w", err)
		}
		if err := os.WriteFile(envFile, []byte(defaultEnvContent()), 0600); err != nil {
			return info, fmt.Errorf("生成默认 .env: %w", err)
		}
		info.Generated = true
	}

	if err := loadEnvFile(envFile); err != nil {
		return info, err
	}

	return info, nil
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开 .env: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 解析 KEY=VALUE（允许 VALUE 中含 =）
		idx := strings.Index(line, "=")
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// 去除引号
		val = strings.Trim(val, "\"'")

		// 仅当环境变量未设置时才加载（显式 export 优先级更高）
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取 .env: %w", err)
	}
	return nil
}

func defaultEnvContent() string {
	readonlyCommands := strings.Join(DefaultReadonlyCommands(), ",")
	return "# =============================================================================\n" +
		"# ELIZA Agent 环境变量配置\n" +
		"# 格式: KEY=VALUE；复制单个 eliza 二进制到新机器后，首次运行会自动生成本文件。\n" +
		"# 编辑下面三项即可接入内网 OpenAI-compatible API。\n" +
		"# =============================================================================\n\n" +
		"ELIZA_PROFILE=internal\n" +
		"ELIZA_BASE_URL=http://your-internal-openai-compatible-api/v1\n" +
		"ELIZA_API_KEY=your-api-key-here\n" +
		"ELIZA_MODEL=DeepSeek-V4-Flash\n\n" +
		"# 命令策略: readonly / autopilot\n" +
		"ELIZA_MODE=readonly\n" +
		"ELIZA_COMMAND_TIMEOUT=60\n" +
		"ELIZA_COMMAND_MAX_OUTPUT=65536\n" +
		"ELIZA_READONLY_COMMANDS=" + readonlyCommands + "\n" +
		"ELIZA_DANGEROUS_PATTERNS=rm\\s+-rf\\s+/;rm\\s+-rf\\s+\\*;\\brm\\b;dd\\s+if=;mkfs;>\\s*/dev/sd;>\\s*\\S;chmod\\s+777;sudo\\s+rm\n\n" +
		"# Agent 单任务预算（不限制会话请求总数）\n" +
		"ELIZA_AGENT_MAX_STEPS=50\n" +
		"ELIZA_AGENT_MAX_TOOL_CALLS=100\n\n" +
		"# LLM streaming（所有正文请求强制 stream=true）\n" +
		"ELIZA_LLM_REQUEST_TIMEOUT=120\n" +
		"ELIZA_LLM_CONNECT_TIMEOUT=10\n" +
		"ELIZA_LLM_MAX_RETRIES=2\n" +
		"ELIZA_LLM_BACKOFF_MAX=4\n\n" +
		"# Context Compaction（有限次数）\n" +
		"ELIZA_COMPACT_ENABLED=true\n" +
		"ELIZA_COMPACT_TRIGGER_PERCENT=60\n" +
		"ELIZA_COMPACT_TARGET_PERCENT=45\n" +
		"ELIZA_COMPACT_EMERGENCY_PERCENT=90\n" +
		"ELIZA_COMPACT_MAX_COUNT=3\n" +
		"ELIZA_COMPACT_KEEP_RECENT_GROUPS=8\n\n" +
		"# 视觉理解（可选，不填则 view_image 工具返回配置提示）\n" +
		"ELIZA_VISION_BASE_URL=\n" +
		"ELIZA_VISION_API_KEY=\n" +
		"ELIZA_VISION_MODEL=\n\n" +
		"# 文件策略\n" +
		"ELIZA_FILE_BASE_DIR=.\n" +
		"ELIZA_WORKSPACE_ROOTS=.\n" +
		"ELIZA_FILE_BLOCKED_PATHS=.env;/proc/kcore;/dev/mem;/dev/kmem\n" +
		"ELIZA_FILE_READONLY_PATHS=/proc;/sys;/dev\n" +
		"ELIZA_FILE_ALLOW_ABSOLUTE=true\n" +
		"ELIZA_FILE_MAX_READ_BYTES=1048576\n" +
		"ELIZA_FILE_MEMORY_PERCENT=25\n\n" +
		"# Memory 与 Skills 加载上限\n" +
		"ELIZA_MEMORY_MAX_FILE_BYTES=32768\n" +
		"ELIZA_MEMORY_MAX_TOTAL_BYTES=65536\n" +
		"ELIZA_SKILLS_ENABLED=true\n" +
		"ELIZA_SKILLS_DISABLED=\n" +
		"ELIZA_SKILL_MAX_FILE_BYTES=131072\n" +
		"ELIZA_SKILL_MAX_INDEX_BYTES=65536\n\n" +
		"# 无头浏览器（可选）：放置到二进制同目录下的 tools/；也兼容 plugins/chromium\n" +
		"ELIZA_BROWSER_TOOLS_DIR=./tools\n" +
		"ELIZA_BROWSER_CHROMIUM_DIR=./plugins/chromium\n" +
		"ELIZA_BROWSER_EXEC_PATH=\n" +
		"ELIZA_BROWSER_TIMEOUT=30\n" +
		"ELIZA_BROWSER_MAX_TEXT_BYTES=24000\n"
}

func splitEnvList(value string, separator string) []string {
	parts := strings.Split(value, separator)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func parsePercentEnv(value string, fallback float64, name string) float64 {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || number <= 0 || number > 100 {
		fmt.Fprintf(os.Stderr, "WARN     忽略非法 %s=%q\n", name, value)
		return fallback
	}
	return number
}

func resolveRuntimePaths(cfg *Config) {
	base := appBaseDir()
	if cfg.Worklog.Dir == "" {
		cfg.Worklog.Dir = "worklogs"
	}
	if !filepath.IsAbs(cfg.Worklog.Dir) {
		cfg.Worklog.Dir = filepath.Join(base, cfg.Worklog.Dir)
	}
	cfg.Plugins.Browser.ChromiumDir = expandUserPath(cfg.Plugins.Browser.ChromiumDir)
	if cfg.Plugins.Browser.ChromiumDir != "" && !filepath.IsAbs(cfg.Plugins.Browser.ChromiumDir) {
		cfg.Plugins.Browser.ChromiumDir = filepath.Join(base, cfg.Plugins.Browser.ChromiumDir)
	}
	cfg.Plugins.Browser.ToolsDir = expandUserPath(cfg.Plugins.Browser.ToolsDir)
	cfg.Plugins.Browser.ExecPath = expandUserPath(cfg.Plugins.Browser.ExecPath)
	if cfg.Plugins.Browser.ToolsDir == "" {
		cfg.Plugins.Browser.ToolsDir = defaultBrowserToolsDir()
	}
	if cfg.Plugins.Browser.ToolsDir != "" && !filepath.IsAbs(cfg.Plugins.Browser.ToolsDir) {
		cfg.Plugins.Browser.ToolsDir = filepath.Join(base, cfg.Plugins.Browser.ToolsDir)
	}
}

func ensureRuntimeLayout(cfg *Config) error {
	base := appBaseDir()
	dirs := []string{
		filepath.Join(base, "skills"),
		filepath.Join(base, "plans"),
	}
	if cfg.Worklog.Enabled {
		dirs = append(dirs, cfg.Worklog.Dir)
	}
	if cfg.Plugins.Browser.ToolsDir != "" {
		dirs = append(dirs, cfg.Plugins.Browser.ToolsDir)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建目录 %s: %w", dir, err)
		}
	}
	return ensureMemoryLayout()
}

func printStartupProfile(envInfo EnvLoadInfo, cfg *Config) {
	profile := os.Getenv("ELIZA_PROFILE")
	if profile == "" {
		profile = "default"
	}
	state := "loaded"
	if envInfo.Generated {
		state = "generated"
	}
	fmt.Fprintf(os.Stderr, "[PROFILE] name=%s env=%s (%s)\n", profile, envInfo.Path, state)
	fmt.Fprintf(os.Stderr, "[PROFILE] model=%s base_url=%s api_key=%s\n",
		cfg.Model.Name,
		displayEndpoint(cfg.Model.BaseURL),
		maskSecret(cfg.Model.APIKey),
	)
	fmt.Fprintf(os.Stderr, "[PROFILE] mode=%s command_timeout=%ds max_output=%d\n",
		cfg.Command.Mode,
		cfg.Command.TimeoutSeconds,
		cfg.Command.MaxOutputBytes,
	)
	fmt.Fprintf(os.Stderr, "[PROFILE] request_max_steps=%d request_max_tool_calls=%d\n",
		cfg.Agent.MaxTurns,
		cfg.Agent.MaxToolCalls,
	)
	fmt.Fprintf(os.Stderr, "[PROFILE] compact=%.0f%%→%.0f%% max=%d emergency=%.0f%% keep_groups=%d\n",
		cfg.Compression.TriggerPct,
		cfg.Compression.TargetPct,
		cfg.Compression.MaxCount,
		cfg.Compression.EmergencyPct,
		cfg.Compression.KeepRecentGroups,
	)
	fmt.Fprintf(os.Stderr, "[PROFILE] file_roots=%s max_read=%d memory_limit=%.1f%%\n",
		strings.Join(cfg.File.WorkspaceRoots, ";"),
		cfg.File.MaxReadBytes,
		cfg.File.MemoryMaxPercent,
	)
	fmt.Fprintf(os.Stderr, "[SYSTEM] os=%s arch=%s distro=%s kernel=%s\n",
		cfg.System.OS,
		cfg.System.Architecture,
		valueOrUnknown(cfg.System.Distribution),
		valueOrUnknown(cfg.System.Kernel),
	)
	if envInfo.Generated {
		fmt.Fprintf(os.Stderr, "[PROFILE] 已生成默认 .env，请编辑后重新运行。\n")
	}
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	if len(value) <= 6 {
		return value[:1] + "***"
	}
	if len(value) <= 12 {
		return value[:3] + "***" + value[len(value)-2:]
	}
	return value[:6] + "***" + value[len(value)-4:]
}

func isPlaceholderAPIKey(value string) bool {
	v := strings.TrimSpace(strings.ToLower(value))
	return v == "" ||
		v == "your-api-key-here" ||
		v == "eliza_api_key" ||
		v == "change-me" ||
		v == "changeme" ||
		v == "none" ||
		v == "null"
}

func findChromium(baseDir string) string {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return ""
	}
	// 按架构匹配
	candidates := []string{
		filepath.Join(baseDir, "chrome-headless-shell-linux64", "chrome-headless-shell"),
		filepath.Join(baseDir, "chrome-headless-shell-linux-arm64", "chrome-headless-shell"),
		filepath.Join(baseDir, "chrome-linux64", "chrome"),
		filepath.Join(baseDir, "chrome-linux-arm64", "chrome"),
		filepath.Join(baseDir, "chrome-headless-shell", "chrome-headless-shell"),
		filepath.Join(baseDir, "chrome-headless-shell"),
		filepath.Join(baseDir, "chromium"),
		filepath.Join(baseDir, "chrome"),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}
