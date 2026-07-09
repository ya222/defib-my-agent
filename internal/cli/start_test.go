package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePrompt(t *testing.T) {
	promptFilePath := filepath.Join(t.TempDir(), "prompt.txt")
	require.NoError(t, os.WriteFile(promptFilePath, []byte("file contents\n"), 0o644))

	tests := []struct {
		name          string
		prompt        string
		promptSet     bool
		promptFile    string
		promptFileSet bool
		stdin         string
		want          string
		wantErr       string
	}{
		{
			name:      "plain prompt",
			prompt:    "do the thing",
			promptSet: true,
			want:      "do the thing",
		},
		{
			name:      "neither flag set",
			promptSet: false,
			want:      "",
		},
		{
			name:          "prompt file from disk",
			promptFile:    promptFilePath,
			promptFileSet: true,
			want:          "file contents\n",
		},
		{
			name:          "prompt file from stdin",
			promptFile:    "-",
			promptFileSet: true,
			stdin:         "stdin contents",
			want:          "stdin contents",
		},
		{
			name:          "mutually exclusive",
			prompt:        "a",
			promptSet:     true,
			promptFile:    "b",
			promptFileSet: true,
			wantErr:       "mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePrompt(tt.prompt, tt.promptSet, tt.promptFile, tt.promptFileSet, strings.NewReader(tt.stdin))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				var ue usageError
				assert.True(t, errors.As(err, &ue), "mutual-exclusion error should be a usageError")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPassthroughArgs(t *testing.T) {
	var got []string
	cmd := &cobra.Command{
		Use:  "start",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			got = passthroughArgs(c, args)
			return nil
		},
	}
	cmd.Flags().StringP("prompt", "p", "", "")

	t.Run("no dash", func(t *testing.T) {
		got = nil
		cmd.SetArgs([]string{"--prompt", "hi"})
		require.NoError(t, cmd.Execute())
		assert.Nil(t, got)
	})

	t.Run("passthrough after dash", func(t *testing.T) {
		got = nil
		cmd.SetArgs([]string{"--prompt", "hi", "--", "echo", "--verbose", "hello"})
		require.NoError(t, cmd.Execute())
		assert.Equal(t, []string{"echo", "--verbose", "hello"}, got)
	})

	t.Run("empty passthrough", func(t *testing.T) {
		got = nil
		cmd.SetArgs([]string{"--prompt", "hi", "--"})
		require.NoError(t, cmd.Execute())
		assert.Equal(t, []string{}, got)
	})
}

func TestBuildCreateParams(t *testing.T) {
	noProvider := func() (string, error) {
		t.Fatal("resolveDefaultProvider should not be called")
		return "", nil
	}

	t.Run("minimal, no overrides", func(t *testing.T) {
		params, err := buildCreateParams(startFlags{Cwd: "/work", Session: "new"}, "do it", nil, noProvider)
		require.NoError(t, err)
		assert.Equal(t, createParams{Cwd: "/work", Prompt: "do it", SessionMode: "new"}, params)
	})

	t.Run("existing session maps session_ref", func(t *testing.T) {
		params, err := buildCreateParams(startFlags{Cwd: "/work", Session: "sess-123"}, "", nil, noProvider)
		require.NoError(t, err)
		assert.Equal(t, "existing", params.SessionMode)
		assert.Equal(t, "sess-123", params.SessionRef)
	})

	t.Run("passthrough args carried through", func(t *testing.T) {
		params, err := buildCreateParams(startFlags{Cwd: "/work", Session: "new"}, "", []string{"echo", "hi"}, noProvider)
		require.NoError(t, err)
		assert.Equal(t, []string{"echo", "hi"}, params.Args)
	})

	t.Run("max-attempts and deadline overrides, provider not needed", func(t *testing.T) {
		params, err := buildCreateParams(startFlags{
			Cwd: "/work", Session: "new",
			MaxAttempts: 5, MaxAttemptsSet: true,
			Deadline: "30m", DeadlineSet: true,
		}, "", nil, noProvider)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"retry.max_attempts": "5",
			"retry.deadline":     "30m",
		}, params.Overrides)
	})

	t.Run("model override uses explicit provider, does not resolve default", func(t *testing.T) {
		params, err := buildCreateParams(startFlags{
			Cwd: "/work", Session: "new", Provider: "claude",
			Model: "opus", ModelSet: true,
		}, "", nil, noProvider)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"providers.claude.model": "opus"}, params.Overrides)
	})

	t.Run("unattended override without --provider resolves the default provider", func(t *testing.T) {
		resolveCalls := 0
		resolve := func() (string, error) {
			resolveCalls++
			return "claude", nil
		}
		params, err := buildCreateParams(startFlags{
			Cwd: "/work", Session: "new",
			Unattended: true, UnattendedSet: true,
		}, "", nil, resolve)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"providers.claude.unattended": "true"}, params.Overrides)
		assert.Equal(t, 1, resolveCalls)
	})

	t.Run("model and unattended together resolve the default provider once", func(t *testing.T) {
		resolveCalls := 0
		resolve := func() (string, error) {
			resolveCalls++
			return "claude", nil
		}
		params, err := buildCreateParams(startFlags{
			Cwd: "/work", Session: "new",
			Model: "opus", ModelSet: true,
			Unattended: true, UnattendedSet: true,
		}, "", nil, resolve)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"providers.claude.model":      "opus",
			"providers.claude.unattended": "true",
		}, params.Overrides)
		assert.Equal(t, 1, resolveCalls)
	})

	t.Run("resolveDefaultProvider error propagates", func(t *testing.T) {
		_, err := buildCreateParams(startFlags{
			Cwd: "/work", Session: "new",
			Model: "opus", ModelSet: true,
		}, "", nil, func() (string, error) { return "", errors.New("boom") })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})
}
