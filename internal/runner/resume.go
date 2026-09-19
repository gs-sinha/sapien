package runner

import (
	"fmt"
	"reflect"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/expr"
	"github.com/gs-sinha/sapien/internal/flow"
)

// resumePlan is Resume resolved against one flow: the main-step execution
// window (fromIdx/untilIdx, both indices into f.Steps), whether earlier
// results are available to reuse, and which main steps -- if any -- are
// skipped with no data at all (FromStep given without From).
type resumePlan struct {
	reuse bool
	// earlierByID indexes From's setup and main step results (Phase !=
	// "teardown") by step id, for O(1) lookup while walking the current
	// flow's setup/main lists. A nested step id maps to its LATEST
	// iteration's result (later entries in From.Steps overwrite earlier
	// ones), matching steps.<id>'s own "latest execution" rule (PLAN
	// §34f.8); earlierNestedByParent below carries the full history.
	earlierByID map[string]domain.StepResult
	// earlierNestedByParent indexes From's nested step results by their
	// Parent (a loop block's id), in original execution order, for
	// reusing a whole block: every iteration's every nested result, not
	// just the latest (PLAN §34f.8).
	earlierNestedByParent map[string][]domain.StepResult
	// fromIdx is the first index of f.Steps to execute for real; indices
	// before it are reused (reuse == true) or skipped without data
	// (reuse == false).
	fromIdx int
	// untilIdx is the last index of f.Steps to execute (inclusive);
	// -1 means no limit (through the last step).
	untilIdx int
	// skippedNoData holds the ids of main steps skipped with no data
	// (fromIdx skip window with reuse == false), for the forward-reference
	// check below.
	skippedNoData map[string]bool
}

// buildResumePlan resolves resume (nil means an ordinary full run) against
// f, validating FromStep/UntilStep name steps in f.Steps and, when set,
// that UntilStep does not run before FromStep. Resume only ever lands at a
// block's own boundary, never inside it (PLAN §34f.8): naming a nested step
// id is rejected with a message pointing at its enclosing block.
func buildResumePlan(f *domain.Flow, resume *Resume) (*resumePlan, error) {
	plan := &resumePlan{untilIdx: -1, skippedNoData: map[string]bool{}}
	if resume == nil {
		return plan, nil
	}

	if resume.From != nil {
		if resume.From.FlowID != "" && resume.From.FlowID != f.ID {
			return nil, errs.New(errs.Invalid, "run %s is a run of flow %q, not %q", resume.From.ID, resume.From.FlowID, f.ID).
				WithHint("resume_from must name a run of this same flow")
		}
		plan.reuse = true
		plan.earlierByID = map[string]domain.StepResult{}
		plan.earlierNestedByParent = map[string][]domain.StepResult{}
		for _, sr := range resume.From.Steps {
			if sr.Phase == "teardown" {
				continue
			}
			plan.earlierByID[sr.StepID] = sr
			if sr.Parent != "" {
				plan.earlierNestedByParent[sr.Parent] = append(plan.earlierNestedByParent[sr.Parent], sr)
			}
		}
	}

	fromIdx := 0
	switch {
	case resume.FromStep != "":
		idx, ok := indexOfStep(f.Steps, resume.FromStep)
		if !ok {
			if blockID, nested := findEnclosingBlock(f.Steps, resume.FromStep); nested {
				return nil, errs.New(errs.Invalid, "from_step %q is a step inside block %q", resume.FromStep, blockID).
					WithHint(fmt.Sprintf("resume at the block %q, not inside it", blockID))
			}
			return nil, errs.New(errs.Invalid, "from_step %q is not one of this flow's steps", resume.FromStep).
				WithHint("from_step must name one of the flow's main step ids")
		}
		fromIdx = idx
	case plan.reuse:
		fromIdx = defaultResumeIndex(f, resume.From)
	}
	plan.fromIdx = fromIdx

	if resume.UntilStep != "" {
		idx, ok := indexOfStep(f.Steps, resume.UntilStep)
		if !ok {
			if blockID, nested := findEnclosingBlock(f.Steps, resume.UntilStep); nested {
				return nil, errs.New(errs.Invalid, "until_step %q is a step inside block %q", resume.UntilStep, blockID).
					WithHint(fmt.Sprintf("resume through the block %q, not inside it", blockID))
			}
			return nil, errs.New(errs.Invalid, "until_step %q is not one of this flow's steps", resume.UntilStep).
				WithHint("until_step must name one of the flow's main step ids")
		}
		if idx < fromIdx {
			return nil, errs.New(errs.Invalid, "until_step %q runs before the resume point (%q)", resume.UntilStep, resume.FromStep).
				WithHint("until_step must be at or after from_step")
		}
		plan.untilIdx = idx
	}

	if !plan.reuse {
		for i := 0; i < fromIdx && i < len(f.Steps); i++ {
			plan.skippedNoData[f.Steps[i].ID] = true
		}
	}
	return plan, nil
}

