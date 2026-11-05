// Package codex executes requests through the vendor `codex` CLI
// (`codex exec --json`) with a per-account CODEX_HOME holding that
// account's ChatGPT sign-in.
//
// The CLI runs one turn per process. Client tools are exposed through a
// per-process loopback MCP endpoint; when the model calls one, the response
// ends with the tool call and the process stays parked on the blocked MCP
// call until the client sends the result. A follow-up turn of a
// conversation the bridge has already served continues the CLI thread with
// `codex exec resume`. A conversation seen for the first time is replayed
// to the CLI as text (an approximate loss).
//
// The CLI's JSON event stream and flags are documented by the vendor; this
// bridge is contract-tested against a fake binary until a live run.
package codex

import (
	"bufio"
	"context"
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/bridge"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

// Options configure the bridge.
type Options struct {
	Binary   string
	Root     string
	ExtraEnv []string
	// MaxPerAccount defaults to 1: the CLI refreshes the sign-in file in
	// place, so concurrent processes of one account could race.
	MaxPerAccount int
	MaxProcesses  int
	ToolTimeout   time.Duration
	// ThreadTTL bounds how long completed threads stay resumable.
	ThreadTTL time.Duration
	Log       *slog.Logger
	Now       func() time.Time
}

// Bridge implements executor.Executor for codex providers.
type Bridge struct {
	o   Options
	mcp *bridge.MCPServer

	mu      sync.Mutex
	running map[string]*run       // live processes by id
	threads map[string]threadInfo // conversation fingerprint -> thread
	stop    chan struct{}
	once    sync.Once
}

type threadInfo struct {
	id, accountID, model, key string
	at                        time.Time
}

// New creates the bridge.
func New(o Options) *Bridge {
	if o.Binary == "" {
		o.Binary = "codex"
	}
	if o.MaxPerAccount <= 0 {
		o.MaxPerAccount = 1
	}
	if o.MaxProcesses <= 0 {
		o.MaxProcesses = 8
	}
	if o.ToolTimeout <= 0 {
		o.ToolTimeout = 30 * time.Minute
	}
	if o.ThreadTTL <= 0 {
		o.ThreadTTL = 24 * time.Hour
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	b := &Bridge{o: o, mcp: bridge.NewMCPServer(), running: map[string]*run{}, threads: map[string]threadInfo{}, stop: make(chan struct{})}
	go b.reaper()
	return b
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
		var dead []*run
		for _, r := range b.running {
			r.mu.Lock()
			if r.parked && now.Sub(r.parkedAt) > b.o.ToolTimeout {
				dead = append(dead, r)
			}
			r.mu.Unlock()
		}
		for k, th := range b.threads {
			if now.Sub(th.at) > b.o.ThreadTTL {
				delete(b.threads, k)
			}
		}
		b.mu.Unlock()
		for _, r := range dead {
			r.kill()
		}
	}
}

// Close ends every process.
func (b *Bridge) Close() {
	b.once.Do(func() { close(b.stop) })
	b.mu.Lock()
	rs := make([]*run, 0, len(b.running))
	for _, r := range b.running {
		rs = append(rs, r)
	}
	b.mu.Unlock()
	for _, r := range rs {
		r.kill()
	}
	b.mcp.Close()
}

// Version runs `codex --version`.
func (b *Bridge) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, b.o.Binary, "--version").Output() //nolint:gosec // operator-configured binary
	if err != nil {
		return "", fmt.Errorf("codex --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------- conversation model ----------

type turnMsg struct {
	role  string
	text  string
	calls []callRef   // assistant tool calls
	res   []resultRef // user tool results
}

type callRef struct{ id, name, args string }
type resultRef struct {
	id      string
	text    string
	isError bool
}

func flatten(m ir.Message) turnMsg {
	t := turnMsg{role: string(m.Role)}
	var b strings.Builder
	for _, p := range m.Parts {
		switch p.Kind {
		case ir.PartText:
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		case ir.PartToolCall:
			if p.ToolCall != nil {
				t.calls = append(t.calls, callRef{p.ToolCall.ID, p.ToolCall.Name, p.ToolCall.Arguments})
			}
		case ir.PartToolResult:
			if p.ToolResult != nil {
				var rb strings.Builder
				for _, c := range p.ToolResult.Content {
					if c.Kind == ir.PartText {
						rb.WriteString(c.Text)
					}
				}
				t.res = append(t.res, resultRef{p.ToolResult.CallID, rb.String(), p.ToolResult.IsError})
			}
		}
	}
	t.text = b.String()
	if m.Role == ir.RoleTool {
		t.role = "user"
	}
	return t
}

func fingerprint(msgs []turnMsg) string {
	h := sha256.New()
	for _, m := range msgs {
		fmt.Fprintf(h, "\x00%s\x00%s\x01", m.role, strings.TrimSpace(m.text))
		for _, c := range m.calls {
			fmt.Fprintf(h, "c:%s\x01", c.id)
		}
		for _, r := range m.res {
			fmt.Fprintf(h, "r:%s\x01", r.id)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// render replays history as text for a CLI thread that has not seen it.
func render(history []turnMsg, last turnMsg) string {
	var b strings.Builder
	if len(history) > 0 {
		b.WriteString("Conversation so far (replayed by the gateway):\n\n")
		for _, m := range history {
			fmt.Fprintf(&b, "[%s]\n", m.role)
			if m.text != "" {
				b.WriteString(m.text + "\n")
			}
			for _, c := range m.calls {
				fmt.Fprintf(&b, "(called tool %s with %s)\n", c.name, c.args)
			}
			for _, r := range m.res {
				fmt.Fprintf(&b, "(tool result: %s)\n", r.text)
			}
			b.WriteString("\n")
		}
		b.WriteString("[user]\n")
	}
	b.WriteString(last.text)
	for _, r := range last.res {
		fmt.Fprintf(&b, "\n(tool result: %s)", r.text)
	}
	return b.String()
}

var mcpNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func mcpName(n string) string {
	if mcpNameRE.MatchString(n) {
		return n
	}
	h := sha256.Sum256([]byte(n))
	return "t_" + hex.EncodeToString(h[:6])
}

// ---------- process ----------

type event struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     json.RawMessage `json:"item"`
	Usage    *struct {
		InputTokens       *int64 `json:"input_tokens"`
		CachedInputTokens *int64 `json:"cached_input_tokens"`
		OutputTokens      *int64 `json:"output_tokens"`
	} `json:"usage"`
	Error   *struct{ Message string } `json:"error"`
	Message string                    `json:"message"`
}

type item struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type run struct {
	b         *Bridge
	id        string
	accountID string
	model     string
	key       string // model + system + tools
	cmd       *exec.Cmd
	sess      *bridge.Session
	events    chan event
	work      string
	nameMap   map[string]string // mcp name -> client name

	mu       sync.Mutex
	threadID string
	fp       string            // fingerprint when parked on tool calls
	pending  []string          // client-visible tool call ids (item ids) in order
	keyOf    map[string]string // item id -> MCP arrival key
	parked   bool
	parkedAt time.Time
	calls    int // client tool calls started over the process lifetime
	started  time.Time
	dead     bool
}

func (r *run) kill() {
	r.mu.Lock()
	if r.dead {
		r.mu.Unlock()
		return
	}
	r.dead = true
	r.mu.Unlock()
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.sess != nil {
		r.sess.Close()
	}
	r.b.mu.Lock()
	delete(r.b.running, r.id)
	r.b.mu.Unlock()
	go func() {
		_ = r.cmd.Wait()
		_ = os.RemoveAll(r.work)
	}()
}

var unsafePath = regexp.MustCompile(`[^A-Za-z0-9]`)

func tomlString(s string) string {
	b, _ := json.Marshal(s) // JSON string escaping is valid TOML basic-string escaping for these inputs
	return string(b)
}

func (b *Bridge) start(accountID string, cred credentials.Secret, model, effort, system string, tools []ir.Tool, resume, prompt string) (*run, error) {
	b.mu.Lock()
	per := 0
	for _, r := range b.running {
		if r.accountID == accountID {
			per++
		}
	}
	if per >= b.o.MaxPerAccount || len(b.running) >= b.o.MaxProcesses {
		b.mu.Unlock()
		return nil, errBusy
	}
	r := &run{b: b, id: strconv.FormatInt(time.Now().UnixNano(), 36), accountID: accountID, model: model, events: make(chan event, 256),
		nameMap: map[string]string{}, keyOf: map[string]string{}, started: b.o.Now()}
	b.running[r.id] = r
	b.mu.Unlock()
	fail := func(err error) (*run, error) {
		b.mu.Lock()
		delete(b.running, r.id)
		b.mu.Unlock()
		return nil, err
	}
	home := filepath.Join(b.o.Root, "accounts", unsafePath.ReplaceAllString(accountID, "_"))
	codexHome := filepath.Join(home, "codex_home")
	for _, d := range []string{codexHome, filepath.Join(home, "home"), filepath.Join(b.o.Root, "w")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fail(err)
		}
	}
	// The account's sign-in file is seeded from the stored credential once;
	// afterwards the CLI refreshes it in place. It is never shared.
	authFile := filepath.Join(codexHome, "auth.json")
	if _, err := os.Stat(authFile); errors.Is(err, os.ErrNotExist) && !cred.IsZero() {
		if err := os.WriteFile(authFile, []byte(cred.Reveal()), 0o600); err != nil {
			return fail(err)
		}
	}
	work, err := os.MkdirTemp(filepath.Join(b.o.Root, "w"), "c")
	if err != nil {
		return fail(err)
	}
	r.work = work
	args := []string{"exec"}
	if resume != "" {
		args = append(args, "resume", resume)
	}
	args = append(args, "--json", "--skip-git-repo-check", "--sandbox", "read-only", "--color", "never", "-m", model)
	if system != "" {
		args = append(args, "-c", "developer_instructions="+tomlString(system))
	}
	if effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+tomlString(effort))
	}
	var mtools []bridge.MCPTool
	for _, t := range tools {
		if t.Kind == "builtin" {
			continue
		}
		mn := mcpName(t.Name)
		r.nameMap[mn] = t.Name
		schema := t.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		mtools = append(mtools, bridge.MCPTool{Name: mn, Description: t.Description, InputSchema: schema})
	}
	if len(mtools) > 0 {
		sess, err := b.mcp.NewSession(mtools)
		if err != nil {
			return fail(err)
		}
		r.sess = sess
		args = append(args, "-c", "experimental_use_rmcp_client=true", "-c", "mcp_servers.client.url="+tomlString(sess.URL()),
			"-c", "mcp_servers.client.tool_timeout_sec="+strconv.Itoa(int(b.o.ToolTimeout.Seconds())+60))
	}
	args = append(args, "-")
	cmd := exec.Command(b.o.Binary, args...) //nolint:gosec // operator-configured binary, arguments are not shell-interpreted
	cmd.Dir = work
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(home, "home"), "CODEX_HOME=" + codexHome,
		"NO_COLOR=1", "RUST_LOG=error"}, b.o.ExtraEnv...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	r.cmd = cmd
	if err := cmd.Start(); err != nil {
		if r.sess != nil {
			r.sess.Close()
		}
		_ = os.RemoveAll(work)
		return fail(fmt.Errorf("start codex: %w", err))
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64<<10), 32<<20)
		for sc.Scan() {
			var ev event
			if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type != "" {
				r.events <- ev
			}
		}
		close(r.events)
	}()
	return r, nil
}

