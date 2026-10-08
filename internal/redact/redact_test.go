package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// run feeds input to a new Writer in chunks of the given size and returns what came out.
func run(t *testing.T, r *Redactor, input string, chunk int) string {
	t.Helper()
	var out bytes.Buffer
	w := r.NewWriter(&out)
	for i := 0; i < len(input); i += chunk {
		end := min(i+chunk, len(input))
		if n, err := w.Write([]byte(input[i:end])); err != nil || n != end-i {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestEveryEncodingIsRedactedAcrossChunkBoundaries(t *testing.T) {
	const v = "??>s3cr/t+val ue!*\"\\<>&\xc3\xa9"
	// Independent oracles from the standard library (and one literal for encodeURIComponent).
	jsonHTML, _ := json.Marshal(v)
	var plain bytes.Buffer
	enc := json.NewEncoder(&plain)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	jsonPlain := strings.TrimSuffix(plain.String(), "\n")

	forms := map[string]string{
		"raw":            v,
		"base64 std":     base64.StdEncoding.EncodeToString([]byte(v)),
		"base64 url":     base64.URLEncoding.EncodeToString([]byte(v)),
		"base64 std raw": base64.RawStdEncoding.EncodeToString([]byte(v)),
		"base64 url raw": base64.RawURLEncoding.EncodeToString([]byte(v)),
		"percent":        strings.ReplaceAll(url.QueryEscape(v), "+", "%20"),
		"percent form":   url.QueryEscape(v),
		"percent uri":    "%3F%3F%3Es3cr%2Ft%2Bval%20ue!*%22%5C%3C%3E%26%C3%A9",
		"json":           jsonPlain[1 : len(jsonPlain)-1],
		"json html":      string(jsonHTML[1 : len(jsonHTML)-1]),
	}
	seen := map[string]string{}
	for name, f := range forms {
		if prev, dup := seen[f]; dup {
			t.Fatalf("test value does not tell %q and %q apart", name, prev)
		}
		seen[f] = name
	}
	forms["multi-line line"] = "abcdefgh12345"

	r, err := New([]Secret{{"TOK.value", []byte(v)}, {"PEM.key", []byte("-----BEGIN KEY-----\r\nabcdefgh12345\r\nshort\r\n-----END KEY-----\n")}})
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range forms {
		label := "TOK.value"
		if name == "multi-line line" {
			label = "PEM.key"
		}
		input := "before " + f + " after"
		want := "before [REDACTED:" + label + "] after"
		for chunk := 1; chunk <= len(input); chunk++ {
			if got := run(t, r, input, chunk); got != want {
				t.Fatalf("%s, chunks of %d: got %q, want %q", name, chunk, got, want)
			}
		}
	}

	// Lines shorter than 8 bytes are not patterns on their own.
	if got := run(t, r, "short\r\n", 3); got != "short\r\n" {
		t.Fatalf("short line was redacted: %q", got)
	}
}

func TestOnlyAPossiblePatternPrefixIsHeldBack(t *testing.T) {
	r, _ := New([]Secret{{"A.value", []byte("hunter2-secret")}})
	var out bytes.Buffer
	w := r.NewWriter(&out)

	w.Write([]byte("Password: "))
	if out.String() != "Password: " {
		t.Fatalf("prompt delayed: %q", out.String())
	}
	w.Write([]byte("working... hunt"))
	if out.String() != "Password: working... " {
		t.Fatalf("held more than the possible prefix: %q", out.String())
	}
	w.Write([]byte("ing\n"))
	if out.String() != "Password: working... hunting\n" {
		t.Fatalf("prefix not released once it cannot match: %q", out.String())
	}
	w.Write([]byte("hunter2"))
	w.Close()
	if out.String() != "Password: working... hunting\nhunter2" {
		t.Fatalf("Close did not flush the held tail: %q", out.String())
	}
}

func TestStreamsKeepSeparateState(t *testing.T) {
	r, _ := New([]Secret{{"A.value", []byte("xyz-secret")}})
	var o, e bytes.Buffer
	ow, ew := r.NewWriter(&o), r.NewWriter(&e)
	ow.Write([]byte("xyz-"))
	ew.Write([]byte("secret"))
	ow.Close()
	ew.Close()
	if o.String() != "xyz-" || e.String() != "secret" {
		t.Fatalf("stdout %q stderr %q: one stream completed the other's pattern", o.String(), e.String())
	}
}

func TestLongerPatternWinsWhileItCanStillComplete(t *testing.T) {
	r, _ := New([]Secret{{"A.value", []byte("abcabc")}, {"B.value", []byte("abcabcdef")}})
	if got := run(t, r, "abcabcdef!", 1); got != "[REDACTED:B.value]!" {
		t.Fatalf("got %q", got)
	}
	if got := run(t, r, "abcabc!", 1); got != "[REDACTED:A.value]!" {
		t.Fatalf("got %q", got)
	}
}

func TestValueCap(t *testing.T) {
	big := bytes.Repeat([]byte("a"), MaxValueSize+1)
	_, err := New([]Secret{{"BIG.value", big}})
	if err == nil || !strings.Contains(err.Error(), "BIG.value") || strings.Contains(err.Error(), "aaaa") {
		t.Fatalf("want an error naming BIG.value only, got %v", err)
	}
	if _, err := New([]Secret{{"BIG.value", big[:MaxValueSize]}}); err != nil {
		t.Fatalf("value of exactly the cap rejected: %v", err)
	}
}
