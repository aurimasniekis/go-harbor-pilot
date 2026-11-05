package ratelimit

import (
	"testing"
	"time"
)

func TestPeekDoesNotConsume(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(60, 2, func() time.Time { return now })
	if ok, _ := l.Peek("k"); !ok {
		t.Fatal("unknown key must be allowed")
	}
	for range 5 {
		if ok, _ := l.Peek("k"); !ok {
			t.Fatal("peek consumed tokens")
		}
	}
	l.Allow("k")
	l.Allow("k")
	ok, retry := l.Peek("k")
	if ok || retry <= 0 || retry > time.Second {
		t.Fatalf("drained bucket: ok=%v retry=%s", ok, retry)
	}
	now = now.Add(time.Second)
	if ok, _ := l.Peek("k"); !ok {
		t.Fatal("bucket did not refill")
	}
}
