package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/executor/bridge"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// Which CLI session served a turn (recorded as ledger provenance).
const (
	pathLive       = "live_process"    // a parked process continued
	pathShelved    = "shelved_session" // a new process resumed a shelved session
	pathTranscript = "transcript"      // a new process resumed a transcript rebuilt from the request
	pathNew        = "new_session"     // the conversation's first turn
)

// Shelf defaults.
const (
	defaultShelfPerAccount = 16
	defaultShelfBytes      = 256 << 20
)

// shelfEntry is the CLI's own session file of a conversation whose process
// ended between turns. A new process resumes it with --resume, so the
// prompt prefix is the one the CLI sent before (byte for byte) and the
// upstream prompt cache is read rather than written again. Entries live at
// most the conversation's cache TTL (D-045).
type shelfEntry struct {
	key       string
	accountID string
	sid       string
	// pending lists the tool calls the conversation waits for; empty when
	// it ended after a complete reply.
	pending []string
	path    string
	size    int64
	created time.Time
	expires time.Time
}

// shelvable reports whether a process that ended for reason may keep its
// session: idle and tool timeouts, eviction and a crash between turns. A
// client that went away, an upstream error or shutdown discard it.
func shelvable(reason string) bool {
	switch reason {
	case "idle timeout", "evicted", "process exited":
		return true
	}
	return false
}

func cacheTTLDuration(ttl string) time.Duration {
	if ttl == "5m" {
		return 5 * time.Minute
	}
	return time.Hour
}

// shelfKey identifies the conversation the next request will continue, in
// the same terms as claim: account, client scope, model, cache TTL, system
// prompt, tools and the conversation fingerprint.
func shelfKey(accountID, scope, model string, c conv, sysKey, toolsKey, fp string) string {
	return hashOf(accountID, scope, model, c.CacheTTL, c.Effort, strconv.Itoa(c.MaxOut), sysKey, toolsKey, fp)
}

// shelfKey must be called with p.mu held.
func (p *proc) shelfKey() string {
	return shelfKey(p.accountID, p.scope, p.model, conv{CacheTTL: p.cacheTTL, Effort: p.effort, MaxOut: p.maxOut}, p.sysKey, p.toolsKey, p.fp)
}

func (b *Bridge) shelfDir(accountID string) string {
	return filepath.Join(b.o.Root, "accounts", safeName(accountID), "shelf")
}

// shelve moves an ended process's session file onto the shelf. Done under
// b.mu so the sweeper never sees an unindexed shelf file of a live entry.
func (b *Bridge) shelve(e *shelfEntry, src string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || !b.o.Now().Before(e.expires) {
		return
	}
	st, err := os.Stat(src)
	if err != nil || st.Size() == 0 || !sessionHolds(src, e.pending) {
		return
	}
	dir := b.shelfDir(e.accountID)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	dst := filepath.Join(dir, randID(8)+".jsonl")
	if os.Rename(src, dst) != nil {
		return
	}
	_ = os.Chmod(dst, 0o600)
	e.path, e.size, e.created = dst, st.Size(), b.o.Now()
	if old := b.shelf[e.key]; old != nil {
		_ = os.Remove(old.path)
	}
	b.shelf[e.key] = e
	b.trimShelf(e.accountID)
}

// trimShelf evicts the oldest entries beyond the per-account count and the
// total size. Called with b.mu held.
func (b *Bridge) trimShelf(accountID string) {
	all := make([]*shelfEntry, 0, len(b.shelf))
	var total int64
	count := 0
	for _, e := range b.shelf {
		all = append(all, e)
		total += e.size
		if e.accountID == accountID {
			count++
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].created.Before(all[j].created) })
	for _, e := range all {
		overBytes := total > b.o.ShelfMaxBytes
		overCount := e.accountID == accountID && count > b.o.ShelfMaxPerAccount
		if !overBytes && !overCount {
			continue
		}
		b.removeShelved(e)
		total -= e.size
		if e.accountID == accountID {
			count--
		}
	}
}

// removeShelved deletes an entry and its file. Called with b.mu held.
func (b *Bridge) removeShelved(e *shelfEntry) {
	if b.shelf[e.key] == e {
		delete(b.shelf, e.key)
	}
	_ = os.Remove(e.path)
}

