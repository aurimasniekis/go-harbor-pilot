// Package claudecode executes requests through the vendor `claude` CLI
// with a subscription setup-token, one isolated process per conversation.
//
// A request is turned into a CLI session: the conversation history becomes
// a session transcript resumed with --resume, the last user turn is written
// to stdin as stream-json, and the client's tools are exposed through a
// per-process loopback MCP endpoint. Built-in CLI tools are disabled.
//
// When the model calls a client tool the response ends with that tool call
// and the process is parked on the blocked MCP call. The client's next
// request carries the tool result, which answers the parked call so the
// same process continues; a plain follow-up turn is written to the parked
// process's stdin. A conversation that does not match a parked process
// starts a new one from the transcript.
package claudecode

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/bridge"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
)

// RateLimitWindow is one subscription window reported by the CLI.
type RateLimitWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
	Status      string  `json:"status"`
}

// RateLimitInfo is the CLI's rate_limit_event payload.
type RateLimitInfo struct {
	Status         string                     `json:"status"`
	ResetsAt       int64                      `json:"resetsAt"`
	RateLimitType  string                     `json:"rateLimitType"`
	Utilization    *float64                   `json:"utilization"`
	UnifiedWindows map[string]RateLimitWindow `json:"unifiedWindows"`
}

// Options configure the bridge.
type Options struct {
	// Binary is the claude executable (default: "claude" on PATH).
	Binary string
	// Root holds per-account config directories and per-process work dirs.
	Root string
	// ExtraEnv is appended to the child environment. Tests use it to point
	// the CLI at a synthetic upstream; production leaves it empty.
	ExtraEnv []string
	// MaxProcesses bounds live processes (default 16); MaxPerAccount bounds
	// them per account (default 4).
	MaxProcesses  int
	MaxPerAccount int
	// IdleTimeout ends parked processes waiting for a follow-up turn
	// (default 10m); ToolTimeout ends processes waiting for client tool
	// results (default 30m).
	IdleTimeout time.Duration
	ToolTimeout time.Duration
	Log         *slog.Logger
	Now         func() time.Time
	// OnRateLimit receives subscription window observations.
	OnRateLimit func(accountID string, info RateLimitInfo)
}

// Bridge implements executor.Executor for claude_code providers.
type Bridge struct {
	o   Options
	mcp *bridge.MCPServer

	mu    sync.Mutex
	procs map[string]*proc
	// reserved counts spawns in progress per account (and overall), so
	// concurrent spawns cannot exceed the process limits.
	reserved map[string]int
	stop     chan struct{}
	once     sync.Once
}

// New creates the bridge and starts its reaper.
func New(o Options) *Bridge {
	if o.Binary == "" {
		o.Binary = "claude"
	}
	if o.MaxProcesses <= 0 {
		o.MaxProcesses = 16
	}
	if o.MaxPerAccount <= 0 {
		o.MaxPerAccount = 4
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 10 * time.Minute
	}
	if o.ToolTimeout <= 0 {
		o.ToolTimeout = 30 * time.Minute
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	b := &Bridge{o: o, mcp: bridge.NewMCPServer(), procs: map[string]*proc{}, reserved: map[string]int{}, stop: make(chan struct{})}
	go b.reaper()
	return b
}

// Close ends every process.
func (b *Bridge) Close() {
	b.once.Do(func() { close(b.stop) })
	b.mu.Lock()
	ps := make([]*proc, 0, len(b.procs))
	for _, p := range b.procs {
		ps = append(ps, p)
	}
	b.mu.Unlock()
	for _, p := range ps {
		p.kill("bridge closed")
	}
	b.mcp.Close()
}

// Version runs `claude --version` (startup self-test).
func (b *Bridge) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, b.o.Binary, "--version").Output() //nolint:gosec // operator-configured binary
	if err != nil {
		return "", fmt.Errorf("claude --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (b *Bridge) reaper() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
		}
		now := b.o.Now()
		b.mu.Lock()
		var dead []*proc
		for _, p := range b.procs {
			p.mu.Lock()
			st, last := p.state, p.lastUsed
			p.mu.Unlock()
			if (st == stateIdle && now.Sub(last) > b.o.IdleTimeout) || (st == stateAwaitingTools && now.Sub(last) > b.o.ToolTimeout) {
				dead = append(dead, p)
			}
		}
		b.mu.Unlock()
		for _, p := range dead {
			p.kill("idle timeout")
		}
		b.sweepConversationFiles(now)
	}
}

