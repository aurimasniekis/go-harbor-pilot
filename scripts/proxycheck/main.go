// Command proxycheck tests a reverse proxy in front of Harbor Pilot.
//
// In backend mode it listens where Harbor Pilot would and answers the way the
// gateway does (the same streaming headers, keepalive comments and body
// limit), with slow replies that expose buffering and short timeouts. The
// client side then talks to the proxy and checks what a coding tool needs:
//
//	proxycheck -backend 127.0.0.1:18080 -url https://localhost:18443
//
// In app mode it signs in to a real Harbor Pilot (the demo) through the proxy
// and signs out again, which needs the session cookie and the Origin check
// against public_url to work:
//
//	proxycheck -app -url https://localhost:18443
//
// Certificates are not verified: the script uses a throwaway self-signed one.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/demo"
)

const maxBody = 32 << 20 // the gateway's request limit

func main() {
	backend := flag.String("backend", "", "listen address for the stand-in backend")
	target := flag.String("url", "", "the proxy's URL")
	app := flag.Bool("app", false, "sign in to a real Harbor Pilot through the proxy")
	quick := flag.Bool("quick", false, "skip the slow checks (long idle stream, slow reply)")
	flag.Parse()
	if *target == "" {
		fmt.Fprintln(os.Stderr, "usage: proxycheck -backend ADDR -url URL | -app -url URL")
		os.Exit(64)
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // throwaway test certificate
		DisableCompression: true,                                  // we send Accept-Encoding ourselves and read raw bytes
		ForceAttemptHTTP2:  true,
	}}
	var checks []check
	if *app {
		checks = appChecks(client, strings.TrimRight(*target, "/"))
	} else {
		if *backend == "" {
			fmt.Fprintln(os.Stderr, "-backend is required without -app")
			os.Exit(64)
		}
		go serve(*backend)
		checks = proxyChecks(client, strings.TrimRight(*target, "/"), *quick)
	}
	if err := waitHealthy(client, strings.TrimRight(*target, "/")); err != nil {
		fmt.Println("FAIL ", err)
		os.Exit(1)
	}
	os.Exit(run(checks))
}

// waitHealthy waits until the backend answers through the proxy: a proxy with
// health checks (HAProxy) marks a server up only after a few probes.
func waitHealthy(c *http.Client, base string) error {
	var last string
	for range 60 {
		res, err := c.Get(base + "/healthz")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
			last = res.Status
		} else {
			last = err.Error()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("backend not reachable through the proxy after 30s: %s", last)
}

type check struct {
	name string
	fn   func() error
}

func run(checks []check) int {
	errs := make([]error, len(checks))
	durs := make([]time.Duration, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Go(func() {
			start := time.Now()
			errs[i] = c.fn()
			durs[i] = time.Since(start).Round(100 * time.Millisecond)
		})
	}
	wg.Wait()
	failed := 0
	for i, c := range checks {
		if errs[i] != nil {
			failed++
			fmt.Printf("FAIL  %-34s %8s  %v\n", c.name, durs[i], errs[i])
		} else {
			fmt.Printf("ok    %-34s %8s\n", c.name, durs[i])
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// ---- stand-in backend ----

func serve(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"status":"ok"}`) })
	// Five events one second apart.
	mux.HandleFunc("POST /check/stream", func(w http.ResponseWriter, r *http.Request) {
		f := sseHeaders(w)
		for i := range 5 {
			fmt.Fprintf(w, "event: delta\ndata: {\"i\":%d,\"t\":%d}\n\n", i, time.Now().UnixMilli())
			f.Flush()
			if i < 4 {
				time.Sleep(time.Second)
			}
		}
	})
	// One event, then only keepalive comments every 15s (the gateway's
	// interval) for 75s, then the end: a long tool-free think.
	mux.HandleFunc("POST /check/idle", func(w http.ResponseWriter, r *http.Request) {
		f := sseHeaders(w)
		_, _ = io.WriteString(w, "event: start\ndata: {}\n\n")
		f.Flush()
		for range 5 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(15 * time.Second):
			}
			_, _ = io.WriteString(w, ": keepalive\n\n")
			f.Flush()
		}
		_, _ = io.WriteString(w, "event: stop\ndata: {}\n\n")
	})
	// A reply that is not streamed: nothing at all for two minutes.
	mux.HandleFunc("POST /check/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Minute):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"done":true}`)
	})
	mux.HandleFunc("POST /check/body", func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		fmt.Fprintf(w, `{"bytes":%d}`, n)
	})
	mux.HandleFunc("GET /check/headers", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"x_forwarded_for": strings.Join(r.Header.Values("X-Forwarded-For"), ","),
			"remote_addr":     r.RemoteAddr,
		})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "backend:", err)
		os.Exit(1)
	}
}

// sseHeaders sends the headers the gateway sends on a stream.
func sseHeaders(w http.ResponseWriter) http.Flusher {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f := w.(http.Flusher)
	f.Flush()
	return f
}

// ---- client checks ----

