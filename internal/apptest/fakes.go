package apptest

import (
	"net/http/httptest"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/synthetic"
)

// Reply scripts one upstream response.
type Reply = synthetic.Reply

// Upstream is a synthetic provider served by an httptest server.
type Upstream struct {
	*synthetic.Upstream
	Srv *httptest.Server
}

// NewUpstream starts a synthetic upstream of kind anthropic | openai | gemini.
func NewUpstream(t testing.TB, kind string) *Upstream {
	u := &Upstream{Upstream: synthetic.New(kind)}
	u.Srv = httptest.NewServer(u.Upstream)
	t.Cleanup(u.Srv.Close)
	return u
}
