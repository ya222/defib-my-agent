package supervisor

import (
	"github.com/ya222/defib/internal/detect"
	"github.com/ya222/defib/internal/provider"
	"github.com/ya222/defib/internal/store"
)

// Session strategy, per docs/providers.md#session-strategy-important:
// pre-generated refs are passed through on the first start, provider-
// assigned refs are parsed from output and stored before any resume, and
// session_mode=existing resumes from the very first attempt.

// buildCommand chooses start vs resume: the first attempt of an existing
// session resumes; a known ref after any attempt resumes when the provider
// supports it; otherwise a fresh start passes the (possibly pre-generated)
// ref through.
func (s *Supervisor) buildCommand() (provider.Command, bool, error) {
	var ref string
	if s.task.SessionRef != nil {
		ref = *s.task.SessionRef
	}
	caps := s.deps.Provider.Capabilities()
	first := s.task.TotalAttempts == 0

	if ref != "" && caps.Resume && (!first || s.task.SessionMode == "existing") {
		cmd, err := s.deps.Provider.BuildResume(s.spec, ref)
		return cmd, true, err
	}

	spec := s.spec
	spec.SessionRef = ref
	cmd, err := s.deps.Provider.BuildStart(spec)
	return cmd, false, err
}

// adoptSessionRef stores a ref parsed from attempt output, but only when
// none is known yet — a known ref (pre-generated or user-supplied) is
// never overwritten. It runs inside the attempt-exit transition, so the
// ref is persisted in the same transaction, before any resume.
func (s *Supervisor) adoptSessionRef(next *store.Task, ev Event) {
	if next.SessionRef != nil && *next.SessionRef != "" {
		return
	}
	ref, ok := s.deps.Provider.ExtractSessionRef(provider.AttemptOutput{
		ExitCode: ev.ExitCode,
		Stdout:   detect.Tail(ev.Stdout, s.policy.ScanBytes),
		Stderr:   detect.Tail(ev.Stderr, s.policy.ScanBytes),
	})
	if ok && ref != "" {
		next.SessionRef = &ref
	}
}
