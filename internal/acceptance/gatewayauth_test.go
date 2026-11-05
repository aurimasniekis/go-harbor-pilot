package acceptance_test

import (
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/apptest"
)

// Failed gateway credential checks are limited per address.
func TestGatewayAuthFailuresAreRateLimited(t *testing.T) {
	e := apptest.New(t)
	body := map[string]any{"model": "x", "max_tokens": 8, "messages": []any{map[string]any{"role": "user", "content": "x"}}}
	forged := "hpk_aaaaaaaa_0000000000000000000000000000000000000000000"
	statuses := map[int]int{}
	for range 80 {
		r := e.Gateway("POST", "/v1/messages", forged, body)
		_ = apptest.ReadAll(r)
		statuses[r.StatusCode]++
		if r.StatusCode == 429 && r.Header.Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
	}
	if statuses[401] == 0 || statuses[429] == 0 || statuses[401] > 61 {
		t.Fatalf("statuses %v: expected about 60 rejections, then throttling", statuses)
	}
}
