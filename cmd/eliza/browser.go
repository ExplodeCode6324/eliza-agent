package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	defaultBrowserTimeoutSeconds = 30
	defaultBrowserMaxTextBytes   = 24000
	defaultBrowserWaitMS         = 500
	maxBrowserWaitMS             = 10000
)

type BrowserRuntime struct {
	execPath     string
	timeout      time.Duration
	maxTextBytes int

	mu              sync.Mutex
	allocatorCtx    context.Context
	allocatorCancel context.CancelFunc
	browserCtx      context.Context
	browserCancel   context.CancelFunc
}

func NewBrowserRuntime(cfg BrowserPluginConfig) (*BrowserRuntime, bool) {
	if cfg.ToolsDir == "" {
		cfg.ToolsDir = defaultBrowserToolsDir()
	}
	execPath := discoverBrowserExecPath(cfg)
	if execPath == "" {
		return nil, false
	}
	timeoutSeconds := cfg.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultBrowserTimeoutSeconds
	}
	maxTextBytes := cfg.MaxTextBytes
	if maxTextBytes <= 0 {
		maxTextBytes = defaultBrowserMaxTextBytes
	}
	return &BrowserRuntime{
		execPath:     execPath,
		timeout:      time.Duration(timeoutSeconds) * time.Second,
		maxTextBytes: maxTextBytes,
	}, true
}

func defaultBrowserPluginConfig() BrowserPluginConfig {
	return BrowserPluginConfig{
		ChromiumDir:    "./plugins/chromium",
		ToolsDir:       defaultBrowserToolsDir(),
		TimeoutSeconds: defaultBrowserTimeoutSeconds,
		MaxTextBytes:   defaultBrowserMaxTextBytes,
	}
}

func defaultBrowserToolsDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "eliza", "tools")
	}
	return filepath.Join(appBaseDir(), "tools")
}

func (b *BrowserRuntime) run(parent context.Context, actions ...chromedp.Action) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensureLocked(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(b.browserCtx, b.timeout)
	defer cancel()
	done := make(chan struct{})
	if parent != nil {
		go func() {
			select {
			case <-parent.Done():
				cancel()
			case <-done:
			}
		}()
	}
	defer close(done)

	err := chromedp.Run(ctx, actions...)
	if err != nil && b.browserCtx.Err() != nil {
		b.resetLocked()
	}
	return err
}

func (b *BrowserRuntime) ensureLocked() error {
	if b.browserCtx != nil && b.browserCtx.Err() == nil {
		return nil
	}

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:len(chromedp.DefaultExecAllocatorOptions):len(chromedp.DefaultExecAllocatorOptions)],
		chromedp.ExecPath(b.execPath),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1280, 900),
	)
	b.allocatorCtx, b.allocatorCancel = chromedp.NewExecAllocator(context.Background(), opts...)
	b.browserCtx, b.browserCancel = chromedp.NewContext(b.allocatorCtx)

	startCtx, cancel := context.WithTimeout(b.browserCtx, b.timeout)
	defer cancel()
	if err := chromedp.Run(startCtx, chromedp.Navigate("about:blank")); err != nil {
		b.resetLocked()
		return fmt.Errorf("启动无头浏览器失败: %w", err)
	}
	return nil
}

func (b *BrowserRuntime) resetLocked() {
	if b.browserCancel != nil {
		b.browserCancel()
	}
	if b.allocatorCancel != nil {
		b.allocatorCancel()
	}
	b.browserCtx = nil
	b.browserCancel = nil
	b.allocatorCtx = nil
	b.allocatorCancel = nil
}

func (b *BrowserRuntime) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetLocked()
}

