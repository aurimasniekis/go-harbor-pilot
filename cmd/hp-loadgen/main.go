// Command hp-loadgen sends concurrent requests to a Harbor Pilot gateway and
// prints latency percentiles. The gateway token is read from a file or the
// environment, never from arguments.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/loadgen"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("hp-loadgen", flag.ContinueOnError)
	c := loadgen.Config{}
	var tokenFile, tokenEnv string
	var asJSON bool
	fs.StringVar(&c.BaseURL, "url", "http://127.0.0.1:8080", "gateway base URL")
	fs.StringVar(&tokenFile, "token-file", "", "file holding the gateway token")
	fs.StringVar(&tokenEnv, "token-env", "HP_GATEWAY_TOKEN", "environment variable holding the gateway token")
	fs.StringVar(&c.Model, "model", "coding/default", "requested model")
	fs.IntVar(&c.Concurrency, "concurrency", 32, "parallel clients")
	fs.IntVar(&c.Requests, "requests", 500, "total requests")
	fs.BoolVar(&c.Stream, "stream", true, "use streaming responses")
	fs.DurationVar(&c.ReadDelay, "read-delay", 0, "pause after each received line (simulates slow clients)")
	fs.IntVar(&c.MaxTokens, "max-tokens", 64, "max_tokens per request")
	fs.BoolVar(&asJSON, "json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case tokenFile != "":
		b, err := os.ReadFile(tokenFile) //nolint:gosec // operator-chosen token file
		if err != nil {
			return err
		}
		c.Token = strings.TrimSpace(string(b))
	default:
		c.Token = strings.TrimSpace(os.Getenv(tokenEnv))
	}
	if c.Token == "" {
		return errors.New("no gateway token: use --token-file or set " + tokenEnv)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	t0 := time.Now()
	rep := loadgen.Run(ctx, c)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	fmt.Println(rep)
	if rep.OK < rep.Requests {
		return fmt.Errorf("%d of %d requests failed after %s", rep.Requests-rep.OK, rep.Requests, time.Since(t0).Round(time.Millisecond))
	}
	return nil
}