var errBusy = errors.New("the account's CLI bridge process is busy")

// ---------- execute ----------

func requestKey(model, system string, tools []ir.Tool) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name+"|"+string(t.Parameters))
	}
	sort.Strings(names)
	h := sha256.Sum256([]byte(model + "\x00" + system + "\x00" + strings.Join(names, "\x00")))
	return hex.EncodeToString(h[:])
}

func systemText(r *ir.Request) string {
	var parts []string
	for _, p := range r.System {
		if p.Kind == ir.PartText {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Execute implements executor.Executor.
func (b *Bridge) Execute(ctx context.Context, t executor.Target, cred credentials.Secret, req executor.Request, stream bool) (*executor.Result, *executor.AttemptError) {
	r := req.IR
	if len(r.Messages) == 0 || r.Messages[len(r.Messages)-1].Role == ir.RoleAssistant {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInvalidRequest, Message: "the last message must be from the user"}}
	}
	msgs := make([]turnMsg, 0, len(r.Messages))
	for _, m := range r.Messages {
		if m.Role == ir.RoleSystem || m.Role == ir.RoleDeveloper {
			continue
		}
		msgs = append(msgs, flatten(m))
	}
	// Tool results may arrive as separate tool-role messages; merge the
	// trailing ones into the final user turn.
	lastIdx := len(msgs) - 1
	for lastIdx > 0 && msgs[lastIdx-1].role == "user" && len(msgs[lastIdx-1].res) > 0 && len(msgs[lastIdx].res) > 0 {
		msgs[lastIdx-1].res = append(msgs[lastIdx-1].res, msgs[lastIdx].res...)
		msgs = msgs[:lastIdx]
		lastIdx--
	}
	history, last := msgs[:lastIdx], msgs[lastIdx]
	system := systemText(r)
	key := requestKey(t.Model, system, r.Tools)
	histFP := fingerprint(history)
	losses := []ir.Loss{{Severity: ir.LossApproximate, Feature: "system_prompt", Detail: "the system prompt is passed as developer instructions after the CLI's own instructions"}}
	if r.Temperature != nil || r.TopP != nil || len(r.Stop) > 0 || r.MaxOutputTokens != nil {
		losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "sampling", Detail: "sampling parameters, stop sequences and output limits are not passed to the CLI"})
	}
	effort := ""
	if rc := r.Reasoning; rc != nil {
		switch rc.Effort {
		case "minimal", "low", "medium", "high", "xhigh":
			effort = rc.Effort
		}
	}

	var ru *run
	// 1. A parked process waiting for exactly these tool results.
	if len(last.res) > 0 {
		ru = b.claimParked(t.Account.ID, key, histFP, last.res)
		if ru != nil {
			for _, res := range last.res {
				ru.mu.Lock()
				k := ru.keyOf[res.id]
				ru.mu.Unlock()
				ru.sess.Reply(k, bridge.ToolReply{Content: []bridge.MCPContent{{Type: "text", Text: res.text}}, IsError: res.isError})
			}
			ru.mu.Lock()
			ru.pending = nil
			ru.mu.Unlock()
		}
	}
	if ru == nil {
		resume := ""
		prompt := last.text
		b.mu.Lock()
		if th, ok := b.threads[histFP]; ok && th.accountID == t.Account.ID && th.key == key && b.o.Now().Sub(th.at) < b.o.ThreadTTL && len(last.res) == 0 {
			resume = th.id
		}
		b.mu.Unlock()
		if resume == "" && (len(history) > 0 || len(last.res) > 0) {
			prompt = render(history, last)
			losses = append(losses, ir.Loss{Severity: ir.LossApproximate, Feature: "history_replay", Detail: "earlier turns were replayed to the CLI as text"})
		}
		nr, err := b.start(t.Account.ID, cred, t.Model, effort, system, r.Tools, resume, prompt)
		if errors.Is(err, errBusy) {
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrOverloaded, Message: err.Error()}}
		}
		if err != nil {
			b.o.Log.Error("bridge.codex.start_failed", "account_id", t.Account.ID, "error", err.Error())
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInternal, Message: "the CLI bridge could not start"}}
		}
		nr.key = key
		ru = nr
	}
	events := make(chan ir.Event, 64)
	actx, cancel := context.WithCancel(ctx)
	res := &executor.Result{Upstream: ir.CodexBridge, Mode: executor.ModeTranslated, Losses: losses, Status: 200, FirstByteAt: time.Now(), Cancel: cancel}
	sent := append(append([]turnMsg{}, history...), last)
	var serr error
	go func() {
		defer close(events)
		serr = ru.relay(actx, sent, events)
	}()
	res.Err = func() error { return serr }
	if !stream {
		resp, err := ir.Collect(events)
		cancel()
		if err != nil {
			var ie *ir.Error
			if errors.As(err, &ie) {
				return nil, &executor.AttemptError{Err: ie, AccountScoped: ie.Kind == ir.ErrAuthentication || ie.Kind == ir.ErrRateLimited || ie.Kind == ir.ErrQuotaExhausted}
			}
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUpstream, Message: "the CLI bridge failed"}, Executed: true}
		}
		res.Response = resp
	} else {
		res.Stream = events
	}
	return res, nil
}