func proxyChecks(c *http.Client, base string, quick bool) []check {
	post := func(path string, body io.Reader) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodPost, base+path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd") // what real tools send
		return c.Do(req)
	}
	checks := []check{
		{"health check passes through", func() error {
			res, err := c.Get(base + "/healthz")
			if err != nil {
				return err
			}
			defer res.Body.Close()
			return wantStatus(res, http.StatusOK)
		}},
		{"stream arrives event by event", func() error {
			start := time.Now()
			res, err := post("/check/stream", strings.NewReader("{}"))
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if err := wantStatus(res, http.StatusOK); err != nil {
				return err
			}
			body, err := decoded(res)
			if err != nil {
				return err
			}
			var arrivals []time.Duration
			sc := bufio.NewScanner(body)
			for sc.Scan() {
				if strings.HasPrefix(sc.Text(), "data:") {
					arrivals = append(arrivals, time.Since(start))
				}
			}
			if len(arrivals) != 5 {
				return fmt.Errorf("got %d of 5 events", len(arrivals))
			}
			if arrivals[0] > 1500*time.Millisecond {
				return fmt.Errorf("first event after %s: the proxy buffers the stream", arrivals[0].Round(time.Millisecond))
			}
			if spread := arrivals[4] - arrivals[0]; spread < 3*time.Second {
				return fmt.Errorf("events arrived together (spread %s): the proxy buffers the stream", spread.Round(time.Millisecond))
			}
			return nil
		}},
		{"20 MiB request is accepted", func() error {
			res, err := post("/check/body", bytes.NewReader(bytes.Repeat([]byte("a"), 20<<20)))
			if err != nil {
				return err
			}
			defer res.Body.Close()
			return wantStatus(res, http.StatusOK)
		}},
		{"client address is forwarded", func() error {
			res, err := c.Get(base + "/check/headers")
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if err := wantStatus(res, http.StatusOK); err != nil {
				return err
			}
			var got map[string]string
			if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
				return err
			}
			if got["x_forwarded_for"] == "" {
				return errors.New("no X-Forwarded-For header reached the backend")
			}
			return nil
		}},
	}
	if quick {
		return checks
	}
	return append(checks,
		check{"75s stream kept open by keepalives", func() error {
			res, err := post("/check/idle", strings.NewReader("{}"))
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if err := wantStatus(res, http.StatusOK); err != nil {
				return err
			}
			body, err := decoded(res)
			if err != nil {
				return err
			}
			b, err := io.ReadAll(body)
			if err != nil {
				return fmt.Errorf("stream cut off: %w", err)
			}
			if !bytes.Contains(b, []byte("event: stop")) {
				return errors.New("stream ended before its last event: a proxy timeout closed it")
			}
			return nil
		}},
		check{"unstreamed reply after 2 minutes", func() error {
			res, err := post("/check/slow", strings.NewReader("{}"))
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if err := wantStatus(res, http.StatusOK); err != nil {
				return fmt.Errorf("%w: the proxy gave up waiting for the reply", err)
			}
			return nil
		}},
	)
}

func appChecks(c *http.Client, base string) []check {
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
	do := func(method, path string, body any, csrf string) (*http.Response, error) {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, base+path, r)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Origin", base) // a browser on the public URL
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		return c.Do(req)
	}
	return []check{
		{"web UI loads", func() error {
			res, err := c.Get(base + "/login")
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if err := wantStatus(res, http.StatusOK); err != nil {
				return err
			}
			b, _ := io.ReadAll(res.Body)
			if !bytes.Contains(b, []byte(`<div id="root">`)) {
				return errors.New("response is not the Harbor Pilot web UI")
			}
			return nil
		}},
		{"sign in and out through the proxy", func() error {
			res, err := do(http.MethodPost, "/api/v1/session",
				map[string]string{"email": demo.AdminEmail, "password": demo.AdminPassword}, "")
			if err != nil {
				return err
			}
			_ = res.Body.Close()
			if err := wantStatus(res, http.StatusOK); err != nil {
				return fmt.Errorf("sign-in: %w", err)
			}
			secure := false
			for _, ck := range res.Cookies() {
				secure = secure || ck.Secure
			}
			if !secure {
				return errors.New("session cookie is not Secure: is public_url the https address?")
			}
			res, err = do(http.MethodGet, "/api/v1/session", nil, "")
			if err != nil {
				return err
			}
			var s struct {
				CSRF string `json:"csrf_token"`
			}
			err = json.NewDecoder(res.Body).Decode(&s)
			_ = res.Body.Close()
			if err != nil || s.CSRF == "" {
				return errors.New("session did not survive the proxy (no CSRF token)")
			}
			res, err = do(http.MethodDelete, "/api/v1/session", nil, s.CSRF)
			if err != nil {
				return err
			}
			_ = res.Body.Close()
			if err := wantStatus(res, http.StatusOK); err != nil {
				return fmt.Errorf("sign-out (Origin and CSRF check): %w", err)
			}
			return nil
		}},
	}
}

// decoded returns the response body, decompressing gzip so that timing is
// measured on what a tool would see. Other encodings can't be checked here.
func decoded(res *http.Response) (io.Reader, error) {
	switch enc := res.Header.Get("Content-Encoding"); enc {
	case "", "identity":
		return res.Body, nil
	case "gzip":
		return gzip.NewReader(res.Body)
	default:
		return nil, fmt.Errorf("stream compressed with %s, which this check can't decode", enc)
	}
}

func wantStatus(res *http.Response, want int) error {
	if res.StatusCode != want {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}