// cacheDirs are the CLI's per-working-directory caches (MCP logs and
// similar), keyed by the escaped working directory.
func cacheDirs(home, work string) []string {
	if home == "" || work == "" {
		return nil
	}
	esc := unsafePath.ReplaceAllString(work, "-")
	return []string{
		filepath.Join(home, "Library", "Caches", "claude-cli-nodejs", esc),
		filepath.Join(home, ".cache", "claude-cli-nodejs", esc),
	}
}

// sweepConversationFiles removes transcripts, caches and run files that no
// live process owns (left behind by a crash or restart), so conversation
// content never outlives its process by more than one idle period.
func (b *Bridge) sweepConversationFiles(now time.Time) {
	b.mu.Lock()
	live := map[string]bool{}
	for _, p := range b.procs {
		live[p.workDir], live[p.runDir], live[p.sessDir] = true, true, true
		for _, d := range cacheDirs(p.home, p.workDir) {
			live[d] = true
		}
	}
	b.mu.Unlock()
	grace := max(b.o.IdleTimeout, b.o.ToolTimeout)
	var parents []string
	accounts, _ := filepath.Glob(filepath.Join(b.o.Root, "accounts", "*"))
	for _, a := range accounts {
		parents = append(parents, filepath.Join(a, "config", "projects"), filepath.Join(a, "home", "Library", "Caches", "claude-cli-nodejs"),
			filepath.Join(a, "home", ".cache", "claude-cli-nodejs"))
	}
	parents = append(parents, filepath.Join(b.o.Root, "w"), filepath.Join(b.o.Root, "r"))
	for _, parent := range parents {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(parent, e.Name())
			if live[path] {
				continue
			}
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > grace {
				_ = os.RemoveAll(path)
			}
		}
	}
}

// ---------- request shaping ----------

// conv is the Anthropic-shaped view of a request.
type conv struct {
	System   string
	Messages []json.RawMessage
	Tools    []bridge.MCPTool
	// nameMap maps model-visible tool names to client tool names.
	nameMap map[string]string
	toMCP   map[string]string
}

const toolPrefix = "mcp__client__"

var mcpNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,50}$`)

func mcpName(client string) string {
	if mcpNameRE.MatchString(client) {
		return client
	}
	h := sha256.Sum256([]byte(client))
	return "t_" + hex.EncodeToString(h[:6])
}

func shapeRequest(r *ir.Request, model string) (*conv, []ir.Loss, error) {
	body, losses, err := anthropic.BuildRequest(r, anthropic.BuildOptions{Model: model, DefaultMaxTokens: 8192, Stream: true, InlineSystemMessages: true})
	if err != nil {
		return nil, nil, err
	}
	var raw struct {
		System   json.RawMessage   `json:"system"`
		Messages []json.RawMessage `json:"messages"`
		Tools    []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
			Type        string          `json:"type"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, err
	}
	c := &conv{Messages: raw.Messages, nameMap: map[string]string{}, toMCP: map[string]string{}}
	c.System = systemText(raw.System)
	for _, t := range raw.Tools {
		if t.Type != "" && t.Type != "custom" {
			losses = append(losses, ir.Loss{Severity: ir.LossDrop, Feature: "builtin_tool:" + t.Type, Detail: "provider-executed tools are not available through the CLI bridge"})
			continue
		}
		mn := mcpName(t.Name)
		c.toMCP[t.Name] = mn
		c.nameMap[toolPrefix+mn] = t.Name
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		c.Tools = append(c.Tools, bridge.MCPTool{Name: mn, Description: t.Description, InputSchema: schema})
	}
	if r.Temperature != nil || r.TopP != nil || r.TopK != nil || len(r.Stop) > 0 {
		losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "sampling", Detail: "sampling parameters and stop sequences are not passed to the CLI"})
	}
	if r.MaxOutputTokens != nil {
		losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "max_output_tokens", Detail: "the CLI chooses its own output limit"})
	}
	if r.ToolChoice != nil && r.ToolChoice.Mode != "" && r.ToolChoice.Mode != "auto" {
		losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "tool_choice", Detail: "forced tool choice is not passed to the CLI"})
	}
	losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "system_prompt", Detail: "the CLI prefixes its own identity line and environment note to the system prompt"})
	return c, losses, nil
}

func systemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// renameTools rewrites tool_use names in a message to the CLI-visible names.
func (c *conv) renameTools(msg json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(msg, &m) != nil {
		return msg
	}
	blocks, ok := m["content"].([]any)
	if !ok {
		return msg
	}
	for _, b := range blocks {
		bm, ok := b.(map[string]any)
		if !ok || bm["type"] != "tool_use" {
			continue
		}
		if n, ok := bm["name"].(string); ok {
			mn, known := c.toMCP[n]
			if !known {
				mn = mcpName(n)
			}
			bm["name"] = toolPrefix + mn
		}
	}
	out, _ := json.Marshal(m)
	return out
}

// fingerprint identifies a conversation prefix independent of formatting:
// roles, text, tool call ids and tool result ids, ignoring reasoning
// blocks and cache markers (clients differ in whether they echo those).
func fingerprint(msgs []json.RawMessage) string {
	h := sha256.New()
	for _, raw := range msgs {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(raw, &m)
		h.Write([]byte("\x00" + m.Role + "\x00"))
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			h.Write([]byte("t:" + strings.TrimSpace(s) + "\x01"))
			continue
		}
		var blocks []map[string]any
		_ = json.Unmarshal(m.Content, &blocks)
		for _, b := range blocks {
			switch b["type"] {
			case "text":
				t, _ := b["text"].(string)
				h.Write([]byte("t:" + strings.TrimSpace(t) + "\x01"))
			case "tool_use":
				id, _ := b["id"].(string)
				h.Write([]byte("u:" + id + "\x01"))
			case "tool_result":
				id, _ := b["tool_use_id"].(string)
				h.Write([]byte("r:" + id + "\x01"))
			case "image", "document":
				h.Write([]byte("m:\x01"))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashOf(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

func toolsKey(ts []bridge.MCPTool) string {
	b, _ := json.Marshal(ts)
	return hashOf(string(b))
}

// lastUser splits a request into history and its final user message.
func lastUser(msgs []json.RawMessage) ([]json.RawMessage, json.RawMessage, error) {
	if len(msgs) == 0 {
		return nil, nil, errors.New("the request has no messages")
	}
	var m struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(msgs[len(msgs)-1], &m)
	if m.Role != "user" {
		return nil, nil, errors.New("the last message must be from the user")
	}
	return msgs[:len(msgs)-1], msgs[len(msgs)-1], nil
}

// toolResults extracts tool results (by tool_use id) and any extra text
// blocks from a user message.
func toolResults(msg json.RawMessage) (map[string]bridge.ToolReply, []string) {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	_ = json.Unmarshal(msg, &m)
	var blocks []map[string]any
	if json.Unmarshal(m.Content, &blocks) != nil {
		var s string
		if json.Unmarshal(m.Content, &s) == nil && s != "" {
			return nil, []string{s}
		}
		return nil, nil
	}
	res := map[string]bridge.ToolReply{}
	var extra []string
	for _, b := range blocks {
		switch b["type"] {
		case "tool_result":
			id, _ := b["tool_use_id"].(string)
			isErr, _ := b["is_error"].(bool)
			res[id] = bridge.ToolReply{Content: mcpContent(b["content"]), IsError: isErr}
		case "text":
			if t, _ := b["text"].(string); t != "" {
				extra = append(extra, t)
			}
		}
	}
	return res, extra
}

func mcpContent(v any) []bridge.MCPContent {
	switch c := v.(type) {
	case string:
		return []bridge.MCPContent{{Type: "text", Text: c}}
	case []any:
		out := []bridge.MCPContent{}
		for _, it := range c {
			m, _ := it.(map[string]any)
			switch m["type"] {
			case "text":
				t, _ := m["text"].(string)
				out = append(out, bridge.MCPContent{Type: "text", Text: t})
			case "image":
				src, _ := m["source"].(map[string]any)
				if src["type"] == "base64" {
					d, _ := src["data"].(string)
					mt, _ := src["media_type"].(string)
					out = append(out, bridge.MCPContent{Type: "image", Data: d, MimeType: mt})
				}
			}
		}
		if len(out) == 0 {
			out = append(out, bridge.MCPContent{Type: "text", Text: ""})
		}
		return out
	}
	return []bridge.MCPContent{{Type: "text", Text: ""}}
}

// ---------- process ----------

type procState int

const (
	stateBusy procState = iota
	stateIdle
	stateAwaitingTools
	stateDead
)

type line struct {
	raw  []byte
	head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
}

type proc struct {
	b         *Bridge
	id        string
	accountID string
	model     string
	sysKey    string
	toolsKey  string
	conv      *conv
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	lines     chan line
	sess      *bridge.Session
	workDir   string
	runDir    string // private files: MCP config with the session token, system prompt
	sessDir   string // the CLI's transcript directory for this process
	home      string // the account's HOME (per-workdir CLI caches live under it)
	scope     string // gateway client scope; processes are reused only within it

	mu       sync.Mutex
	state    procState
	fp       string   // fingerprint of the conversation the process holds
	pending  []string // tool_use ids awaiting client results
	lastUsed time.Time
	exitErr  error
	retryAt  int64 // latest rate-limit reset reported by the CLI
}

func (p *proc) kill(reason string) {
	p.mu.Lock()
	if p.state == stateDead {
		p.mu.Unlock()
		return
	}
	p.state = stateDead
	p.mu.Unlock()
	bridge.KillTree(p.cmd)
	p.sess.Close()
	_ = p.stdin.Close()
	p.b.mu.Lock()
	delete(p.b.procs, p.id)
	p.b.mu.Unlock()
	go func() {
		_ = p.cmd.Wait()
		// Conversation content must not outlive the process: remove the
		// work dir, the private run files and the CLI's transcript.
		_ = os.RemoveAll(p.workDir)
		_ = os.RemoveAll(p.runDir)
		if p.sessDir != "" {
			_ = os.RemoveAll(p.sessDir)
		}
		for _, d := range cacheDirs(p.home, p.workDir) {
			_ = os.RemoveAll(d)
		}
	}()
	p.b.o.Log.Debug("bridge.process.ended", "process", p.id, "account_id", p.accountID, "reason", reason)
}

func randID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

var unsafePath = regexp.MustCompile(`[^A-Za-z0-9]`)

// spawn starts a process holding history; the caller then writes the next
// user message.
func (b *Bridge) spawn(ctx context.Context, accountID, scope string, cred credentials.Secret, model, effort string, c *conv, history []json.RawMessage) (_ *proc, err error) {
	release, err := b.reserveSlot(accountID)
	if err != nil {
		return nil, err
	}
	defer release()
	base := filepath.Join(b.o.Root, "accounts", safeName(accountID))
	cfgDir, home := filepath.Join(base, "config"), filepath.Join(base, "home")
	for _, d := range []string{cfgDir, home, filepath.Join(b.o.Root, "w")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	work, err := os.MkdirTemp(filepath.Join(b.o.Root, "w"), "p")
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(work); err == nil {
		work = real
	}
	sessDir := filepath.Join(cfgDir, "projects", unsafePath.ReplaceAllString(work, "-"))
	var sess *bridge.Session
	runDir := ""
	defer func() {
		if err == nil {
			return
		}
		if sess != nil {
			sess.Close()
		}
		_ = os.RemoveAll(work)
		_ = os.RemoveAll(sessDir)
		if runDir != "" {
			_ = os.RemoveAll(runDir)
		}
	}()
	if sess, err = b.mcp.NewSession(c.Tools); err != nil {
		return nil, err
	}
	// The MCP session token and the system prompt go in 0600 files, not in
	// arguments that any local user can read.
	if runDir, err = bridge.PrivateDir(b.o.Root); err != nil {
		return nil, err
	}
	mcpCfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"client": map[string]any{"type": "http", "url": sess.URL(),
		"headers": map[string]string{"Authorization": "Bearer " + sess.Token()}}}})
	mcpPath, sysPath := filepath.Join(runDir, "mcp.json"), filepath.Join(runDir, "system.txt")
	if err = bridge.WritePrivate(mcpPath, mcpCfg); err != nil {
		return nil, err
	}
	if err = bridge.WritePrivate(sysPath, []byte(c.System)); err != nil {
		return nil, err
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--strict-mcp-config", "--mcp-config", mcpPath, "--tools", "", "--allowedTools", "mcp__client",
		"--setting-sources", "", "--model", model, "--system-prompt-file", sysPath}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	if len(history) > 0 {
		sid := newUUID()
		if err = os.MkdirAll(sessDir, 0o700); err != nil {
			return nil, err
		}
		if err = writeTranscript(filepath.Join(sessDir, sid+".jsonl"), sid, work, model, c, history); err != nil {
			return nil, err
		}
		args = append(args, "--resume", sid)
	} else {
		args = append(args, "--no-session-persistence")
	}
	cmd := exec.Command(b.o.Binary, args...) //nolint:gosec // operator-configured binary, arguments are not shell-interpreted
	cmd.Dir = work
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"CLAUDE_CONFIG_DIR=" + cfgDir,
		"CLAUDE_CODE_OAUTH_TOKEN=" + cred.Reveal(),
		"DISABLE_AUTOUPDATER=1",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_MAX_RETRIES=0",
		"DISABLE_AUTO_COMPACT=1",
		"DISABLE_COMPACT=1",
		"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
		"CLAUDE_CODE_SKIP_PROMPT_HISTORY=1",
		"ENABLE_TOOL_SEARCH=false",
		"MCP_TOOL_TIMEOUT=" + fmt.Sprint(b.o.ToolTimeout.Milliseconds()+60_000),
	}, b.o.ExtraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	bridge.Isolate(cmd)
	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	p := &proc{b: b, id: randID(8), accountID: accountID, scope: scope, model: model, sysKey: hashOf(c.System), toolsKey: toolsKey(c.Tools), conv: c,
		cmd: cmd, stdin: stdin, lines: make(chan line, 256), sess: sess, workDir: work, runDir: runDir, sessDir: sessDir, home: home, state: stateBusy,
		lastUsed: b.o.Now()}
	b.mu.Lock()
	b.procs[p.id] = p
	b.mu.Unlock()
	go p.read(stdout)
	return p, nil
}

func safeName(s string) string { return unsafePath.ReplaceAllString(s, "_") }

// PruneAccounts removes the private directories (CLI config, transcripts,
// caches) of accounts that no longer exist, unless a process of theirs is
// still running.
func (b *Bridge) PruneAccounts(active map[string]bool) {
	keep := map[string]bool{}
	for id := range active {
		keep[safeName(id)] = true
	}
	b.mu.Lock()
	for _, p := range b.procs {
		keep[safeName(p.accountID)] = true
	}
	b.mu.Unlock()
	entries, _ := os.ReadDir(filepath.Join(b.o.Root, "accounts"))
	for _, e := range entries {
		if e.IsDir() && !keep[e.Name()] {
			_ = os.RemoveAll(filepath.Join(b.o.Root, "accounts", e.Name()))
		}
	}
}

// reserveSlot holds a provisional slot while a process is being spawned, so
// concurrent spawns respect the limits; release it once the process is
// registered (or the spawn failed).
func (b *Bridge) reserveSlot(accountID string) (release func(), err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	release = func() {
		b.mu.Lock()
		if b.reserved[accountID]--; b.reserved[accountID] <= 0 {
			delete(b.reserved, accountID)
		}
		b.mu.Unlock()
	}
	total := 0
	for _, n := range b.reserved {
		total += n
	}
	per := b.reserved[accountID]
	var idle []*proc
	for _, p := range b.procs {
		if p.accountID == accountID {
			per++
		}
		p.mu.Lock()
		if p.state == stateIdle {
			idle = append(idle, p)
		}
		p.mu.Unlock()
	}
	if len(b.procs)+total < b.o.MaxProcesses && per < b.o.MaxPerAccount {
		b.reserved[accountID]++
		return release, nil
	}
	// Evict the least recently used idle process (of this account first).
	sort.Slice(idle, func(i, j int) bool {
		ai, aj := idle[i].accountID == accountID, idle[j].accountID == accountID
		if ai != aj {
			return ai
		}
		return idle[i].lastUsed.Before(idle[j].lastUsed)
	})
	if len(idle) > 0 && (len(b.procs)+total >= b.o.MaxProcesses || idle[0].accountID == accountID) {
		victim := idle[0]
		b.reserved[accountID]++
		b.mu.Unlock()
		victim.kill("evicted")
		b.mu.Lock()
		return release, nil
	}
	return nil, errBusy
}

var errBusy = errors.New("all CLI bridge processes for this account are busy")

func (p *proc) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
	for sc.Scan() {
		raw := append([]byte(nil), sc.Bytes()...)
		var l line
		if json.Unmarshal(raw, &l.head) != nil {
			continue // diagnostic text, not protocol
		}
		l.raw = raw
		if l.head.Type == "rate_limit_event" {
			var ev struct {
				Info RateLimitInfo `json:"rate_limit_info"`
			}
			if json.Unmarshal(raw, &ev) == nil {
				p.mu.Lock()
				p.retryAt = ev.Info.ResetsAt
				p.mu.Unlock()
				if p.b.o.OnRateLimit != nil {
					p.b.o.OnRateLimit(p.accountID, ev.Info)
				}
			}
		}
		p.lines <- l
	}
	p.mu.Lock()
	p.exitErr = sc.Err()
	p.mu.Unlock()
	close(p.lines)
	p.kill("process exited")
}

