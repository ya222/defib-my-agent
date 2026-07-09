package logging

import (
	"bytes"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Built-in secret shapes. These are best-effort heuristics for the token
// formats defib is likely to see in provider output and its own logs; they
// are not a substitute for not logging secrets in the first place.
var (
	reSKKey       = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	reGitHubToken = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`)
	reBearer      = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	reAuthHeader  = regexp.MustCompile(`(?im)\bAuthorization\s*:\s*\S.*?$`)
)

// Redactor replaces secret-shaped substrings with "[REDACTED]". It is
// best-effort: it covers a fixed set of known token shapes (sk-..., ghp_...,
// Bearer ..., Authorization: headers) plus the literal values of
// environment variables named like *_TOKEN/*_KEY/*_SECRET. It cannot catch
// secrets in shapes it doesn't know about, so callers must still avoid
// logging secrets directly wherever possible.
type Redactor struct {
	// envValues holds the literal secret values pulled from environ, sorted
	// longest-first so a value that is a substring of another doesn't leave
	// a fragment behind after the longer one is redacted.
	envValues []string
}

// NewRedactor builds a Redactor covering built-in secret shapes plus the
// values of environ entries ("KEY=value" form, e.g. from os.Environ())
// whose names end in _TOKEN, _KEY, or _SECRET (case-insensitive). Empty
// values are ignored.
func NewRedactor(environ []string) *Redactor {
	seen := make(map[string]struct{}, len(environ))
	var values []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		upper := strings.ToUpper(name)
		if !strings.HasSuffix(upper, "_TOKEN") && !strings.HasSuffix(upper, "_KEY") && !strings.HasSuffix(upper, "_SECRET") {
			continue
		}
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })

	return &Redactor{envValues: values}
}

// Redact returns s with every secret occurrence replaced by "[REDACTED]".
func (r *Redactor) Redact(s string) string {
	for _, v := range r.envValues {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
	}
	s = reSKKey.ReplaceAllString(s, "[REDACTED]")
	s = reGitHubToken.ReplaceAllString(s, "[REDACTED]")
	s = reBearer.ReplaceAllString(s, "Bearer [REDACTED]")
	s = reAuthHeader.ReplaceAllString(s, "Authorization: [REDACTED]")
	return s
}

// RedactBytes is the []byte counterpart (used by the process capture path).
func (r *Redactor) RedactBytes(b []byte) []byte {
	return []byte(r.Redact(string(b)))
}

// Writer wraps w so that written data is redacted line-by-line: bytes are
// buffered until a newline, redacted, then forwarded. Close flushes any
// unterminated final line. (Needed by M3-T2 to stream child output through
// the redactor into attempt log files.)
func (r *Redactor) Writer(w io.Writer) io.WriteCloser {
	return &redactWriter{r: r, w: w}
}

// redactWriter is the io.WriteCloser returned by Redactor.Writer.
type redactWriter struct {
	r   *Redactor
	w   io.Writer
	buf []byte
}

// Write buffers p and forwards each complete (newline-terminated) line
// through the redactor as it becomes available. It always reports the full
// length of p consumed, since input is retained in buf even if the
// underlying write fails.
func (rw *redactWriter) Write(p []byte) (int, error) {
	rw.buf = append(rw.buf, p...)
	for {
		i := bytes.IndexByte(rw.buf, '\n')
		if i < 0 {
			break
		}
		line := rw.buf[:i+1]
		rw.buf = rw.buf[i+1:]
		if _, err := rw.w.Write(rw.r.RedactBytes(line)); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

// Close flushes any buffered, unterminated final line through the redactor.
func (rw *redactWriter) Close() error {
	if len(rw.buf) == 0 {
		return nil
	}
	final := rw.buf
	rw.buf = nil
	_, err := rw.w.Write(rw.r.RedactBytes(final))
	return err
}
