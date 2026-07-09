package logging

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactor_BuiltinShapes(t *testing.T) {
	r := NewRedactor(nil)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "sk key",
			in:   "using key sk-ant-api03-abcdefgh1234567",
			want: "using key [REDACTED]",
		},
		{
			name: "sk key at start of line",
			in:   "sk-proj-abcdefgh12345678 is the key",
			want: "[REDACTED] is the key",
		},
		{
			name: "github personal token ghp_",
			in:   "token: ghp_1234567890ABCDEFghijklmnop",
			want: "token: [REDACTED]",
		},
		{
			name: "github server token ghs_",
			in:   "token: ghs_1234567890ABCDEFghijklmnop",
			want: "token: [REDACTED]",
		},
		{
			name: "github token at end of line",
			in:   "leaked ghp_1234567890ABCDEFghijklmnop",
			want: "leaked [REDACTED]",
		},
		{
			// The Authorization: header rule consumes to end of line
			// (per docs/architecture.md#security-model), so the trailing
			// quote is swallowed along with the credential.
			name: "bearer credential",
			in:   "curl -H \"Authorization: Bearer abcdefgh12345678\"",
			want: "curl -H \"Authorization: [REDACTED]",
		},
		{
			name: "bearer alone lowercase",
			in:   "sending bearer abcdefgh12345678 now",
			want: "sending Bearer [REDACTED] now",
		},
		{
			name: "authorization header embedded in surrounding output",
			in:   "line one\nAuthorization: Basic dXNlcjpwYXNz\nline three",
			want: "line one\nAuthorization: [REDACTED]\nline three",
		},
		{
			name: "multiple secrets in one string",
			in:   "sk-abcdefgh12345678 and ghp_1234567890ABCDEFghijklmnop together",
			want: "[REDACTED] and [REDACTED] together",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, r.Redact(tt.in))
		})
	}
}

func TestRedactor_EnvValues(t *testing.T) {
	environ := []string{
		"MY_API_TOKEN=tok-abc123xyz",
		"AWS_SECRET=deadbeefcafe",
		"DB_KEY=k9y-value",
		"IGNORED_VAR=visible",
	}
	r := NewRedactor(environ)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "token value redacted",
			in:   "auth with tok-abc123xyz please",
			want: "auth with [REDACTED] please",
		},
		{
			name: "secret value redacted",
			in:   "aws secret is deadbeefcafe",
			want: "aws secret is [REDACTED]",
		},
		{
			name: "key value redacted",
			in:   "db key k9y-value in use",
			want: "db key [REDACTED] in use",
		},
		{
			name: "ignored var not covered by suffix rule passes through",
			in:   "visible value stays",
			want: "visible value stays",
		},
		{
			name: "multiple env secrets in one string",
			in:   "tok-abc123xyz and deadbeefcafe and k9y-value",
			want: "[REDACTED] and [REDACTED] and [REDACTED]",
		},
		{
			name: "env secret at start and end of line",
			in:   "tok-abc123xyz middle deadbeefcafe",
			want: "[REDACTED] middle [REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, r.Redact(tt.in))
		})
	}
}

func TestRedactor_EmptyEnvValuesIgnored(t *testing.T) {
	environ := []string{"EMPTY_TOKEN=", "NO_EQUALS_SIGN", "MALFORMED_KEY=", "REAL_SECRET=abc123realvalue"}
	r := NewRedactor(environ)

	// If an empty value were treated as a literal to redact, ReplaceAll
	// would insert "[REDACTED]" between every character of any string.
	// EMPTY_TOKEN/MALFORMED_KEY must be skipped, and NO_EQUALS_SIGN isn't
	// KEY=value shaped so it is skipped outright.
	assert.Equal(t, "plain text here", r.Redact("plain text here"))

	// The one real secret is still redacted normally.
	assert.Equal(t, "token [REDACTED] leaked", r.Redact("token abc123realvalue leaked"))
}

func TestRedactor_EnvValueSubstringOfAnother(t *testing.T) {
	environ := []string{
		"SHORT_KEY=abc",
		"LONG_TOKEN=abcdef",
	}
	r := NewRedactor(environ)

	// Longest value must be redacted first so "abc" (a substring of
	// "abcdef") doesn't leave a "[REDACTED]def" fragment behind.
	got := r.Redact("prefix abcdef middle abc end")
	assert.Equal(t, "prefix [REDACTED] middle [REDACTED] end", got)
}

func TestRedactor_PassThrough(t *testing.T) {
	r := NewRedactor([]string{"MY_TOKEN=supersecretvalue"})

	tests := []string{
		"task-1234",
		"skiing",
		"brisk-pace",
		"gherkin_salad",
		"Please review our authorization policy before proceeding.",
		"an ordinary sentence with no secrets at all",
	}

	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, in, r.Redact(in))
		})
	}
}

func TestRedactor_RedactBytes(t *testing.T) {
	r := NewRedactor(nil)
	got := r.RedactBytes([]byte("token sk-abcdefgh12345678 here"))
	assert.Equal(t, []byte("token [REDACTED] here"), got)
}

func TestRedactor_Writer_LineBuffering(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(nil)
	w := r.Writer(&out)

	// A secret split across two Write calls, with the newline arriving in
	// the second call.
	n1, err := w.Write([]byte("token sk-abcdef"))
	require.NoError(t, err)
	assert.Equal(t, len("token sk-abcdef"), n1)
	assert.Empty(t, out.Bytes(), "nothing should be flushed before a newline")

	n2, err := w.Write([]byte("gh12345678 leaked\n"))
	require.NoError(t, err)
	assert.Equal(t, len("gh12345678 leaked\n"), n2)
	assert.Equal(t, "token [REDACTED] leaked\n", out.String())

	require.NoError(t, w.Close())
}

func TestRedactor_Writer_UnterminatedLineFlushedOnClose(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(nil)
	w := r.Writer(&out)

	_, err := w.Write([]byte("trailing sk-abcdefgh12345678 no newline"))
	require.NoError(t, err)
	assert.Empty(t, out.Bytes(), "unterminated line should not be flushed before Close")

	require.NoError(t, w.Close())
	assert.Equal(t, "trailing [REDACTED] no newline", out.String())
}

func TestRedactor_Writer_MultiLineSingleWrite(t *testing.T) {
	var out bytes.Buffer
	r := NewRedactor(nil)
	w := r.Writer(&out)

	_, err := w.Write([]byte("sk-abcdefgh12345678 first\nghp_1234567890ABCDEFghijklmnop second\nplain third\n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	assert.Equal(t, "[REDACTED] first\n[REDACTED] second\nplain third\n", out.String())
}
