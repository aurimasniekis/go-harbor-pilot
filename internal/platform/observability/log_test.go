package observability

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
)

func TestLoggerRedactsSecrets(t *testing.T) {
	const (
		anth = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz012345"
		tg   = "123456789:AAH-abcdefghijklmnopqrstuvwxyz12345"
	)
	tests := []struct {
		name  string
		log   func(l *slog.Logger)
		leaks []string
	}{
		{name: "sensitive key", log: func(l *slog.Logger) { l.Info("x", "api_key", anth) }, leaks: []string{anth}},
		{name: "authorization header", log: func(l *slog.Logger) { l.Info("x", "header", "Bearer "+anth) }, leaks: []string{anth}},
		{name: "secret type", log: func(l *slog.Logger) { l.Info("x", "value", credentials.NewSecret("hunter2-very-secret")) }, leaks: []string{"hunter2"}},
		{name: "telegram url in error", log: func(l *slog.Logger) {
			l.Error("x", "error", errors.New("post https://api.telegram.org/bot"+tg+"/sendMessage: timeout"))
		}, leaks: []string{tg, "AAH-abcdef"}},
		{name: "query string key", log: func(l *slog.Logger) { l.Info("x", "url", "https://h/x?api_key=supersecretvalue1&a=b") }, leaks: []string{"supersecretvalue1"}},
		{name: "nexos key", log: func(l *slog.Logger) { l.Info("x", "msg2", "used team-AbCdEfGhIjKlMnOpQrStUv") }, leaks: []string{"team-AbCdEfGhIjKlMnOpQrStUv"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.log(NewLogger(&buf, slog.LevelDebug))
			out := buf.String()
			for _, leak := range tt.leaks {
				if strings.Contains(out, leak) {
					t.Fatalf("log leaked %q: %s", leak, out)
				}
			}
			if !strings.Contains(out, Redacted) {
				t.Fatalf("expected redaction marker: %s", out)
			}
		})
	}
}

func TestLoggerKeepsNumericTokenCounts(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf, slog.LevelInfo).Info("x", "token_count", 42)
	if !strings.Contains(buf.String(), `"token_count":42`) {
		t.Fatalf("numeric token count should not be redacted: %s", buf.String())
	}
}