// indexOfStep returns id's index in steps, or ok == false. It only looks at
// the top level: a nested step (inside a loop block) is never found this
// way, by design -- see findEnclosingBlock.
func indexOfStep(steps []domain.Step, id string) (int, bool) {
	for i, st := range steps {
		if st.ID == id {
			return i, true
		}
	}
	return 0, false
}

// findEnclosingBlock searches steps' top-level loop blocks for one whose
// nested Steps contains id, returning that block's id. Used to give
// from_step/until_step a specific, actionable error when it names a step
// inside a block rather than the block itself (PLAN §34f.8: resume only
// ever lands at a block's boundary).
func findEnclosingBlock(steps []domain.Step, id string) (blockID string, ok bool) {
	for _, st := range steps {
		if !st.IsBlock() {
			continue
		}
		for _, nested := range st.Steps {
			if nested.ID == id {
				return st.ID, true
			}
		}
	}
	return "", false
}

// defaultResumeIndex is the default resume point when From is set and
// FromStep is not (PLAN §9): the index (in f.Steps) of the first main step
// that did not pass in From, or -- if every step From recorded passed, or
// the failing step's id no longer exists in f -- the index positionally
// after the last main step From recorded. Only top-level entries (call
// steps and loop blocks) are considered: a loop block's own StepResult
// already reflects whether any of its iterations failed (PLAN §34f.8), so
// a nested result never needs to be inspected directly, and resume always
// lands at the block's own position, never inside it.
func defaultResumeIndex(f *domain.Flow, earlier *domain.Run) int {
	topLevel := make(map[string]bool, len(f.Steps))
	for _, st := range f.Steps {
		topLevel[st.ID] = true
	}
	var mainSteps []domain.StepResult
	for _, sr := range earlier.Steps {
		if (sr.Phase == "" || sr.Phase == "steps") && topLevel[sr.StepID] {
			mainSteps = append(mainSteps, sr)
		}
	}
	for _, sr := range mainSteps {
		if sr.Status == domain.StepPassed {
			continue
		}
		if idx, ok := indexOfStep(f.Steps, sr.StepID); ok {
			return idx
		}
		break // the failing step's id is gone from f; fall through below.
	}
	return len(mainSteps)
}

// reuseStepResult builds the new run's copy of an earlier StepResult: it is
// marked Reused, ReusedFromRun is set, Index is set for the new run's
// position (Phase is left to finishStep, which stamps it from ec.phase the
// same way it does for an executed step), and warning (if non-empty) is
// appended to Warnings. The original Request/Response/Assertions/Out/
// Timings/Status/Started/Finished are otherwise carried over unchanged.
func reuseStepResult(sr domain.StepResult, earlierRunID string, index int, warning string) domain.StepResult {
	out := sr
	out.Index = index
	out.Reused = true
	out.ReusedFromRun = earlierRunID
	if warning != "" {
		out.Warnings = append(append([]string(nil), sr.Warnings...), warning)
	}
	return out
}

// definitionChangedWarning compares step (as named by stepID) between
// currentSrc and earlierSrc (raw flow YAML), reporting the warning text
// Resume's contract calls for when the fields that determine what actually
// runs -- call, input, body, headers, extract, until -- differ. Returns ""
// when there is nothing to compare (either source is empty, they are
// byte-identical, or the step can't be found in one of them) or the
// comparable fields are equal.
func definitionChangedWarning(currentSrc, earlierSrc, stepID, earlierRunID string) string {
	if currentSrc == "" || earlierSrc == "" || currentSrc == earlierSrc {
		return ""
	}
	cur, ok := findStepByID(currentSrc, stepID)
	if !ok {
		return ""
	}
	old, ok := findStepByID(earlierSrc, stepID)
	if !ok {
		return ""
	}
	if stepDefinitionEqual(cur, old) {
		return ""
	}
	return fmt.Sprintf("definition changed since run %s; reused anyway", earlierRunID)
}

// findStepByID re-parses src (a raw flow YAML snapshot) and returns the step
// named id from any of its setup/steps/teardown lists.
func findStepByID(src, id string) (domain.Step, bool) {
	f, err := flow.Parse(src)
	if err != nil {
		return domain.Step{}, false
	}
	for _, fs := range flow.AllSteps(f) {
		if fs.Step.ID == id {
			return fs.Step, true
		}
	}
	return domain.Step{}, false
}

