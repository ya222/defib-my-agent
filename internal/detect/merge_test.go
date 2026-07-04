package detect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMerge_ReplacementByName(t *testing.T) {
	builtin := []Rule{
		{Name: "generic.overloaded", Category: CategoryTransientError, Priority: 40},
		{Name: "generic.success", Category: CategorySuccess, Priority: 1},
	}
	user := []Rule{
		{Name: "generic.overloaded", Category: CategoryFatalError, Priority: 99},
	}

	got := Merge(builtin, user)

	require.Len(t, got, 2)
	// The replacement wins outright: category and priority come from the
	// user rule, and it now sorts to the front by priority.
	assert.Equal(t, "generic.overloaded", got[0].Name)
	assert.Equal(t, CategoryFatalError, got[0].Category)
	assert.Equal(t, 99, got[0].Priority)
	assert.Equal(t, "generic.success", got[1].Name)
}

func TestMerge_AdditiveMerge(t *testing.T) {
	builtin := []Rule{
		{Name: "generic.success", Category: CategorySuccess, Priority: 1},
	}
	user := []Rule{
		{Name: "custom.quota", Category: CategoryQuotaExhausted, Priority: 86},
	}

	got := Merge(builtin, user)

	require.Len(t, got, 2)
	assert.Equal(t, "custom.quota", got[0].Name, "higher priority sorts first")
	assert.Equal(t, "generic.success", got[1].Name)
}

func TestMerge_ReplacementAndAdditionTogether(t *testing.T) {
	builtin := []Rule{
		{Name: "generic.overloaded", Category: CategoryTransientError, Priority: 40},
		{Name: "generic.success", Category: CategorySuccess, Priority: 1},
	}
	user := []Rule{
		{Name: "generic.overloaded", Category: CategoryFatalError, Priority: 50},
		{Name: "custom.quota", Category: CategoryQuotaExhausted, Priority: 86},
	}

	got := Merge(builtin, user)

	require.Len(t, got, 3)
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	assert.Equal(t, []string{"custom.quota", "generic.overloaded", "generic.success"}, names)
	assert.Equal(t, CategoryFatalError, got[1].Category, "overloaded rule was replaced")
}

func TestMerge_EmptyUser(t *testing.T) {
	builtin := []Rule{
		{Name: "generic.success", Category: CategorySuccess, Priority: 1},
		{Name: "generic.overloaded", Category: CategoryTransientError, Priority: 40},
	}

	got := Merge(builtin, nil)

	require.Len(t, got, 2)
	assert.Equal(t, "generic.overloaded", got[0].Name)
	assert.Equal(t, "generic.success", got[1].Name)
}

func TestMerge_EmptyBuiltin(t *testing.T) {
	user := []Rule{
		{Name: "custom.quota", Category: CategoryQuotaExhausted, Priority: 86},
	}

	got := Merge(nil, user)

	require.Len(t, got, 1)
	assert.Equal(t, "custom.quota", got[0].Name)
}

func TestMerge_BothEmpty(t *testing.T) {
	got := Merge(nil, nil)
	assert.Empty(t, got)
}

func TestMerge_StableOrderingAtEqualPriority(t *testing.T) {
	// At equal priority, built-ins precede added user rules.
	builtin := []Rule{
		{Name: "builtin.a", Category: CategorySuccess, Priority: 10},
		{Name: "builtin.b", Category: CategorySuccess, Priority: 10},
	}
	user := []Rule{
		{Name: "user.a", Category: CategorySuccess, Priority: 10},
	}

	got := Merge(builtin, user)

	require.Len(t, got, 3)
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	assert.Equal(t, []string{"builtin.a", "builtin.b", "user.a"}, names)
}

func TestMerge_DoesNotMutateInputs(t *testing.T) {
	builtin := []Rule{
		{Name: "generic.overloaded", Category: CategoryTransientError, Priority: 40},
		{Name: "generic.success", Category: CategorySuccess, Priority: 1},
	}
	user := []Rule{
		{Name: "generic.overloaded", Category: CategoryFatalError, Priority: 99},
	}
	builtinCopy := append([]Rule(nil), builtin...)
	userCopy := append([]Rule(nil), user...)

	_ = Merge(builtin, user)

	assert.Equal(t, builtinCopy, builtin)
	assert.Equal(t, userCopy, user)
}
