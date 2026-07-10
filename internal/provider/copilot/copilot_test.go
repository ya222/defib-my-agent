package copilot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib-my-agent/internal/detect"
	"github.com/ya222/defib-my-agent/internal/provider"
)

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	assert.Equal(t, "copilot", c.Name())
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
				Model:       "gpt-5",
				Passthrough: []string{"--foo", "bar"},
				ProviderConfig: map[string]any{
					"unattended": true,
					"extra_args": []string{"--add-dir", "/tmp/x"},
				},
			},
			want: []string{
				"copilot", "-p", "do the thing",
				"--session-id", "sess-123",
				"--output-format", "json",
				"--model", "gpt-5",
				"--allow-all-tools",
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
				"copilot", "-p", "hello",
				"--session-id", "sess-abc",
				"--output-format", "json",
			},
		},
		{
			name: "custom binary",
			task: provider.TaskSpec{
				Prompt:     "hi",
				SessionRef: "sess-1",
				ProviderConfig: map[string]any{
					"binary": "/opt/copilot/copilot",
				},
			},
			want: []string{
				"/opt/copilot/copilot", "-p", "hi",
				"--session-id", "sess-1",
				"--output-format", "json",
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
				"copilot", "-p", "hi",
				"--session-id", "sess-2",
				"--output-format", "json",
				"--add-dir", "/tmp/y",
			},
		},
		{
			name: "empty SessionRef omits --session-id",
			task: provider.TaskSpec{
				Prompt: "hi",
			},
			want: []string{
				"copilot", "-p", "hi",
				"--output-format", "json",
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
			"copilot", "-p", defaultResumePrompt,
			"--session-id", "sess-1",
			"--output-format", "json",
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
			"copilot", "-p", "Pick up where you left off.",
			"--session-id", "sess-2",
			"--output-format", "json",
		}, cmd.Argv)
	})

	t.Run("model", func(t *testing.T) {
		c := New()
		task := provider.TaskSpec{Model: "gpt-5"}
		cmd, err := c.BuildResume(task, "sess-3")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"copilot", "-p", defaultResumePrompt,
			"--session-id", "sess-3",
			"--output-format", "json",
			"--model", "gpt-5",
		}, cmd.Argv)
	})

	t.Run("unattended adds --allow-all-tools", func(t *testing.T) {
		c := New()
		task := provider.TaskSpec{
			ProviderConfig: map[string]any{
				"unattended": true,
			},
		}
		cmd, err := c.BuildResume(task, "sess-4")
		require.NoError(t, err)
		assert.Equal(t, []string{
			"copilot", "-p", defaultResumePrompt,
			"--session-id", "sess-4",
			"--output-format", "json",
			"--allow-all-tools",
		}, cmd.Argv)
	})

	t.Run("error on empty sessionRef", func(t *testing.T) {
		c := New()
		_, err := c.BuildResume(provider.TaskSpec{}, "")
		assert.Error(t, err)
	})
}

func TestExtractSessionRef(t *testing.T) {
	t.Run("with stdout", func(t *testing.T) {
		c := New()
		out := provider.AttemptOutput{Stdout: []byte(`{"type":"result","session_id":"abc-123"}` + "\n")}
		id, ok := c.ExtractSessionRef(out)
		assert.False(t, ok)
		assert.Empty(t, id)
	})

	t.Run("empty output", func(t *testing.T) {
		c := New()
		id, ok := c.ExtractSessionRef(provider.AttemptOutput{})
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

func TestDetectionRules(t *testing.T) {
	c := New()
	rules := c.DetectionRules()

	// The set compiles and the low-priority success fallback is present and last.
	_, err := detect.NewEngine(rules)
	require.NoError(t, err)
	last := rules[len(rules)-1]
	assert.Equal(t, "copilot.success", last.Name)
	assert.Equal(t, detect.CategorySuccess, last.Category)
	assert.Equal(t, []int{0}, last.Match.ExitCodeIn)
}
