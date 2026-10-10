package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Timeout bounds all webhook deliveries of one action together. A variable so tests can shorten it.
var Timeout = 3 * time.Second

// client does not follow redirects: a 3xx is reported as a failure instead of sending the body on.
var client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Notify sends rec to every webhook that matches it, all at once, and returns when each has
// finished or Timeout has passed. resolve turns a URL template into a URL (it fills in {{NAME}}
// from the vault). The result has one error per failed webhook, in config order. No error
// contains a URL or any text from the server, because the URL may be a secret.
func Notify(hooks []Webhook, rec Record, resolve func(template string) (string, error)) []error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	errs := make([]error, len(hooks))
	var wg sync.WaitGroup
	for i, w := range hooks {
		if !w.matches(rec) {
			continue
		}
		u, err := resolve(w.URL)
		if err != nil {
			errs[i] = err
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = send(ctx, w.Format, u, rec)
		}()
	}
	wg.Wait()
	var out []error
	for i, err := range errs {
		if err != nil {
			out = append(out, fmt.Errorf("webhook #%d: %w", i+1, err))
		}
	}
	return out
}

func send(ctx context.Context, format, target string, rec Record) error {
	if err := checkURL(target); err != nil {
		return err
	}
	body, header := rec.JSON(), http.Header{"Content-Type": {"application/json"}}
	if format == "ntfy" {
		body, header = ntfy(rec)
	}
	header.Set("User-Agent", "agv")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("could not build the request")
	}
	req.Header = header
	resp, err := client.Do(req) // its error text holds the URL: say only what kind of failure it was
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s", Timeout)
		}
		return errors.New("could not connect or send")
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("the server answered HTTP %d", resp.StatusCode)
	}
	return nil
}

// ntfy renders rec for the ntfy app: a short plain-text body and Title, Priority and Tags headers.
// Headers carry printable ASCII only, which every ntfy server and client accepts.
func ntfy(r Record) ([]byte, http.Header) {
	title, prio, tags := "agv "+r.Action, "default", "key,white_check_mark"
	if r.Program != "" {
		title += ": " + r.Program
	}
	if r.Exit != 0 {
		title += fmt.Sprintf(" (exit %d)", r.Exit)
		prio, tags = "high", "key,x"
	}
	secrets := "none"
	if len(r.Secrets) > 0 {
		secrets = strings.Join(r.Secrets, ", ")
	}
	body := fmt.Sprintf("Secrets: %s\nExit %d after %s\nIn: %s", secrets, r.Exit, time.Duration(r.DurationMS)*time.Millisecond, r.Cwd)
	h := http.Header{"Content-Type": {"text/plain; charset=utf-8"}}
	h.Set("Title", ascii(title))
	h.Set("Priority", prio)
	h.Set("Tags", tags)
	return []byte(body), h
}

func ascii(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '?'
		}
		return r
	}, s)
}
