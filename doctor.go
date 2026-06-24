package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

type doctorReporter struct{ severity int }

func (r *doctorReporter) line(level, name, detail string) {
	fmt.Printf("%-5s %-24s %s\n", level, name, detail)
	if level == "FAIL" {
		r.severity = 2
	} else if level == "WARN" && r.severity < 1 {
		r.severity = 1
	}
}
func (r *doctorReporter) pass(name, detail string) { r.line("PASS", name, detail) }
func (r *doctorReporter) warn(name, detail string) { r.line("WARN", name, detail) }
func (r *doctorReporter) fail(name, detail string) { r.line("FAIL", name, detail) }

func runDoctor(opts CLIOptions) int {
	r := &doctorReporter{}
	if info, ok := debug.ReadBuildInfo(); ok {
		r.pass("binary", fmt.Sprintf("version=%s go=%s", version, info.GoVersion))
	} else {
		r.warn("binary", fmt.Sprintf("version=%s build info unavailable", version))
	}
	system := DetectSystemInfo()
	r.pass("system", fmt.Sprintf("os=%s arch=%s distro=%s kernel=%s cwd=%s binary_dir=%s", system.OS, system.Architecture, valueOrUnknown(system.Distribution), valueOrUnknown(system.Kernel), mustGetwd(), appBaseDir()))
	envPath := filepath.Join(appBaseDir(), ".env")
	if info, err := os.Stat(envPath); err != nil {
		r.fail(".env", fmt.Sprintf("missing or unreadable: %v", err))
	} else if info.IsDir() {
		r.fail(".env", "path is a directory")
	} else if err := loadEnvFile(envPath); err != nil {
		r.fail(".env", err.Error())
	} else {
		keys, readErr := readEnvKeys(envPath)
		if readErr != nil {
			r.fail(".env", readErr.Error())
		} else {
			missing := []string{}
			for _, key := range []string{"ELIZA_BASE_URL", "ELIZA_API_KEY", "ELIZA_MODEL"} {
				if !keys[key] {
					missing = append(missing, key)
				}
			}
			if len(missing) > 0 {
				r.fail(".env", fmt.Sprintf("missing keys: %s", strings.Join(missing, ",")))
			} else {
				r.pass(".env", envPath)
			}
		}
	}
	cfg, err := loadConfig(opts.ConfigPath)
	if err != nil {
		r.fail("config", err.Error())
		return r.severity
	}
	applyEnvironment(cfg)
	if opts.Model != "" {
		cfg.Model.Name = opts.Model
	}
	cfg.System = system
	resolveRuntimePaths(cfg)
	if cfg.Model.Name == "" {
		r.fail("model", "missing")
	} else {
		r.pass("model", cfg.Model.Name)
	}
	if isPlaceholderAPIKey(cfg.Model.APIKey) {
		r.fail("api_key", "missing or placeholder")
	} else {
		r.pass("api_key", maskSecret(cfg.Model.APIKey))
	}
	parsed, parseErr := url.Parse(cfg.Model.BaseURL)
	if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		r.fail("base_url", fmt.Sprintf("invalid: %q", cfg.Model.BaseURL))
	} else {
		r.pass("base_url", parsed.Scheme+"://"+parsed.Host+parsed.EscapedPath())
	}
	r.pass("streaming", "forced=true; no non-streaming fallback")
	if cfg.LLM.RequestTimeoutSeconds <= 0 || cfg.LLM.ConnectTimeoutSeconds <= 0 || cfg.LLM.MaxRetries < 0 || cfg.LLM.BackoffMaxSeconds <= 0 {
		r.fail("LLM policy", "invalid timeout/retry values")
	} else {
		r.pass("LLM policy", fmt.Sprintf("request=%ds connect=%ds retries=%d backoff_max=%ds", cfg.LLM.RequestTimeoutSeconds, cfg.LLM.ConnectTimeoutSeconds, cfg.LLM.MaxRetries, cfg.LLM.BackoffMaxSeconds))
	}
	if len(cfg.Command.ReadonlyCommands) == 0 {
		r.fail("command policy", "readonly whitelist is empty")
	} else if _, err := NewCommandPolicy(cfg.Command.Mode, cfg.DangerousPatterns, cfg.Command.ReadonlyCommands); err != nil {
		r.fail("command policy", err.Error())
	} else {
		r.pass("command policy", fmt.Sprintf("mode=%s readonly_commands=%d patterns=%d", cfg.Command.Mode, len(cfg.Command.ReadonlyCommands), len(cfg.DangerousPatterns)))
	}
	if cfg.File.MemoryMaxPercent <= 0 || cfg.File.MemoryMaxPercent > 25 {
		r.fail("file policy", fmt.Sprintf("memory percent %.1f must be in (0,25]", cfg.File.MemoryMaxPercent))
	} else if _, err := NewFilePolicy(cfg.File); err != nil {
		r.fail("file policy", err.Error())
	} else {
		r.pass("file policy", fmt.Sprintf("roots=%s max_read=%d memory=%.1f%%", strings.Join(cfg.File.WorkspaceRoots, ";"), cfg.File.MaxReadBytes, cfg.File.MemoryMaxPercent))
	}
	if err := validateCompressConfig(cfg.Compression); err != nil {
		r.fail("compaction", err.Error())
	} else {
		r.pass("compaction", fmt.Sprintf("%.0f%% -> %.0f%% emergency=%.0f%% max=%d", cfg.Compression.TriggerPct, cfg.Compression.TargetPct, cfg.Compression.EmergencyPct, cfg.Compression.MaxCount))
	}
	inspectDoctorLock(r)
	inspectShell(r)
	if runtime.GOOS == "linux" {
		if _, _, _, ok := linuxMemorySnapshot(); ok {
			r.pass("/proc metrics", "MemTotal/MemAvailable/VmRSS readable")
		} else {
			r.warn("/proc metrics", "required memory metrics unavailable; fallback limits will apply")
		}
	}
	for _, path := range []string{filepath.Join(appBaseDir(), "skills"), memoryDir(), cfg.Worklog.Dir, filepath.Join(appBaseDir(), "plans")} {
		inspectPath(r, path)
	}
	inspectMemory(r)
	configureSkills(cfg.Skills, nil)
	scanSkills()
	skillMu.RLock()
	valid, invalid := len(skillIndex), len(skillErrors)
	skillMu.RUnlock()
	if invalid > 0 {
		r.warn("skill index", fmt.Sprintf("valid=%d rejected=%d", valid, invalid))
	} else {
		r.pass("skill index", fmt.Sprintf("valid=%d", valid))
	}
	if opts.Offline {
		r.warn("network", "skipped by --offline")
	} else if parseErr == nil && parsed.Host != "" {
		probeNetwork(r, parsed, cfg.LLM.ConnectTimeoutSeconds)
	}
	return r.severity
}

