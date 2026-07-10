package provider

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib-my-agent/internal/detect"
)

// stubProvider is a minimal Provider implementation for exercising the registry and interface
// shape without depending on any concrete adapter.
type stubProvider struct {
	name  string
	caps  Capabilities
	rules []detect.Rule
}

func (s *stubProvider) Name() string               { return s.name }
func (s *stubProvider) Capabilities() Capabilities { return s.caps }

func (s *stubProvider) BuildStart(task TaskSpec) (Command, error) {
	return Command{Argv: []string{s.name, task.Prompt}}, nil
}

func (s *stubProvider) BuildResume(task TaskSpec, sessionRef string) (Command, error) {
	return Command{Argv: []string{s.name, "--resume", sessionRef}}, nil
}

func (s *stubProvider) ExtractSessionRef(out AttemptOutput) (string, bool) {
	return "", false
}

func (s *stubProvider) DetectionRules() []detect.Rule { return s.rules }

func (s *stubProvider) CheckAvailability(ctx context.Context, task TaskSpec) (Availability, error) {
	return Availability{State: Unsupported}, nil
}

func newStub(name string) *stubProvider {
	return &stubProvider{
		name: name,
		caps: Capabilities{
			Resume:           true,
			ClientSuppliedID: true,
			Headless:         true,
			Interactive:      false,
			StructuredOutput: true,
		},
		rules: []detect.Rule{
			{Name: name + ".success", Category: detect.CategorySuccess, Priority: 1, Match: detect.Match{ExitCodeIn: []int{0}}},
		},
	}
}

func TestRegistry_RegisterGetRoundTrip(t *testing.T) {
	r := NewRegistry()
	p := newStub("roundtrip")

	require.NoError(t, r.Register(p))

	got, err := r.Get("roundtrip")
	require.NoError(t, err)
	assert.Same(t, Provider(p), got)
}

func TestRegistry_GetUnknownNameErrors(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(newStub("known")))

	_, err := r.Get("nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

func TestRegistry_DuplicateRegisterErrors(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(newStub("dup")))

	err := r.Register(newStub("dup"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dup")
}

func TestRegistry_ListSortedAndReflectsRegistrations(t *testing.T) {
	r := NewRegistry()

	require.NoError(t, r.Register(newStub("charlie")))
	require.NoError(t, r.Register(newStub("alpha")))
	require.NoError(t, r.Register(newStub("bravo")))

	list := r.List()
	require.Len(t, list, 3)
	names := make([]string, len(list))
	for i, p := range list {
		names[i] = p.Name()
	}
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, names)
}

func TestRegistry_CapabilitiesRoundTripThroughInterface(t *testing.T) {
	r := NewRegistry()
	want := Capabilities{
		Resume:           true,
		ClientSuppliedID: true,
		Headless:         true,
		Interactive:      false,
		StructuredOutput: true,
	}
	p := newStub("caps")
	require.NoError(t, r.Register(p))

	got, err := r.Get("caps")
	require.NoError(t, err)
	assert.Equal(t, want, got.Capabilities())
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	const n = 20

	var wg sync.WaitGroup
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = r.Register(newStub(fmt.Sprintf("concurrent-%d", i)))
		}(i)
		go func() {
			defer wg.Done()
			_ = r.List()
			_, _ = r.Get("concurrent-0")
		}()
	}
	wg.Wait()

	assert.LessOrEqual(t, len(r.List()), n)
}

func TestDefaultRegistry_RegisterGet(t *testing.T) {
	name := "provider-test-default-only-stub"
	require.NoError(t, Register(newStub(name)))

	got, err := Get(name)
	require.NoError(t, err)
	assert.Equal(t, name, got.Name())

	found := false
	for _, p := range List() {
		if p.Name() == name {
			found = true
		}
	}
	assert.True(t, found)
}

func TestAvailabilityState_String(t *testing.T) {
	tests := []struct {
		state AvailabilityState
		want  string
	}{
		{Available, "available"},
		{Unavailable, "unavailable"},
		{Unsupported, "unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.state.String())
		})
	}
}
