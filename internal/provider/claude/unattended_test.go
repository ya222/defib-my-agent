package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/provider"
)

// M10-T3: the skip-approvals flag is never present by default and appears
// only on explicit opt-in.
func TestUnattendedFlag(t *testing.T) {
	c := New()
	build := func(t *testing.T, cfg map[string]any) [][]string {
		t.Helper()
		task := provider.TaskSpec{Prompt: "p", ProviderConfig: cfg}
		start, err := c.BuildStart(task)
		require.NoError(t, err)
		resume, err := c.BuildResume(task, "11111111-1111-1111-1111-111111111111")
		require.NoError(t, err)
		return [][]string{start.Argv, resume.Argv}
	}

	tests := []struct {
		name string
		cfg  map[string]any
		want bool
	}{
		{"absent by default (no config)", nil, false},
		{"absent when explicitly false", map[string]any{"unattended": false}, false},
		{"absent for a non-bool value", map[string]any{"unattended": "yes"}, false},
		{"present only when opted in", map[string]any{"unattended": true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, argv := range build(t, tt.cfg) {
				assert.Equal(t, tt.want, contains(argv, skipPermissionsFlag), "argv: %v", argv)
			}
		})
	}

	t.Run("flag precedes the passthrough separator", func(t *testing.T) {
		task := provider.TaskSpec{
			Prompt:         "p",
			Passthrough:    []string{"--custom"},
			ProviderConfig: map[string]any{"unattended": true},
		}
		cmd, err := c.BuildStart(task)
		require.NoError(t, err)
		flagAt, dashAt := index(cmd.Argv, skipPermissionsFlag), index(cmd.Argv, "--")
		require.GreaterOrEqual(t, flagAt, 0)
		require.GreaterOrEqual(t, dashAt, 0)
		assert.Less(t, flagAt, dashAt, "defib-controlled flag must not leak into passthrough")
	})
}

func contains(argv []string, s string) bool { return index(argv, s) >= 0 }

func index(argv []string, s string) int {
	for i, a := range argv {
		if a == s {
			return i
		}
	}
	return -1
}
