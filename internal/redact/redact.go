// Package redact replaces secret values in a byte stream with
// [REDACTED:NAME.field], on raw bytes, without delaying output that cannot be
// part of a secret.
package redact

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// MaxValueSize is the largest value that can be redacted. It bounds pattern size.
const MaxValueSize = 1 << 20

// minLine is the shortest line of a multi-line value that is redacted on its own.
const minLine = 8

// Secret is a value labelled NAME.field.
type Secret struct {
	Label string
	Value []byte
}

type pattern struct {
	b    []byte
	mark []byte
}

// Redactor holds the patterns of one run. It is read-only after New, so
// several Writers (one per stream) may share it.
type Redactor struct {
	byFirst [256][]*pattern
}

// New builds the patterns of every secret: the raw value, base64 (standard and
// URL-safe, padded and unpadded), percent-encoded, JSON string-escaped, and
// for multi-line values each line of 8 or more bytes. Empty values are
// skipped. A value over MaxValueSize is an error naming only its label.
func New(secrets []Secret) (*Redactor, error) {
	r := &Redactor{}
	seen := map[string]bool{}
	for _, s := range secrets {
		if len(s.Value) > MaxValueSize {
			return nil, fmt.Errorf("%s is larger than %d bytes and cannot be redacted", s.Label, MaxValueSize)
		}
		mark := []byte("[REDACTED:" + s.Label + "]")
		for _, p := range variants(s.Value) {
			if len(p) == 0 || seen[string(p)] {
				continue
			}
			seen[string(p)] = true
			r.byFirst[p[0]] = append(r.byFirst[p[0]], &pattern{b: p, mark: mark})
		}
	}
	return r, nil
}

func variants(v []byte) [][]byte {
	out := [][]byte{v}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		out = append(out, []byte(enc.EncodeToString(v)))
	}
	out = append(out, percent(v, "", false), percent(v, "", true), percent(v, "!'()*", false))
	for _, escapeHTML := range []bool{false, true} {
		out = append(out, jsonEscape(v, escapeHTML))
	}
	if bytes.IndexByte(v, '\n') >= 0 {
		for _, line := range bytes.Split(v, []byte("\n")) {
			if line = bytes.TrimSuffix(line, []byte("\r")); len(line) >= minLine {
				out = append(out, line)
			}
		}
	}
	return out
}

// percent encodes every byte except RFC 3986 unreserved characters and keep
// as %XX; with plus, a space becomes '+' (form encoding).
func percent(v []byte, keep string, plus bool) []byte {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 0, len(v))
	for _, c := range v {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~', strings.IndexByte(keep, c) >= 0:
			out = append(out, c)
		case c == ' ' && plus:
			out = append(out, '+')
		default:
			out = append(out, '%', hex[c>>4], hex[c&15])
		}
	}
	return out
}

// jsonEscape returns v as the inside of a JSON string.
func jsonEscape(v []byte, escapeHTML bool) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(escapeHTML)
	if enc.Encode(string(v)) != nil {
		return nil
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return b[1 : len(b)-1]
}

// String redacts a whole string, for error messages.
func (r *Redactor) String(s string) string {
	out, _ := r.scan(nil, []byte(s), true)
	return string(out)
}

// scan redacts data into out. It returns the offset where an unfinished
// possible match begins (len(data) if none); the caller keeps data[rest:] and
// scans it again with more bytes. With final set nothing is held back.
func (r *Redactor) scan(out, data []byte, final bool) (_ []byte, rest int) {
	i := 0
	for i < len(data) {
		j := i
		for j < len(data) && r.byFirst[data[j]] == nil {
			j++
		}
		out = append(out, data[i:j]...)
		if i = j; i == len(data) {
			break
		}
		match, partial := r.matchAt(data[i:])
		if partial && !final {
			return out, i
		}
		if match != nil {
			out = append(out, match.mark...)
			i += len(match.b)
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out, len(data)
}

// matchAt returns the longest pattern that data starts with, and whether data
// is a proper prefix of some pattern (so more bytes could still complete one).
func (r *Redactor) matchAt(data []byte) (match *pattern, partial bool) {
	for _, p := range r.byFirst[data[0]] {
		switch {
		case len(data) >= len(p.b):
			if bytes.HasPrefix(data, p.b) && (match == nil || len(p.b) > len(match.b)) {
				match = p
			}
		case bytes.HasPrefix(p.b, data):
			partial = true
		}
	}
	return match, partial
}

// Writer redacts what is written to it and passes the result to the
// underlying writer. Each stream needs its own Writer: the held-back tail is
// per stream. A Writer is not safe for concurrent use.
type Writer struct {
	r    *Redactor
	w    io.Writer
	hold []byte // tail that may still become a pattern
	out  []byte
	err  error
}

// NewWriter returns a Writer on top of w. Close it to flush the held tail.
func (r *Redactor) NewWriter(w io.Writer) *Writer { return &Writer{r: r, w: w} }

// Write redacts p. Bytes that cannot be the start of a pattern are written
// at once; only a tail that is a possible pattern prefix waits for more input.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	data := p
	if len(w.hold) > 0 {
		w.hold = append(w.hold, p...)
		data = w.hold
	}
	var rest int
	w.out, rest = w.r.scan(w.out[:0], data, false)
	w.hold = append(w.hold[:0], data[rest:]...)
	if err := w.flush(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close writes the held tail. It does not close the underlying writer.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	w.out, _ = w.r.scan(w.out[:0], w.hold, true)
	w.hold = w.hold[:0]
	return w.flush()
}

func (w *Writer) flush() error {
	if len(w.out) == 0 {
		return nil
	}
	if _, err := w.w.Write(w.out); err != nil {
		w.err = err
	}
	return w.err
}