func (p *proc) send(msg json.RawMessage) error {
	b, err := json.Marshal(map[string]any{"type": "user", "message": json.RawMessage(msg)})
	if err != nil {
		return err
	}
	_, err = p.stdin.Write(append(b, '\n'))
	return err
}

// ---------- transcript ----------

func writeTranscript(path, sid, cwd, model string, c *conv, history []json.RawMessage) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	var parent any
	ts := time.Now().UTC()
	for i, raw := range history {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(c.renameTools(raw), &m); err != nil {
			return err
		}
		u := newUUID()
		e := map[string]any{"parentUuid": parent, "isSidechain": false, "type": m.Role, "uuid": u, "userType": "external",
			"timestamp": ts.Add(time.Duration(i) * time.Millisecond).Format("2006-01-02T15:04:05.000Z"), "cwd": cwd, "sessionId": sid}
		if m.Role == "assistant" {
			e["message"] = map[string]any{"id": "msg_hist_" + fmt.Sprint(i), "type": "message", "role": "assistant", "model": model,
				"content": m.Content, "stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0}}
		} else {
			e["message"] = map[string]any{"role": "user", "content": m.Content}
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		_, _ = w.Write(append(b, '\n'))
		parent = u
	}
	return w.Flush()
}

// ---------- execute ----------

// Execute implements executor.Executor.
func (b *Bridge) Execute(ctx context.Context, t executor.Target, cred credentials.Secret, req executor.Request, stream bool) (*executor.Result, *executor.AttemptError) {
	if cred.IsZero() {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrAuthentication, Message: "the account has no setup-token configured"}, AccountScoped: true}
	}
	c, losses, err := shapeRequest(req.IR, t.Model)
	if err != nil {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInvalidRequest, Message: err.Error()}}
	}
	history, last, err := lastUser(c.Messages)
	if err != nil {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInvalidRequest, Message: err.Error()}}
	}
	if ir.HasReject(losses) && !req.AllowLosses {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUnsupported, Message: "the request needs features the CLI bridge cannot provide"}}
	}
	effort := ""
	if r := req.IR.Reasoning; r != nil {
		switch r.Effort {
		case "low", "medium", "high", "xhigh", "max":
			effort = r.Effort
		}
	}
	histFP := fingerprint(history)
	results, extra := toolResults(last)

	// 1. A parked process holding exactly this conversation continues.
	p := b.claim(t.Account.ID, req.Scope, t.Model, c, histFP, results)
	if p != nil {
		p.conv = c
		if len(results) > 0 && len(p.pending) > 0 {
			if len(extra) > 0 {
				losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "tool_result_text", Detail: "text sent alongside tool results is appended to the last tool result"})
				lastID := p.pending[len(p.pending)-1]
				r := results[lastID]
				for _, x := range extra {
					r.Content = append(r.Content, bridge.MCPContent{Type: "text", Text: x})
				}
				results[lastID] = r
			}
			for _, id := range p.pending {
				p.sess.Reply(id, results[id])
			}
			p.mu.Lock()
			p.pending = nil
			p.mu.Unlock()
		} else if err := p.send(c.renameTools(last)); err != nil {
			p.kill("stdin closed")
			p = nil
		}
	}
	// 2. Otherwise start a process from the transcript.
	if p == nil {
		if len(results) > 0 {
			// The CLI cannot accept a tool result for a call it did not make;
			// the history already carries the call, so the result is sent as
			// the next turn and the CLI treats the earlier call as completed.
			losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "tool_continuation", Detail: "no live bridge process held this tool call; the conversation was resumed from its transcript"})
			history = append(history, last)
			last = json.RawMessage(`{"role":"user","content":[{"type":"text","text":"Continue."}]}`)
		}
		np, err := b.spawn(ctx, t.Account.ID, req.Scope, cred, t.Model, effort, c, history)
		if errors.Is(err, errBusy) {
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrOverloaded, Message: err.Error()}}
		}
		if err != nil {
			b.o.Log.Error("bridge.spawn.failed", "account_id", t.Account.ID, "error", err.Error())
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInternal, Message: "the CLI bridge could not start"}}
		}
		if err := np.send(c.renameTools(last)); err != nil {
			np.kill("stdin closed")
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInternal, Message: "the CLI bridge could not start"}}
		}
		p = np
	}
	events := make(chan ir.Event, 64)
	actx, cancel := context.WithCancel(ctx)
	res := &executor.Result{Upstream: ir.ClaudeCodeBridge, Mode: executor.ModeTranslated, Losses: losses, Status: 200, FirstByteAt: time.Now(), Cancel: cancel}
	var serr error
	go func() {
		defer close(events)
		serr = p.turn(actx, c, append(append([]json.RawMessage{}, history...), last), events)
	}()
	res.Err = func() error { return serr }
	if !stream {
		r, err := ir.Collect(events)
		cancel()
		if err != nil {
			var ie *ir.Error
			if errors.As(err, &ie) {
				return nil, &executor.AttemptError{Err: ie, AccountScoped: accountScoped(ie.Kind)}
			}
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUpstream, Message: "the CLI bridge failed"}, Executed: true}
		}
		res.Response = r
		res.Stream = nil
	} else {
		res.Stream = events
	}
	return res, nil
}