// unshelve takes the shelved session that continues this conversation, if
// any: the same key, not expired, and for a conversation waiting on tool
// calls, results for all of them. The entry leaves the shelf; its file is
// moved by spawn (or put back with reshelve, or dropped).
func (b *Bridge) unshelve(accountID, scope, model string, c *conv, histFP string, results map[string]bridge.ToolReply) *shelfEntry {
	key := shelfKey(accountID, scope, model, conv{CacheTTL: c.CacheTTL, Effort: c.Effort, MaxOut: c.MaxOut}, hashOf(c.System), toolsKey(c.Tools), histFP)
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.shelf[key]
	if e == nil {
		return nil
	}
	if !b.o.Now().Before(e.expires) {
		b.removeShelved(e)
		return nil
	}
	if len(e.pending) > 0 {
		for _, id := range e.pending {
			if _, ok := results[id]; !ok {
				return nil
			}
		}
	} else if len(results) > 0 {
		return nil
	}
	delete(b.shelf, key)
	return e
}

// reshelve puts back an entry that could not be used (no free process
// slot) unless a newer one took its place.
func (b *Bridge) reshelve(e *shelfEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.shelf[e.key] != nil {
		_ = os.Remove(e.path)
		return
	}
	b.shelf[e.key] = e
}

func (b *Bridge) dropShelved(e *shelfEntry) { _ = os.Remove(e.path) }

// sweepShelf removes expired entries, entries of accounts no longer kept
// (keep nil: all accounts kept) and shelf files no entry indexes (left by a
// restart: the index lives in memory, so a restart drops the shelf).
func (b *Bridge) sweepShelf(now time.Time, keep map[string]bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	indexed := map[string]bool{}
	for _, e := range b.shelf {
		if !now.Before(e.expires) || (keep != nil && !keep[safeName(e.accountID)]) {
			b.removeShelved(e)
			continue
		}
		indexed[e.path] = true
	}
	dirs, _ := filepath.Glob(filepath.Join(b.o.Root, "accounts", "*", "shelf"))
	for _, d := range dirs {
		files, _ := os.ReadDir(d)
		for _, f := range files {
			if p := filepath.Join(d, f.Name()); !indexed[p] {
				_ = os.RemoveAll(p)
			}
		}
	}
}

// clearShelf removes every shelved session (shutdown).
func (b *Bridge) clearShelf() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.sweepShelf(time.Time{}, map[string]bool{})
}

// appendToolResults adds the client's tool results to a shelved session
// that ended waiting for them, as the user entry the CLI itself writes
// after a tool call (chained to the session's last entry). Without it, a
// resumed CLI marks the calls as interrupted and the results are lost.
func appendToolResults(path string, content json.RawMessage, now time.Time) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var last map[string]any
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0 && last == nil; i-- {
		var e map[string]any
		if json.Unmarshal(lines[i], &e) == nil {
			if _, ok := e["uuid"].(string); ok {
				last = e
			}
		}
	}
	if last == nil {
		return errors.New("the shelved session has no entries")
	}
	var blocks []any
	if err := json.Unmarshal(content, &blocks); err != nil {
		return err
	}
	e := map[string]any{"parentUuid": last["uuid"], "isSidechain": false, "type": "user", "uuid": newUUID(),
		"message": map[string]any{"role": "user", "content": blocks}, "toolUseResult": blocks, "sourceToolAssistantUUID": last["uuid"],
		"timestamp": now.UTC().Format("2006-01-02T15:04:05.000Z")}
	for _, k := range []string{"userType", "entrypoint", "cwd", "sessionId", "version"} {
		if v, ok := last[k]; ok {
			e[k] = v
		}
	}
	line, err := wire.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		line = append([]byte("\n"), line...)
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// sessionHolds reports whether a session file already records every
// pending tool call (the CLI writes it shortly after streaming the call).
func sessionHolds(path string, ids []string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, id := range ids {
		if !bytes.Contains(raw, []byte(`"id":"`+id+`"`)) {
			return false
		}
	}
	return true
}