func (b *Bridge) claimParked(accountID, key, fp string, results []resultRef) *run {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.running {
		r.mu.Lock()
		ok := r.parked && r.accountID == accountID && r.key == key && r.fp == fp && len(r.pending) == len(results)
		if ok {
			have := map[string]bool{}
			for _, x := range results {
				have[x.id] = true
			}
			for _, id := range r.pending {
				ok = ok && have[id]
			}
		}
		if ok {
			r.parked = false
			r.mu.Unlock()
			return r
		}
		// keep pending ids until the replies are sent
		r.mu.Unlock()
	}
	return nil
}

func classify(msg string) ir.ErrorKind {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "401") || strings.Contains(m, "unauthorized") || strings.Contains(m, "not logged in") || strings.Contains(m, "login"):
		return ir.ErrAuthentication
	case strings.Contains(m, "usage limit") || strings.Contains(m, "429") || strings.Contains(m, "rate limit"):
		return ir.ErrRateLimited
	case strings.Contains(m, "context") && strings.Contains(m, "window"):
		return ir.ErrContextLength
	}
	return ir.ErrUpstream
}

// relay turns CLI events into one client turn.
func (r *run) relay(ctx context.Context, sent []turnMsg, out chan<- ir.Event) error {
	idx := 0
	emit := func(evs ...ir.Event) bool {
		for _, e := range evs {
			select {
			case out <- e:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}
	if !emit(ir.Event{Kind: ir.EvStart, Model: r.model}) {
		r.kill()
		return ctx.Err()
	}
	var assistant turnMsg
	assistant.role = "assistant"
	started := 0
	var arrived <-chan struct{}
	if r.sess != nil {
		arrived = r.sess.Arrived()
	}
	internalTool := false
	for {
		select {
		case <-ctx.Done():
			r.kill()
			return ctx.Err()
		case <-arrived:
			if r.parkIfReady(sent, assistant, started) {
				return r.finishTools(emit)
			}
			continue
		case ev, ok := <-r.events:
			if !ok {
				e := &ir.Error{Kind: ir.ErrUpstream, Message: "the CLI exited before completing the turn"}
				emit(ir.Event{Kind: ir.EvError, Err: e})
				r.kill()
				return e
			}
			switch ev.Type {
			case "thread.started":
				r.mu.Lock()
				r.threadID = ev.ThreadID
				r.mu.Unlock()
			case "item.started", "item.completed":
				var it item
				_ = json.Unmarshal(ev.Item, &it)
				switch {
				case it.Type == "agent_message" && ev.Type == "item.completed":
					assistant.text += it.Text
					if !emit(ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &ir.Part{Kind: ir.PartText}}, ir.Event{Kind: ir.EvDelta, Index: idx, Delta: it.Text},
						ir.Event{Kind: ir.EvPartStop, Index: idx}) {
						r.kill()
						return ctx.Err()
					}
					idx++
				case it.Type == "reasoning" && ev.Type == "item.completed" && it.Text != "":
					if !emit(ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &ir.Part{Kind: ir.PartReasoning, Reasoning: &ir.Reasoning{Summary: true}}},
						ir.Event{Kind: ir.EvDelta, Index: idx, Delta: it.Text}, ir.Event{Kind: ir.EvPartStop, Index: idx}) {
						r.kill()
						return ctx.Err()
					}
					idx++
				case it.Type == "mcp_tool_call" && it.Server == "client" && ev.Type == "item.started":
					name := r.nameMap[it.Tool]
					if name == "" {
						name = it.Tool
					}
					args := string(it.Arguments)
					if args == "" || args == "null" {
						args = "{}"
					}
					started++
					r.mu.Lock()
					r.calls++
					r.pending = append(r.pending, it.ID)
					r.keyOf[it.ID] = bridge.ArrivalKey(r.calls)
					r.mu.Unlock()
					assistant.calls = append(assistant.calls, callRef{it.ID, name, args})
					if !emit(ir.Event{Kind: ir.EvPartStart, Index: idx, Part: &ir.Part{Kind: ir.PartToolCall, ToolCall: &ir.ToolCall{ID: it.ID, Name: name, Kind: "function"}}},
						ir.Event{Kind: ir.EvDelta, Index: idx, Delta: args}, ir.Event{Kind: ir.EvPartStop, Index: idx}) {
						r.kill()
						return ctx.Err()
					}
					idx++
					if r.parkIfReady(sent, assistant, started) {
						return r.finishTools(emit)
					}
				case it.Type == "command_execution" || it.Type == "file_change" || it.Type == "web_search":
					internalTool = true
				}
			case "turn.completed":
				u := ir.Usage{InputIncludesCache: true}
				if ev.Usage != nil {
					u.InputTokens, u.CacheReadTokens, u.OutputTokens = ev.Usage.InputTokens, ev.Usage.CachedInputTokens, ev.Usage.OutputTokens
				}
				evs := []ir.Event{{Kind: ir.EvUsage, Usage: &u}, {Kind: ir.EvStop, StopReason: ir.StopEndTurn}}
				if internalTool {
					r.b.o.Log.Info("bridge.codex.internal_tool", "account_id", r.accountID)
				}
				emit(evs...)
				r.mu.Lock()
				tid := r.threadID
				r.mu.Unlock()
				if tid != "" {
					r.b.mu.Lock()
					r.b.threads[fingerprint(append(sent, assistant))] = threadInfo{id: tid, accountID: r.accountID, model: r.model, key: r.key, at: r.b.o.Now()}
					r.b.mu.Unlock()
				}
				r.kill()
				return nil
			case "turn.failed", "error":
				msg := ev.Message
				if ev.Error != nil {
					msg = ev.Error.Message
				}
				if msg == "" {
					msg = "the CLI reported an error"
				}
				e := &ir.Error{Kind: classify(msg), Message: msg}
				emit(ir.Event{Kind: ir.EvError, Err: e})
				r.kill()
				return e
			}
		}
	}
}

// parkIfReady reports whether every started client tool call has reached
// the MCP endpoint, so the turn can be handed to the client.
func (r *run) parkIfReady(sent []turnMsg, assistant turnMsg, started int) bool {
	if started == 0 || r.sess == nil || len(r.sess.Calls()) < started {
		return false
	}
	r.mu.Lock()
	r.parked, r.parkedAt = true, r.b.o.Now()
	r.fp = fingerprint(append(sent, assistant))
	r.mu.Unlock()
	return true
}

func (r *run) finishTools(emit func(...ir.Event) bool) error {
	emit(ir.Event{Kind: ir.EvStop, StopReason: ir.StopToolUse})
	return nil
}
