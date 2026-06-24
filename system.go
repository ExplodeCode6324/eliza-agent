package main

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// SystemInfo 每次启动重新探测，不写入持久缓存。
type SystemInfo struct {
	OS           string
	Architecture string
	Distribution string
	Kernel       string
	Hostname     string
}

func DetectSystemInfo() SystemInfo {
	info := SystemInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}
	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}

	if runtime.GOOS == "linux" {
		info.Distribution = detectLinuxDistribution()
		if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			info.Kernel = strings.TrimSpace(string(data))
		}
	} else {
		info.Distribution = runtime.GOOS
	}
	return info
}

func detectLinuxDistribution() string {
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		values := parseOSRelease(string(data))
		if pretty := values["PRETTY_NAME"]; pretty != "" {
			return pretty
		}
		name := values["NAME"]
		version := values["VERSION"]
		if name != "" && version != "" {
			return name + " " + version
		}
		if name != "" {
			return name
		}
	}

	for _, path := range []string{"/etc/kylin-release", "/etc/redhat-release", "/etc/centos-release", "/etc/issue"} {
		if line := readFirstNonEmptyLine(path); line != "" {
			return line
		}
	}
	return "Linux (unknown distribution)"
}

func parseOSRelease(content string) map[string]string {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, "'\"")
		}
		values[key] = value
	}
	return values
}

func readFirstNonEmptyLine(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			return line
		}
	}
	return ""
}

func (s SystemInfo) Prompt() string {
	var builder strings.Builder
	builder.WriteString("\n\n运行系统信息（每次启动重新探测）：\n")
	builder.WriteString("- OS: " + valueOrUnknown(s.OS) + "\n")
	builder.WriteString("- Architecture: " + valueOrUnknown(s.Architecture) + "\n")
	builder.WriteString("- Distribution: " + valueOrUnknown(s.Distribution) + "\n")
	builder.WriteString("- Kernel: " + valueOrUnknown(s.Kernel) + "\n")
	builder.WriteString("- Hostname: " + valueOrUnknown(s.Hostname) + "\n")
	if s.OS == "windows" {
		builder.WriteString("工具与命令原则：当前系统是 Windows，使用 cmd.exe 可执行的 Windows 命令；不要生成 Linux shell 命令。执行前先确认 PowerShell、系统组件和命令版本是否存在。")
	} else {
		builder.WriteString("工具与命令原则：项目以 Linux/POSIX 为主要目标；不要假设 bash、systemd 或特定 GNU 工具必然存在；在老版本 Linux、麒麟等环境中，必要时先用 which、uname 或读取系统文件确认能力。")
	}
	return builder.String()
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