// stepDefinitionEqual compares the fields of a step definition that decide
// what actually runs: call, input, body, headers, extract, until.
func stepDefinitionEqual(a, b domain.Step) bool {
	return a.Call == b.Call &&
		reflect.DeepEqual(a.Input, b.Input) &&
		reflect.DeepEqual(a.Body, b.Body) &&
		reflect.DeepEqual(a.Headers, b.Headers) &&
		reflect.DeepEqual(a.Extract, b.Extract) &&
		a.Until == b.Until
}

// stepValueFromResult rebuilds the expr.StepValue a reused StepResult would
// have produced had it just executed, so `${steps.x.out.y}`,
// `steps.x.body`, and `steps.x.status` resolve exactly as they did in the
// earlier run.
func stepValueFromResult(sr domain.StepResult) expr.StepValue {
	sv := expr.StepValue{Out: sr.Out}
	if sr.Request != nil {
		sv.Request = map[string]any{
			"method":  sr.Request.Method,
			"url":     sr.Request.URL,
			"headers": lowerHeaderMap(sr.Request.Headers),
			"body":    sr.Request.Body,
		}
	}
	if sr.Response != nil {
		sv.Status = sr.Response.Status
		sv.Headers = lowerHeaderMap(sr.Response.Headers)
		sv.Body = sr.Response.Body
	}
	if sr.Timings != nil {
		sv.LatencyMs = sr.Timings.TotalMs
	}
	return sv
}

// buildBlockStepValue groups nested (an ordered list of a block's nested
// StepResults, however many iterations it spans) by Iteration into the
// per-iteration []map[string]StepValue shape steps.<block>.iterations
// exposes (PLAN §34f.8), and returns the block's aggregated StepValue.
// count is the block's own recorded iteration count -- normally equal to
// the number of distinct iterations found in nested, but authoritative on
// its own (e.g. an iteration whose nested steps are all reused-away).
func buildBlockStepValue(nested []domain.StepResult, count int) expr.StepValue {
	byIteration := map[int]map[string]expr.StepValue{}
	maxIter := -1
	for _, sr := range nested {
		if sr.Iteration == nil {
			continue
		}
		i := *sr.Iteration
		if byIteration[i] == nil {
			byIteration[i] = map[string]expr.StepValue{}
		}
		byIteration[i][sr.StepID] = stepValueFromResult(sr)
		if i > maxIter {
			maxIter = i
		}
	}
	n := count
	if maxIter+1 > n {
		n = maxIter + 1
	}
	iterations := make([]map[string]expr.StepValue, n)
	for i := range iterations {
		if m := byIteration[i]; m != nil {
			iterations[i] = m
		} else {
			iterations[i] = map[string]expr.StepValue{}
		}
	}
	return expr.StepValue{IsBlock: true, Count: count, Iterations: iterations}
}

// mergeResumeInputs merges provided over earlierInputs (provided wins per
// key), for RunOptions' "inputs default to the earlier run's inputs when
// not given" (PLAN §9). Returns provided unchanged when earlierInputs is
// empty.
func mergeResumeInputs(earlierInputs, provided map[string]any) map[string]any {
	if len(earlierInputs) == 0 {
		return provided
	}
	merged := make(map[string]any, len(earlierInputs)+len(provided))
	for k, v := range earlierInputs {
		merged[k] = v
	}
	for k, v := range provided {
		merged[k] = v
	}
	return merged
}

// referencedSkippedSteps returns the ids from skippedNoData that st's
// expressions reference via `steps.<id>`, for the "FromStep without
// ResumeFrom" forward-reference check: a step skipped that way carries no
// data at all, so referencing it must fail loudly rather than silently
// evaluate against an empty StepValue.
func referencedSkippedSteps(st domain.Step, skippedNoData map[string]bool) []string {
	if len(skippedNoData) == 0 {
		return nil
	}
	var hits []string
	for _, id := range flow.ReferencedSteps(st) {
		if skippedNoData[id] {
			hits = append(hits, id)
		}
	}
	return hits
}

// forwardRefError builds the StepResult/error pair for a step that
// references a step skipped with no data (see referencedSkippedSteps).
func forwardRefError(step domain.Step, idx int, op *domain.Operation, refs []string, now time.Time) domain.StepResult {
	err := errs.New(errs.Invalid, "step %q references step %q, which was skipped (from_step) and has no data", step.ID, refs[0]).
		WithHint("resume from a run that includes it, or drop from_step to run the whole flow")
	return domain.StepResult{
		StepID:    step.ID,
		Index:     idx,
		Operation: opID(op),
		Status:    domain.StepErrored,
		Error:     errs.ToInfo(err),
		Started:   now,
		Finished:  now,
	}
}
