package runs

import "github.com/gs-sinha/sapien/internal/domain"

// Summarize recomputes a domain.RunSummary from step results. It is a pure
// function (no I/O) so the runner can call it directly while a run is still
// in-flight. Teardown steps (Phase == "teardown") are excluded: PLAN §9
// keeps the summary about the main steps (setup counts too, since a failed
// setup step blocks the main steps the same way a failed main step does),
// so a failed cleanup step never muddies whether the run's own work
// passed.
func Summarize(steps []domain.StepResult) domain.RunSummary {
	var s domain.RunSummary
	for _, step := range steps {
		if step.Phase == "teardown" {
			continue
		}
		s.StepsTotal++
		switch step.Status {
		case domain.StepPassed:
			s.StepsPassed++
		case domain.StepFailed:
			s.StepsFailed++
		case domain.StepErrored:
			s.StepsErrored++
		case domain.StepSkipped:
			s.StepsSkipped++
		}
		for _, a := range step.Assertions {
			s.Assertions++
			if !a.Passed {
				if a.Soft {
					s.AssertionsWarned++
				} else {
					s.AssertionsFailed++
				}
			}
		}
	}
	return s
}

// DeriveStatus decides the terminal run status from step results and
// whether the run was cancelled or hit an engine-level error (PLAN.md §9):
//
//   - cancelled always wins: RunCancelled.
//   - otherwise a non-nil engineErr (resolution/transport/engine failure
//     outside any single step) wins: RunErrored.
//   - otherwise any step StepErrored: RunErrored.
//   - otherwise any step StepFailed (assertion or until-timeout): RunFailed.
//   - otherwise, if at least one step StepPassed: RunPassed.
//   - otherwise (no steps, or only skipped/pending/cancelled steps and none
//     passed): RunErrored, since nothing meaningful actually completed.
//
// Teardown steps (Phase == "teardown") never affect the outcome: a failed
// or errored cleanup step is recorded on its own StepResult but does not
// change the run's status (PLAN §9).
func DeriveStatus(steps []domain.StepResult, cancelled bool, engineErr error) domain.RunStatus {
	if cancelled {
		return domain.RunCancelled
	}
	if engineErr != nil {
		return domain.RunErrored
	}

	var hasFailed bool
	var passed int
	for _, step := range steps {
		if step.Phase == "teardown" {
			continue
		}
		switch step.Status {
		case domain.StepErrored:
			return domain.RunErrored
		case domain.StepFailed:
			hasFailed = true
		case domain.StepPassed:
			passed++
		}
	}
	if hasFailed {
		return domain.RunFailed
	}
	if passed > 0 {
		return domain.RunPassed
	}
	return domain.RunErrored
}