func inspectDoctorLock(r *doctorReporter) {
	exe, err := os.Executable()
	if err != nil {
		r.warn("instance lock", err.Error())
		return
	}
	exe, _ = filepath.Abs(exe)
	if resolved, e := filepath.EvalSymlinks(exe); e == nil {
		exe = resolved
	}
	hash := sha256.Sum256([]byte(exe))
	path := filepath.Join(os.TempDir(), "eliza-instance-locks", fmt.Sprintf("%x.lock", hash[:8]))
	meta, err := readInstanceMetadata(path)
	if os.IsNotExist(err) {
		r.pass("instance lock", "not held")
		return
	}
	if err != nil {
		r.warn("instance lock", fmt.Sprintf("residual/unreadable lock: %v", err))
		return
	}
	if processAlive(meta.PID) {
		r.warn("instance lock", fmt.Sprintf("held by PID %d", meta.PID))
	} else {
		r.warn("instance lock", fmt.Sprintf("stale lock from PID %d (doctor did not remove it)", meta.PID))
	}
}
func inspectShell(r *doctorReporter) {
	name := "sh"
	if runtime.GOOS == "windows" {
		name = "cmd.exe"
	}
	if path, err := exec.LookPath(name); err != nil {
		r.fail("command interpreter", err.Error())
	} else {
		r.pass("command interpreter", path)
	}
}
func inspectPath(r *doctorReporter, path string) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		r.warn("runtime path", "missing (normal startup will create): "+path)
		return
	}
	if err != nil {
		r.fail("runtime path", fmt.Sprintf("%s: %v", path, err))
		return
	}
	mode := info.Mode().Perm()
	if mode&0400 == 0 {
		r.fail("runtime path", fmt.Sprintf("not readable: %s mode=%o", path, mode))
		return
	}
	if mode&0200 == 0 {
		r.warn("runtime path", fmt.Sprintf("not owner-writable: %s mode=%o", path, mode))
		return
	}
	r.pass("runtime path", fmt.Sprintf("%s mode=%o", path, mode))
}
func inspectMemory(r *doctorReporter) {
	for _, name := range []string{"user.md", "project.md", "agent.md"} {
		path := filepath.Join(memoryDir(), name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			r.warn("memory "+name, "missing")
		} else if err != nil || !info.Mode().IsRegular() {
			r.fail("memory "+name, "invalid or unreadable")
		} else {
			r.pass("memory "+name, fmt.Sprintf("%d bytes", info.Size()))
		}
	}
}

func probeNetwork(r *doctorReporter, target *url.URL, timeoutSeconds int) {
	if timeoutSeconds <= 0 || timeoutSeconds > 10 {
		timeoutSeconds = 5
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	host := target.Hostname()
	addresses, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		r.fail("DNS", err.Error())
		return
	}
	r.pass("DNS", fmt.Sprintf("%s -> %s", host, strings.Join(addresses, ",")))
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	address := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: time.Duration(timeoutSeconds) * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		r.fail("TCP", err.Error())
		return
	}
	_ = connection.Close()
	r.pass("TCP", address)
	if target.Scheme == "https" {
		tlsConn, err := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			r.fail("TLS", err.Error())
			return
		}
		version := tlsConn.ConnectionState().Version
		_ = tlsConn.Close()
		r.pass("TLS", fmt.Sprintf("version=0x%x", version))
	}
	client := &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
	request, _ := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	response, err := client.Do(request)
	if err != nil {
		r.fail("HTTP", err.Error())
		return
	}
	_ = response.Body.Close()
	if response.StatusCode >= 500 {
		r.fail("HTTP", response.Status)
	} else if response.StatusCode >= 400 {
		r.warn("HTTP", response.Status+" (endpoint reachable; authentication or method may be required)")
	} else {
		r.pass("HTTP", response.Status)
	}
}
func mustGetwd() string {
	value, err := os.Getwd()
	if err != nil {
		return "<unknown>"
	}
	return value
}

func readEnvKeys(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	keys := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if index := strings.Index(line, "="); index > 0 {
			keys[strings.TrimSpace(line[:index])] = true
		}
	}
	return keys, scanner.Err()
}