func accountScoped(k ir.ErrorKind) bool {
	return k == ir.ErrAuthentication || k == ir.ErrPermission || k == ir.ErrRateLimited || k == ir.ErrQuotaExhausted
}

// claim takes a parked process matching the conversation.
func (b *Bridge) claim(accountID, scope, model string, c *conv, histFP string, results map[string]bridge.ToolReply) *proc {
	b.mu.Lock()
	defer b.mu.Unlock()
	sk, tk := hashOf(c.System), toolsKey(c.Tools)
	for _, p := range b.procs {
		p.mu.Lock()
		ok := p.accountID == accountID && p.scope == scope && p.model == model && p.sysKey == sk && p.toolsKey == tk && p.fp == histFP
		if ok {
			switch p.state {
			case stateIdle:
				ok = len(results) == 0
			case stateAwaitingTools:
				ok = len(results) > 0
				for _, id := range p.pending {
					if _, have := results[id]; !have {
						ok = false
					}
				}
			default:
				ok = false
			}
		}
		if ok {
			p.state = stateBusy
			p.lastUsed = b.o.Now()
			p.mu.Unlock()
			return p
		}
		p.mu.Unlock()
	}
	return nil
}

// turn relays one client-visible turn: it ends at the CLI's result line or
// when the model calls client tools.
func (p *proc) turn(ctx context.Context, c *conv, sent []json.RawMessage, out chan<- ir.Event) error {
	t := newTranslator(c, p.model)
	emit := func(evs []ir.Event) bool {
		for _, e := range evs {
			select {
			case out <- e:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}
	for {
		var l line
		var ok bool
		select {
		case <-ctx.Done():
			p.kill("client went away")
			return ctx.Err()
		case l, ok = <-p.lines:
		}
		if !ok {
			ev := &ir.Error{Kind: ir.ErrUpstream, Message: "the CLI process exited unexpectedly"}
			emit([]ir.Event{{Kind: ir.EvError, Err: ev}})
			return ev
		}
		p.mu.Lock()
		retryAt := p.retryAt
		p.mu.Unlock()
		evs, done, ferr := t.feed(l, retryAt, p.b.o.Now())
		if !emit(evs) {
			p.kill("client went away")
			return ctx.Err()
		}
		if ferr != nil {
			emit([]ir.Event{{Kind: ir.EvError, Err: ferr}})
			if done == doneError {
				// The upstream refused (auth, quota); the process is reusable
				// in principle but its state is uncertain.
				p.kill("upstream error")
			}
			return ferr
		}
		switch done {
		case doneTurn:
			p.mu.Lock()
			p.state, p.lastUsed = stateIdle, p.b.o.Now()
			p.fp = fingerprint(append(sent, t.assistantMessage()))
			p.mu.Unlock()
			return nil
		case doneTools:
			p.mu.Lock()
			p.state, p.lastUsed = stateAwaitingTools, p.b.o.Now()
			p.pending = t.toolIDs
			p.fp = fingerprint(append(sent, t.assistantMessage()))
			p.mu.Unlock()
			return nil
		}
	}
}
