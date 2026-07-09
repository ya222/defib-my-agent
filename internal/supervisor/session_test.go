package supervisor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Session strategy per docs/providers.md#session-strategy-important.
func TestSessionHandling(t *testing.T) {
	t.Run("pre-generated id is passed through to BuildStart", func(t *testing.T) {
		h := newHarness(t, harnessOpts{sessionRef: "pre-gen-uuid"})
		h.handle(Event{Type: EventStart})

		require.Len(t, h.spy.startSpecs, 1)
		assert.Equal(t, "pre-gen-uuid", h.spy.startSpecs[0].SessionRef,
			"first attempt of a new session starts (never resumes) with the pre-generated ref embedded")
		assert.Empty(t, h.spy.resumeRefs)
	})

	t.Run("session_mode=existing resumes on the very first attempt", func(t *testing.T) {
		h := newHarness(t, harnessOpts{sessionMode: "existing", sessionRef: "user-supplied"})
		h.handle(Event{Type: EventStart})

		assert.Empty(t, h.spy.startSpecs, "BuildStart must not be called")
		require.Len(t, h.spy.resumeRefs, 1)
		assert.Equal(t, "user-supplied", h.spy.resumeRefs[0])
	})

	t.Run("parsed ref is stored before any resume", func(t *testing.T) {
		h := newHarness(t, harnessOpts{}) // no pre-generated ref
		h.spy.extractRef = "parsed-from-output"

		h.handle(Event{Type: EventStart})
		require.Len(t, h.spy.startSpecs, 1)
		assert.Empty(t, h.spy.startSpecs[0].SessionRef)

		// Retryable exit: the ref parsed from output must be persisted in
		// the same transaction as the transition.
		h.handle(exit(9, "retryable noise\n"))
		task := h.dbTask()
		require.NotNil(t, task.SessionRef)
		assert.Equal(t, "parsed-from-output", *task.SessionRef)

		// The next attempt resumes with the parsed ref.
		h.clock.Advance(time.Minute)
		h.handle(h.expectTimerFire())
		require.Len(t, h.spy.resumeRefs, 1)
		assert.Equal(t, "parsed-from-output", h.spy.resumeRefs[0])
	})

	t.Run("a known ref never gets overwritten by extraction", func(t *testing.T) {
		h := newHarness(t, harnessOpts{sessionRef: "original"})
		h.spy.extractRef = "imposter"

		h.handle(Event{Type: EventStart})
		h.handle(exit(9, "retryable\n"))

		task := h.dbTask()
		require.NotNil(t, task.SessionRef)
		assert.Equal(t, "original", *task.SessionRef)
	})

	t.Run("no ref and no resume support falls back to fresh starts", func(t *testing.T) {
		h := newHarness(t, harnessOpts{}) // fake extracts nothing by default

		h.handle(Event{Type: EventStart})
		h.handle(exit(9, "retryable\n"))
		h.clock.Advance(time.Minute)
		h.handle(h.expectTimerFire())

		assert.Len(t, h.spy.startSpecs, 2, "both attempts start fresh")
		assert.Empty(t, h.spy.resumeRefs)
	})
}