func discoverBrowserExecPath(cfg BrowserPluginConfig) string {
	if path := resolveExecutable(strings.TrimSpace(cfg.ExecPath)); path != "" {
		return path
	}

	toolsDir := strings.TrimSpace(cfg.ToolsDir)
	for _, baseDir := range []string{
		toolsDir,
		filepath.Join(toolsDir, "chromium"),
		filepath.Join(toolsDir, "chrome"),
		filepath.Join(toolsDir, "chrome-headless-shell"),
	} {
		if path := findChromium(baseDir); path != "" {
			return path
		}
	}

	if path := findChromium(strings.TrimSpace(cfg.ChromiumDir)); path != "" {
		return path
	}

	candidates := []string{
		"chromium",
		"chromium-browser",
		"google-chrome",
		"google-chrome-stable",
		"chrome",
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append([]string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			filepath.Join(os.Getenv("HOME"), "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
			filepath.Join(os.Getenv("HOME"), "Applications/Chromium.app/Contents/MacOS/Chromium"),
		}, candidates...)
	case "windows":
		candidates = append([]string{
			filepath.Join(os.Getenv("PROGRAMFILES"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES(X86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("PROGRAMFILES"), "Chromium", "Application", "chrome.exe"),
		}, candidates...)
	}

	for _, candidate := range candidates {
		if path := resolveExecutable(candidate); path != "" {
			return path
		}
	}
	return ""
}

func expandUserPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func resolveExecutable(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	if strings.ContainsAny(candidate, `/\`) || filepath.IsAbs(candidate) {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
		return ""
	}
	if path, err := exec.LookPath(candidate); err == nil {
		return path
	}
	return ""
}

type BrowserOpenTool struct {
	browser *BrowserRuntime
}

func (t *BrowserOpenTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "browser_open",
			Description: "用内置无头 Chromium 打开网页。参数: url (必填，http/https), wait_ms (可选，默认500)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "要打开的 http/https URL；省略协议时默认补 https://",
					},
					"wait_ms": map[string]any{
						"type":        "integer",
						"description": "导航后额外等待毫秒数，最大 10000",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (t *BrowserOpenTool) Execute(args map[string]any) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *BrowserOpenTool) ExecuteContext(ctx context.Context, args map[string]any) (string, error) {
	rawURL, _ := args["url"].(string)
	target, err := normalizeBrowserURL(rawURL)
	if err != nil {
		return "", err
	}
	wait, err := browserWaitArg(args)
	if err != nil {
		return "", err
	}

	var title, location string
	if err := t.browser.run(ctx,
		chromedp.Navigate(target),
		chromedp.Sleep(wait),
		chromedp.Title(&title),
		chromedp.Location(&location),
	); err != nil {
		return "", fmt.Errorf("打开网页失败: %w", err)
	}
	if strings.TrimSpace(title) == "" {
		title = "<empty>"
	}
	return fmt.Sprintf("url=%s\ntitle=%s", location, title), nil
}

type BrowserSnapshotTool struct {
	browser *BrowserRuntime
}

func (t *BrowserSnapshotTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "browser_snapshot",
			Description: "读取当前浏览器页面摘要，包括标题、URL、正文、主要链接、按钮和输入框。参数: max_chars (可选)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"max_chars": map[string]any{
						"type":        "integer",
						"description": "正文最大返回字符数，默认使用配置上限",
					},
				},
			},
		},
	}
}

func (t *BrowserSnapshotTool) Execute(args map[string]any) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *BrowserSnapshotTool) ExecuteContext(ctx context.Context, args map[string]any) (string, error) {
	maxChars := int64(t.browser.maxTextBytes)
	if requested, err := integerToolArg(args, "max_chars", maxChars); err == nil {
		maxChars = requested
	} else {
		return "", err
	}
	if maxChars <= 0 {
		return "", fmt.Errorf("max_chars 必须大于 0")
	}
	if maxChars > int64(t.browser.maxTextBytes) {
		maxChars = int64(t.browser.maxTextBytes)
	}

	var snapshot browserSnapshot
	if err := t.browser.run(ctx, chromedp.Evaluate(browserSnapshotScript, &snapshot)); err != nil {
		return "", fmt.Errorf("读取页面摘要失败: %w", err)
	}
	snapshot.Text = truncateString(snapshot.Text, int(maxChars))
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化页面摘要失败: %w", err)
	}
	return string(data), nil
}

type BrowserClickTool struct {
	browser *BrowserRuntime
}

func (t *BrowserClickTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "browser_click",
			Description: "点击当前页面中的元素，仅 autopilot 模式可用。参数: selector (必填), by (可选 search/query), wait_ms (可选)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector": map[string]any{
						"type":        "string",
						"description": "文本、CSS selector 或 XPath。默认 by=search 时三者都可尝试",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "search 或 query；query 表示 CSS selector",
						"enum":        []string{"search", "query"},
					},
					"wait_ms": map[string]any{
						"type":        "integer",
						"description": "点击后额外等待毫秒数，最大 10000",
					},
				},
				"required": []string{"selector"},
			},
		},
	}
}

func (t *BrowserClickTool) Execute(args map[string]any) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *BrowserClickTool) ExecuteContext(ctx context.Context, args map[string]any) (string, error) {
	selector, err := browserSelectorArg(args)
	if err != nil {
		return "", err
	}
	wait, err := browserWaitArg(args)
	if err != nil {
		return "", err
	}
	opts, err := browserQueryOptions(args)
	if err != nil {
		return "", err
	}
	actions := []chromedp.Action{chromedp.Click(selector, opts...)}
	if wait > 0 {
		actions = append(actions, chromedp.Sleep(wait))
	}
	if err := t.browser.run(ctx, actions...); err != nil {
		return "", fmt.Errorf("点击失败: %w", err)
	}
	return fmt.Sprintf("clicked selector=%q", selector), nil
}

type BrowserTypeTool struct {
	browser *BrowserRuntime
}

func (t *BrowserTypeTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "browser_type",
			Description: "向当前页面元素输入文本，仅 autopilot 模式可用。参数: selector (必填), text (必填), clear (可选), by (可选 search/query)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector": map[string]any{
						"type":        "string",
						"description": "输入框的文本、CSS selector 或 XPath。默认 by=search",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "要输入的文本",
					},
					"clear": map[string]any{
						"type":        "boolean",
						"description": "输入前清空现有内容，默认 true",
					},
					"by": map[string]any{
						"type":        "string",
						"description": "search 或 query；query 表示 CSS selector",
						"enum":        []string{"search", "query"},
					},
					"wait_ms": map[string]any{
						"type":        "integer",
						"description": "输入后额外等待毫秒数，最大 10000",
					},
				},
				"required": []string{"selector", "text"},
			},
		},
	}
}

func (t *BrowserTypeTool) Execute(args map[string]any) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *BrowserTypeTool) ExecuteContext(ctx context.Context, args map[string]any) (string, error) {
	selector, err := browserSelectorArg(args)
	if err != nil {
		return "", err
	}
	text, _ := args["text"].(string)
	if text == "" {
		return "", fmt.Errorf("缺少 text 参数")
	}
	clear := true
	if value, ok := args["clear"].(bool); ok {
		clear = value
	}
	wait, err := browserWaitArg(args)
	if err != nil {
		return "", err
	}
	opts, err := browserQueryOptions(args)
	if err != nil {
		return "", err
	}
	actions := []chromedp.Action{}
	if clear {
		actions = append(actions, chromedp.Clear(selector, opts...))
	}
	actions = append(actions, chromedp.SendKeys(selector, text, opts...))
	if wait > 0 {
		actions = append(actions, chromedp.Sleep(wait))
	}
	if err := t.browser.run(ctx, actions...); err != nil {
		return "", fmt.Errorf("输入失败: %w", err)
	}
	return fmt.Sprintf("typed selector=%q bytes=%d", selector, len(text)), nil
}

type BrowserScreenshotTool struct {
	browser *BrowserRuntime
	policy  *FilePolicy
}

func (t *BrowserScreenshotTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "browser_screenshot",
			Description: "保存当前页面截图到 workspace 内。参数: path (可选), full_page (可选，默认 true)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "截图保存路径，默认 browser/screenshots/<timestamp>.png",
					},
					"full_page": map[string]any{
						"type":        "boolean",
						"description": "是否截取完整页面，默认 true",
					},
				},
			},
		},
	}
}

func (t *BrowserScreenshotTool) Execute(args map[string]any) (string, error) {
	return t.ExecuteContext(context.Background(), args)
}

func (t *BrowserScreenshotTool) ExecuteContext(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		path = filepath.Join("browser", "screenshots", "screenshot_"+time.Now().Format("20060102_150405")+".png")
	}
	resolved, err := t.policy.ResolveWrite(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return "", fmt.Errorf("创建截图目录失败: %w", err)
	}

	fullPage := true
	if value, ok := args["full_page"].(bool); ok {
		fullPage = value
	}
	var image []byte
	var action chromedp.Action
	if fullPage {
		action = chromedp.FullScreenshot(&image, 100)
	} else {
		action = chromedp.CaptureScreenshot(&image)
	}
	if err := t.browser.run(ctx, action); err != nil {
		return "", fmt.Errorf("截图失败: %w", err)
	}
	if err := os.WriteFile(resolved, image, 0644); err != nil {
		return "", fmt.Errorf("写入截图失败: %w", err)
	}
	return fmt.Sprintf("screenshot=%s bytes=%d", resolved, len(image)), nil
}

type BrowserResetTool struct {
	browser *BrowserRuntime
}

func (t *BrowserResetTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "browser_reset",
			Description: "关闭并重置当前无头浏览器会话。无参数。",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *BrowserResetTool) Execute(args map[string]any) (string, error) {
	t.browser.Reset()
	return "browser reset", nil
}

func registerBrowserTools(registry *ToolRegistry, cfg BrowserPluginConfig, filePolicy *FilePolicy) bool {
	browser, ok := NewBrowserRuntime(cfg)
	if !ok {
		return false
	}
	registry.Register(&BrowserOpenTool{browser: browser})
	registry.Register(&BrowserSnapshotTool{browser: browser})
	registry.Register(&BrowserClickTool{browser: browser})
	registry.Register(&BrowserTypeTool{browser: browser})
	registry.Register(&BrowserScreenshotTool{browser: browser, policy: filePolicy})
	registry.Register(&BrowserResetTool{browser: browser})
	return true
}

func isBrowserAutopilotOnlyTool(name string) bool {
	return name == "browser_click" || name == "browser_type" || name == "browser_screenshot"
}

func normalizeBrowserURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("缺少 url 参数")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URL 无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("browser_open 仅允许 http/https URL")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("URL 缺少 host")
	}
	return parsed.String(), nil
}

func browserWaitArg(args map[string]any) (time.Duration, error) {
	waitMS, err := integerToolArg(args, "wait_ms", defaultBrowserWaitMS)
	if err != nil {
		return 0, err
	}
	if waitMS < 0 {
		return 0, fmt.Errorf("wait_ms 不能为负数")
	}
	if waitMS > maxBrowserWaitMS {
		waitMS = maxBrowserWaitMS
	}
	return time.Duration(waitMS) * time.Millisecond, nil
}

func browserSelectorArg(args map[string]any) (string, error) {
	selector, _ := args["selector"].(string)
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return "", fmt.Errorf("缺少 selector 参数")
	}
	return selector, nil
}

func browserQueryOptions(args map[string]any) ([]chromedp.QueryOption, error) {
	by, _ := args["by"].(string)
	by = strings.ToLower(strings.TrimSpace(by))
	switch by {
	case "", "search":
		return []chromedp.QueryOption{chromedp.BySearch, chromedp.NodeVisible}, nil
	case "query":
		return []chromedp.QueryOption{chromedp.ByQuery, chromedp.NodeVisible}, nil
	default:
		return nil, fmt.Errorf("未知 by=%q，可用: search / query", by)
	}
}

func truncateString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + fmt.Sprintf("\n[TRUNCATED bytes=%d max=%d]", len(value), limit)
}

type browserSnapshot struct {
	Title   string                 `json:"title"`
	URL     string                 `json:"url"`
	Text    string                 `json:"text"`
	Links   []browserSnapshotItem  `json:"links"`
	Buttons []browserSnapshotItem  `json:"buttons"`
	Inputs  []browserSnapshotInput `json:"inputs"`
	Meta    map[string]any         `json:"meta,omitempty"`
}

type browserSnapshotItem struct {
	Text     string `json:"text"`
	Href     string `json:"href,omitempty"`
	Selector string `json:"selector,omitempty"`
}

type browserSnapshotInput struct {
	Label       string `json:"label"`
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Selector    string `json:"selector,omitempty"`
}

const browserSnapshotScript = `(() => {
  const clean = (value, max = 180) => String(value || "").replace(/\s+/g, " ").trim().slice(0, max);
  const cssEscape = (value) => {
    if (window.CSS && CSS.escape) return CSS.escape(value);
    return String(value).replace(/["\\#.:,[\]>+~*=\s]/g, "\\$&");
  };
  const cssPath = (el) => {
    if (!el || !el.tagName) return "";
    if (el.id) return "#" + cssEscape(el.id);
    const parts = [];
    let node = el;
    while (node && node.nodeType === Node.ELEMENT_NODE && parts.length < 5) {
      let part = node.tagName.toLowerCase();
      if (node.classList && node.classList.length) {
        part += "." + Array.from(node.classList).slice(0, 2).map(cssEscape).join(".");
      }
      const parent = node.parentElement;
      if (parent) {
        const same = Array.from(parent.children).filter((child) => child.tagName === node.tagName);
        if (same.length > 1) part += ":nth-of-type(" + (same.indexOf(node) + 1) + ")";
      }
      parts.unshift(part);
      node = parent;
    }
    return parts.join(" > ");
  };
  const labelFor = (el) => {
    if (!el) return "";
    if (el.id) {
      const label = Array.from(document.querySelectorAll("label")).find((item) => item.htmlFor === el.id);
      if (label) return clean(label.innerText);
    }
    return clean(el.getAttribute("aria-label") || el.getAttribute("title") || el.placeholder || el.name || el.innerText || el.value || "");
  };
  const links = Array.from(document.querySelectorAll("a[href]")).slice(0, 40).map((el) => ({
    text: clean(el.innerText || el.getAttribute("aria-label") || el.href),
    href: el.href,
    selector: cssPath(el)
  }));
  const buttons = Array.from(document.querySelectorAll("button,input[type='button'],input[type='submit'],a[role='button']")).slice(0, 40).map((el) => ({
    text: labelFor(el),
    selector: cssPath(el)
  }));
  const inputs = Array.from(document.querySelectorAll("input,textarea,select")).slice(0, 40).map((el) => ({
    label: labelFor(el),
    type: clean(el.type || el.tagName.toLowerCase(), 40),
    name: clean(el.name, 80),
    placeholder: clean(el.placeholder, 120),
    selector: cssPath(el)
  }));
  return {
    title: document.title || "",
    url: location.href,
    text: document.body ? document.body.innerText || "" : "",
    links,
    buttons,
    inputs,
    meta: {
      link_count: document.querySelectorAll("a[href]").length,
      button_count: document.querySelectorAll("button,input[type='button'],input[type='submit'],a[role='button']").length,
      input_count: document.querySelectorAll("input,textarea,select").length
    }
  };
})()`
