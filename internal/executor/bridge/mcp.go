// Package bridge contains what CLI bridges share: a loopback MCP server
// that exposes a client's tools to a CLI process and parks tool calls until
// the client returns results in its next request.
package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MCPTool is a tool exposed to the CLI.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolCall is a call the CLI made to a client tool.
type ToolCall struct {
	// ToolUseID is the model's tool_use id (from the call metadata).
	ToolUseID string
	Name      string
	Arguments json.RawMessage
	reply     chan ToolReply
}

// ToolReply is the client's result for a tool call.
type ToolReply struct {
	Content []MCPContent
	IsError bool
}

// MCPContent is one MCP content item.
type MCPContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Session is one CLI process's MCP endpoint.
type Session struct {
	srv   *MCPServer
	token string
	tools []MCPTool

	mu      sync.Mutex
	calls   map[string]*ToolCall // by tool_use id (or arrival key)
	replies map[string]ToolReply // results that arrived before the call
	seq     int                  // arrival counter for calls without an id
	arrived chan struct{}        // signalled when a call arrives
	closed  bool
	done    chan struct{}
}

// MCPServer serves MCP over streamable HTTP on loopback. Each session has
// an unguessable path token; nothing else is reachable.
type MCPServer struct {
	mu       sync.Mutex
	ln       net.Listener
	hs       *http.Server
	sessions map[string]*Session
}

// NewMCPServer creates an idle server; it listens on first use.
func NewMCPServer() *MCPServer { return &MCPServer{sessions: map[string]*Session{}} }

func (m *MCPServer) start() error {
	if m.ln != nil {
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	m.ln = ln
	m.hs = &http.Server{Handler: http.HandlerFunc(m.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = m.hs.Serve(ln) }()
	return nil
}

// Close stops the server and releases parked calls.
func (m *MCPServer) Close() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*Session{}
	hs := m.hs
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
	if hs != nil {
		_ = hs.Close()
	}
}

// NewSession registers tools for one CLI process.
func (m *MCPServer) NewSession(tools []MCPTool) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.start(); err != nil {
		return nil, err
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	s := &Session{srv: m, token: hex.EncodeToString(b), tools: tools, calls: map[string]*ToolCall{}, replies: map[string]ToolReply{},
		arrived: make(chan struct{}, 64), done: make(chan struct{})}
	m.sessions[s.token] = s
	return s, nil
}

// URL is the session endpoint for the CLI's MCP configuration.
func (s *Session) URL() string { return "http://" + s.srv.ln.Addr().String() + "/mcp/" + s.token }

// Close releases parked calls and unregisters the session.
func (s *Session) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	s.mu.Unlock()
	s.srv.mu.Lock()
	delete(s.srv.sessions, s.token)
	s.srv.mu.Unlock()
}

// Reply answers a tool call by tool_use id. When the call has not arrived
// yet the reply is kept until it does.
func (s *Session) Reply(toolUseID string, r ToolReply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.calls[toolUseID]; ok {
		delete(s.calls, toolUseID)
		c.reply <- r
		return
	}
	s.replies[toolUseID] = r
}

// Pending returns the ids of calls waiting for a reply.
func (s *Session) Pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for id := range s.calls {
		out = append(out, id)
	}
	return out
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, rerr *rpcError) {
	body := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		body["error"] = rerr
	} else {
		body["result"] = result
	}
	b, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (m *MCPServer) serve(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.URL.Path, "/mcp/")
	m.mu.Lock()
	s := m.sessions[token]
	m.mu.Unlock()
	if !ok || s == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		// No server-initiated stream.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req rpcReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(req.ID) == 0 { // notification
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		writeRPC(w, req.ID, map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "client", "version": "1"}}, nil)
	case "ping":
		writeRPC(w, req.ID, map[string]any{}, nil)
	case "tools/list":
		writeRPC(w, req.ID, map[string]any{"tools": s.tools}, nil)
	case "tools/call":
		s.call(w, r.Context(), req)
	default:
		writeRPC(w, req.ID, nil, &rpcError{Code: -32601, Message: "method not found"})
	}
}

func (s *Session) call(w http.ResponseWriter, ctx context.Context, req rpcReq) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      map[string]any  `json:"_meta"`
	}
	_ = json.Unmarshal(req.Params, &p)
	id, _ := p.Meta["claudecode/toolUseId"].(string)
	c := &ToolCall{ToolUseID: id, Name: p.Name, Arguments: p.Arguments, reply: make(chan ToolReply, 1)}
	s.mu.Lock()
	if id == "" {
		// Clients that do not tag calls are matched in arrival order.
		s.seq++
		id = ArrivalKey(s.seq)
		c.ToolUseID = id
	}
	if s.closed {
		s.mu.Unlock()
		writeRPC(w, req.ID, nil, &rpcError{Code: -32000, Message: "session closed"})
		return
	}
	if r, ok := s.replies[id]; ok && id != "" {
		delete(s.replies, id)
		c.reply <- r
	} else {
		s.calls[id] = c
	}
	s.mu.Unlock()
	select {
	case s.arrived <- struct{}{}:
	default:
	}
	select {
	case r := <-c.reply:
		writeRPC(w, req.ID, map[string]any{"content": r.Content, "isError": r.IsError}, nil)
	case <-s.done:
		writeRPC(w, req.ID, nil, &rpcError{Code: -32000, Message: "session closed"})
	case <-ctx.Done():
	}
}

// ErrNoSession is returned for unknown sessions.
var ErrNoSession = errors.New("bridge: unknown MCP session")

// ArrivalKey names the n-th (1-based) untagged tool call of a session.
func ArrivalKey(n int) string { return "arrival:" + strconv.Itoa(n) }

// Calls returns the parked calls (for clients that do not tag calls).
func (s *Session) Calls() []ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ToolCall, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, ToolCall{ToolUseID: c.ToolUseID, Name: c.Name, Arguments: c.Arguments})
	}
	return out
}

// Arrived signals when a tool call reaches the session.
func (s *Session) Arrived() <-chan struct{} { return s.arrived }
