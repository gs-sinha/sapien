package runner

import (
	"context"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/expr"
)

// blockIterationCap is a hard, defensive ceiling on iterations independent
// of a block's own `max`/`repeat.max` (which the validator and executeBlock
// itself already bound to 1..1000): it exists only so a flow built
// programmatically and never validated cannot spin forever.
const blockIterationCap = 1000

// kindOf returns a loop block's Kind ("foreach" or "repeat", PLAN §34f.8).
func kindOf(block domain.Step) string {
	if block.Repeat != nil {
		return "repeat"
	}
	return "foreach"
}

// blockScope builds the expr.Scope a block's own foreach/when (iter nil) or
// break_when/repeat.until/repeat.while (iter set, so a sibling in the same
// iteration and steps.<block>'s own children are visible) evaluates
// against.
func blockScope(ec *execCtx, stepsSoFar map[string]expr.StepValue, iter *expr.IterValue) expr.Scope {
	return expr.Scope{Inputs: ec.run.Inputs, Env: ec.envVars, Steps: stepsSoFar, Iter: iter}
}

// executeBlock runs a loop block (PLAN §34f.8): evaluates `when`, then
// either foreach (iterating a CEL list, capped at max, failing before
// iterating if the list is longer) or repeat (up to repeat.max, gated by
// while/until, waiting interval between iterations), recursing into the
// block's own nested steps once per iteration via runBlockIteration --
// which uses the exact same executeStep/rememberStep machinery a top-level
// step uses, so a nested step's own `when`, assertions, extraction, and
// steps.<id> resolution all behave identically, just with iter.item/
// iter.index available and sibling steps of the same block visible
// regardless of declaration order (see internal/flow.checkStepRef).
//
// Cancellation/timeout (ctx) is checked before each iteration and inside
// runBlockIteration between nested steps, so a loop stops promptly rather
// than running to completion once the context is done.
func (r *Runner) executeBlock(ctx context.Context, ec *execCtx, block domain.Step, idx int, opsByID map[string]*domain.Operation, stepsSoFar map[string]expr.StepValue, engineErr *error) (domain.StepResult, expr.StepValue) {
	started := ec.now()
	kind := kindOf(block)
	result := domain.StepResult{StepID: block.ID, Index: idx, Kind: kind, Started: started}

	fail := func(status domain.StepStatus, err error) (domain.StepResult, expr.StepValue) {
		result.Status = status
		result.Finished = ec.now()
		if err != nil {
			e := errs.As(err)
			result.Error = &domain.ErrorInfo{Code: string(e.Code), Message: e.Message, Details: e.Details}
		}
		return result, expr.StepValue{IsBlock: true}
	}

	// `when` on a block behaves exactly like `when` on a call step: checked
	// before anything else, no sibling/iter access (nothing has run yet),
	// and a false result skips the whole block -- zero iterations, left out
	// of stepsSoFar (PLAN §34f.7/8).
	if block.When != "" {
		ok, werr := ec.eval.EvalBool(block.When, blockScope(ec, stepsSoFar, nil))
		if werr != nil {
			return fail(domain.StepErrored, werr)
		}
		if !ok {
			result.SkipReason = "when"
			return fail(domain.StepSkipped, nil)
		}
	}

	r.emitStep(ec, block.ID, domain.StepResolving, 0)

	var items []any
	max := blockIterationCap
	if block.Foreach != "" {
		v, err := ec.eval.Eval(block.Foreach, blockScope(ec, stepsSoFar, nil))
		if err != nil {
			return fail(domain.StepErrored, err)
		}
		list, ok := v.([]any)
		if !ok {
			return fail(domain.StepErrored, errs.New(errs.Invalid,
				"block %q: foreach must evaluate to a list, got %T", block.ID, v).WithDetail("step", block.ID))
		}
		effMax := block.Max
		if effMax == 0 {
			effMax = 100
		}
		if len(list) > effMax {
			// PLAN §34f.8: the block fails before iterating -- it never
			// silently truncates the list.
			return fail(domain.StepFailed, errs.New(errs.Invalid,
				"block %q: foreach list has %d item(s), exceeding max %d", block.ID, len(list), effMax).
				WithDetail("step", block.ID).WithDetail("count", len(list)).WithDetail("max", effMax))
		}
		items = list
		max = len(list)
	} else if block.Repeat != nil {
		max = block.Repeat.Max
	}

	var interval time.Duration
	if block.Repeat != nil && block.Repeat.Interval != "" {
		d, err := time.ParseDuration(block.Repeat.Interval)
		if err != nil {
			return fail(domain.StepErrored, err)
		}
		interval = d
	}

	var iterations []map[string]expr.StepValue
	var anyFailed bool
	count := 0

	for i := 0; i < max; i++ {
		if block.Repeat != nil && block.Repeat.While != "" {
			ok, werr := ec.eval.EvalBool(block.Repeat.While, blockScope(ec, stepsSoFar, &expr.IterValue{Index: i}))
			if werr != nil {
				return fail(domain.StepErrored, werr)
			}
			if !ok {
				break
			}
		}

		if ctx.Err() != nil {
			break
		}

		var item any
		if block.Foreach != "" {
			item = items[i]
		}
		iter := &expr.IterValue{Item: item, Index: i}

		iterVals, iterFailed, iterCancelled := r.runBlockIteration(ctx, ec, block, opsByID, stepsSoFar, iter, engineErr)
		count++
		iterations = append(iterations, iterVals)
		if iterFailed {
			anyFailed = true
		}
		if iterCancelled {
			break
		}
		if iterFailed && block.OnError != "continue" {
			break
		}

		if block.BreakWhen != "" {
			ok, werr := ec.eval.EvalBool(block.BreakWhen, blockScope(ec, stepsSoFar, iter))
			if werr != nil {
				anyFailed = true
				break
			}
			if ok {
				break
			}
		}

		if block.Repeat != nil && block.Repeat.Until != "" {
			ok, uerr := ec.eval.EvalBool(block.Repeat.Until, blockScope(ec, stepsSoFar, iter))
			if uerr != nil {
				anyFailed = true
				break
			}
			if ok {
				break
			}
		}

		if interval > 0 && i+1 < max && ctx.Err() == nil {
			if serr := ec.sleep(ctx, interval); serr != nil {
				break
			}
		}
	}

	result.Count = count
	result.Finished = ec.now()
	switch {
	case ctx.Err() != nil:
		result.Status = domain.StepCancelled
	case anyFailed:
		result.Status = domain.StepFailed
	default:
		result.Status = domain.StepPassed
	}

	raw := expr.StepValue{IsBlock: true, Count: count, Iterations: iterations}
	return result, raw
}

