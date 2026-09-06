package diagnose

import (
	"context"
	"fmt"
	"sort"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
)

// SoftChange is one soft assertion whose outcome differs from the previous
// run of the same flow.
type SoftChange struct {
	StepID     string `json:"step_id"`
	Expr       string `json:"expr"`
	NowPassing bool   `json:"now_passing"`
	SinceRun   string `json:"since_run"`
}

// SoftChanges compares run's soft assertions with the most recent earlier
// run of the same flow and reports the ones that flipped, so a gap a flow
// recorded as a soft mismatch is announced when it closes (and a
// regression when it opens) instead of waiting to be noticed by eye. Best
// effort: nil when there is no earlier run or the store cannot be read.
func SoftChanges(ctx context.Context, eng engine.Engine, run *domain.Run) []SoftChange {
	if run == nil || run.FlowID == "" || eng == nil {
		return nil
	}
	earlier, err := eng.Runs().List(ctx, domain.RunFilter{FlowID: run.FlowID, Limit: 20})
	if err != nil {
		return nil
	}
	var prev *domain.Run
	for i := range earlier {
		r := &earlier[i]
		if r.ID == run.ID || !r.Started.Before(run.Started) {
			continue
		}
		if prev == nil || r.Started.After(prev.Started) {
			prev = r
		}
	}
	if prev == nil {
		return nil
	}
	// The list may be summaries without steps; fetch the full run then.
	if len(prev.Steps) == 0 {
		full, err := eng.Runs().Get(ctx, prev.ID)
		if err != nil {
			return nil
		}
		prev = full
	}
	before := softOutcomes(prev)
	var out []SoftChange
	for _, st := range run.Steps {
		for _, a := range st.Assertions {
			if !a.Soft {
				continue
			}
			key := st.StepID + "\x00" + a.Expr
			was, seen := before[key]
			if !seen || was == a.Passed {
				continue
			}
			out = append(out, SoftChange{StepID: st.StepID, Expr: a.Expr, NowPassing: a.Passed, SinceRun: prev.ID})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StepID+out[i].Expr < out[j].StepID+out[j].Expr })
	return out
}

func softOutcomes(run *domain.Run) map[string]bool {
	m := map[string]bool{}
	for _, st := range run.Steps {
		for _, a := range st.Assertions {
			if a.Soft {
				m[st.StepID+"\x00"+a.Expr] = a.Passed
			}
		}
	}
	return m
}

// SoftLines renders SoftChanges and the run's current soft mismatches as
// human lines for CLI and MCP output.
func SoftLines(run *domain.Run, changes []SoftChange) []string {
	var lines []string
	for _, c := range changes {
		if c.NowPassing {
			lines = append(lines, fmt.Sprintf("soft assertion now passing since run %s: %s: %s", c.SinceRun, c.StepID, c.Expr))
		} else {
			lines = append(lines, fmt.Sprintf("soft assertion now failing since run %s: %s: %s", c.SinceRun, c.StepID, c.Expr))
		}
	}
	if run != nil {
		for _, st := range run.Steps {
			for _, a := range st.Assertions {
				if a.Soft && !a.Passed {
					msg := a.Expr
					if a.Message != "" {
						msg += " (" + a.Message + ")"
					}
					lines = append(lines, fmt.Sprintf("soft mismatch: %s: %s", st.StepID, msg))
				}
			}
		}
	}
	return lines
}
