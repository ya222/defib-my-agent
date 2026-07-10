package claude

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib-my-agent/internal/provider"
)

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	assert.Equal(t, "claude", c.Name())
	assert.Equal(t, provider.Capabilities{
		Resume:           true,
		ClientSuppliedID: true,
		Headless:         true,
		Interactive:      true,
		StructuredOutput: true,
	}, c.Capabilities())
}

func TestBuildStart(t *testing.T) {
	cases := []struct {
		name string
		task provider.TaskSpec
		want []string
	}{
		{
			name: "full",
			task: provider.TaskSpec{
				Prompt:      "do the thing",
				SessionRef:  "sess-123",
				Model:       "claude-opus-4",
				Passthrough: []string{"--foo", "bar"},
				ProviderConfig: map[string]any{
					"extra_args": []string{"--add-dir", "/tmp/x"},
				},
			},
			want: []string{
				"claude", "-p", "do the thing",
				"--session-id", "sess-123",
				"--output-format", "stream-json", "--verbose",
				"--model", "claude-opus-4",
				"--add-dir", "/tmp/x",
				"--", "--foo", "bar",
			},
		},
		{
			name: "minimal",
			task: provider.TaskSpec{
				Prompt:     "hello",
				SessionRef: "sess-abc",
			},
			want: []string{
				"claude", "-p", "hello",
				"--session-id", "sess-abc",
				"--output-format", "stream-json", "--verbose",
			},
		},
		{
			name: "custom binary",
			task: provider.TaskSpec{
				Prompt:     "hi",
				SessionRef: "sess-1",
				ProviderConfig: map[string]any{
					"binary": "/opt/claude/claude",
				},
			},
			want: []string{
				"/opt/claude/claude", "-p", "hi",
				"--session-id", "sess-1",
				"--output-format", "stream-json", "--verbose",
			},
		},
		{
			name: "extra_args as []any of strings",
			task: provider.TaskSpec{
				Prompt:     "hi",
				SessionRef: "sess-2",
				ProviderConfig: map[string]any{
					"extra_args": []any{"--add-dir", "/tmp/y"},
				},
			},
			want: []string{
				"claude", "-p", "hi",
				"--session-id", "sess-2",
				"--output-format", "stream-json", "--verbose",
				"--add-dir", "/tmp/y",
			},
		},
		{
			name: "empty SessionRef omits --session-id",
			task: provider.TaskSpec{
				Prompt: "hi",
			},
			want: []string{
				"claude", "-p", "hi",
				"--output-format", "stream-json", "--verbose",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			cmd, err := c.BuildStart(tc.task)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cmd.Argv)
			assert.Nil(t, cmd.Env)
		})
	}
}

func TestBuildResume(t *testing.T) {
	t.Run("default resume prompt", func(t *testing.T) {
		c := New()
		cmd, err := c.BuildResume(provider.TaskSpec{}, "sess-1")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"claude", "-p", defaultResumePrompt,
			"--resume", "sess-1",
			"--output-format", "stream-json", "--verbose",
		}, cmd.Argv)
	})

	t.Run("custom resume_prompt", func(t *testing.T) {
		c := New()
		task := provider.TaskSpec{
			ProviderConfig: map[string]any{
				"resume_prompt": "Pick up where you left off.",
			},
		}
		cmd, err := c.BuildResume(task, "sess-2")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"claude", "-p", "Pick up where you left off.",
			"--resume", "sess-2",
			"--output-format", "stream-json", "--verbose",
		}, cmd.Argv)
	})

	t.Run("model", func(t *testing.T) {
		c := New()
		task := provider.TaskSpec{Model: "claude-sonnet-4"}
		cmd, err := c.BuildResume(task, "sess-3")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"claude", "-p", defaultResumePrompt,
			"--resume", "sess-3",
			"--output-format", "stream-json", "--verbose",
			"--model", "claude-sonnet-4",
		}, cmd.Argv)
	})

	t.Run("error on empty sessionRef", func(t *testing.T) {
		c := New()
		_, err := c.BuildResume(provider.TaskSpec{}, "")
		assert.Error(t, err)
	})
}

func TestExtractSessionRef(t *testing.T) {
	t.Run("real success fixture", func(t *testing.T) {
		data, err := os.ReadFile("../../../testdata/claude/success.stream-json.stdout.log")
		require.NoError(t, err)
		c := New()
		id, ok := c.ExtractSessionRef(provider.AttemptOutput{Stdout: data})
		assert.True(t, ok)
		assert.Equal(t, "d4a41016-bdd6-4707-ae43-fafc872621ae", id)
	})

	t.Run("real auth-error fixture", func(t *testing.T) {
		data, err := os.ReadFile("../../../testdata/claude/auth-error.stdout.log")
		require.NoError(t, err)
		c := New()
		id, ok := c.ExtractSessionRef(provider.AttemptOutput{Stdout: data})
		assert.True(t, ok)
		assert.Equal(t, "8b2090dc-e393-492b-aeda-2a7a51ab14e6", id)
	})

	t.Run("empty output", func(t *testing.T) {
		c := New()
		id, ok := c.ExtractSessionRef(provider.AttemptOutput{})
		assert.False(t, ok)
		assert.Empty(t, id)
	})

	t.Run("only non-init lines", func(t *testing.T) {
		c := New()
		out := provider.AttemptOutput{Stdout: []byte(
			`{"type":"assistant","message":{"role":"assistant"}}` + "\n" +
				`{"type":"system","subtype":"thinking_tokens","estimated_tokens":8}` + "\n",
		)}
		id, ok := c.ExtractSessionRef(out)
		assert.False(t, ok)
		assert.Empty(t, id)
	})

	t.Run("garbage non-JSON", func(t *testing.T) {
		c := New()
		out := provider.AttemptOutput{Stdout: []byte("not json at all\nnor is this{{{\n")}
		id, ok := c.ExtractSessionRef(out)
		assert.False(t, ok)
		assert.Empty(t, id)
	})
}

func TestCheckAvailability(t *testing.T) {
	c := New()
	avail, err := c.CheckAvailability(context.Background(), provider.TaskSpec{})
	require.NoError(t, err)
	assert.Equal(t, provider.Availability{State: provider.Unsupported}, avail)
}