// runBlockIteration executes block's nested steps once for one iteration,
// via the same executeStep/rememberStep machinery a top-level step uses:
// ec.iter/ec.parent are set for the duration (executeStep reads ec.iter to
// populate the `iter` root; finishStep reads both to stamp
// StepResult.Iteration/Parent and the live event payload, PLAN §34f.8).
// stepsSoFar is updated after each nested step exactly as it is for a
// top-level step, so steps.<nested-id> always resolves to its latest
// execution -- including, on the next iteration, this one's.
//
// A failed or errored nested step ends THIS iteration (later nested steps
// in the same pass do not run), matching a flow's ordinary
// stop-on-(the-first-)failure policy; whether it ends the whole BLOCK is
// on_error's call, made by the caller. Cancellation is checked between
// nested steps so a loop cannot get stuck mid-iteration once ctx is done.
func (r *Runner) runBlockIteration(ctx context.Context, ec *execCtx, block domain.Step, opsByID map[string]*domain.Operation, stepsSoFar map[string]expr.StepValue, iter *expr.IterValue, engineErr *error) (iterVals map[string]expr.StepValue, failed, cancelled bool) {
	iterVals = map[string]expr.StepValue{}

	savedIter, savedParent := ec.iter, ec.parent
	ec.iter, ec.parent = iter, block.ID
	defer func() { ec.iter, ec.parent = savedIter, savedParent }()

	for _, nested := range block.Steps {
		if nested.IsBlock() {
			// Defensive only: the validator rejects a block nested inside
			// another block (NESTED_LOOP) before a flow ever reaches the
			// runner.
			continue
		}
		if ctx.Err() != nil {
			cancelled = true
			failed = true
			break
		}

		op := opsByID[nested.ID]
		result, raw := r.executeStep(ctx, ec, nested, ec.nextIndex(), op, stepsSoFar)
		if ferr := r.finishStep(ctx, ec, result); ferr != nil && *engineErr == nil {
			*engineErr = ferr
		}
		rememberStep(stepsSoFar, result, raw)
		if result.Status != domain.StepSkipped {
			iterVals[nested.ID] = raw
		}

		switch result.Status {
		case domain.StepFailed, domain.StepErrored:
			failed = true
		case domain.StepCancelled:
			failed = true
			cancelled = true
		}
		if failed {
			break
		}
	}
	return iterVals, failed, cancelled
}

// reuseBlock reuses an earlier run's whole loop block: its own StepResult
// (sr) plus every nested StepResult it recorded across every iteration
// (plan.earlierNestedByParent[block.ID], in original order), so the new
// run's history looks exactly like the old one's for this block, and
// stepsSoFar is rebuilt for both the block itself (aggregated, with
// count/iterations) and each nested step id (its latest reused iteration)
// -- PLAN §34f.8.
func (r *Runner) reuseBlock(ctx context.Context, ec *execCtx, plan *resumePlan, block domain.Step, sr domain.StepResult, resumeRunID string, stepsSoFar map[string]expr.StepValue) error {
	var engineErr error

	blockResult := reuseStepResult(sr, resumeRunID, ec.nextIndex(), "")
	if ferr := r.finishStep(ctx, ec, blockResult); ferr != nil && engineErr == nil {
		engineErr = ferr
	}

	nested := plan.earlierNestedByParent[block.ID]
	for _, nsr := range nested {
		reused := reuseStepResult(nsr, resumeRunID, ec.nextIndex(), "")
		// reuseStepResult carries Iteration/Parent over from nsr already
		// (they are plain fields on the copied struct); finishStep only
		// overwrites them when ec.iter is set, which it deliberately is
		// not here -- reused nested results keep their original values.
		if ferr := r.finishStep(ctx, ec, reused); ferr != nil && engineErr == nil {
			engineErr = ferr
		}
		stepsSoFar[nsr.StepID] = stepValueFromResult(reused)
	}

	stepsSoFar[block.ID] = buildBlockStepValue(nested, sr.Count)
	return engineErr
}
