// Package runs is Sapien's run persistence and retention layer (PLAN.md §9,
// §15). It stores flow/call executions (runs) and their per-step results
// (run_steps) in SQLite, and prunes old, unpinned history.
//
// Timestamps are stored as RFC3339Nano text in UTC; a zero time.Time is
// stored as SQL NULL and read back as the zero time.Time. Structured columns
// (maps, slices, pointers to records) go through store.MarshalJSON /
// store.UnmarshalJSON; plain optional strings go through store.NullString /
// store.StringOrEmpty.
package runs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/store"
)

// Store persists runs and run_steps.
type Store struct{ db *store.DB }

// New returns a Store backed by db.
func New(db *store.DB) *Store { return &Store{db: db} }

// runColumns is the fixed column list/order for the runs table, shared by
// every SELECT so scanRun always matches.
const runColumns = `id, flow_id, flow_snapshot_json, env, inputs_json, status, started, finished, duration_ms, summary_json, pinned, trigger, error_json, operation_hashes_json`

// stepColumns is the fixed column list/order for run_steps, excluding
// run_id (always known from the query's WHERE clause).
const stepColumns = `step_id, idx, status, operation, attempts, request_json, response_json, timings_json, assertions_json, out_json, error_json, started, finished`

// Create inserts run, assigning run.ID and run.Started if unset and
// defaulting run.Status to queued. Only the fields relevant to a freshly
// started run are persisted (flow id/snapshot, environment, inputs,
// trigger, operation hashes, status, started); finished, duration, summary,
// error, and pinned start at their zero/default values regardless of what
// run carries, since those are set later via Update. No steps are written.
func (s *Store) Create(ctx context.Context, run *domain.Run) error {
	if run.ID == "" {
		run.ID = store.NewID("run")
	}
	if run.Status == "" {
		run.Status = domain.RunQueued
	}
	if run.Started.IsZero() {
		run.Started = time.Now().UTC()
	} else {
		run.Started = run.Started.UTC()
	}

	inputs := run.Inputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	inputsJSON, err := store.MarshalJSON(inputs)
	if err != nil {
		return err
	}

	var opHashesJSON sql.NullString
	if len(run.OperationHashes) > 0 {
		s2, err := store.MarshalJSON(run.OperationHashes)
		if err != nil {
			return err
		}
		opHashesJSON = sql.NullString{String: s2, Valid: true}
	}

	zeroSummaryJSON, err := store.MarshalJSON(domain.RunSummary{})
	if err != nil {
		return err
	}

	return s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO runs (`+runColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.ID,
			store.NullString(run.FlowID),
			store.NullString(run.FlowSnapshot),
			store.NullString(run.Environment),
			inputsJSON,
			string(run.Status),
			timeToNull(run.Started),
			sql.NullString{}, // finished: not set at create
			sql.NullInt64{},  // duration_ms: not set at create
			zeroSummaryJSON,
			0, // pinned
			store.NullString(run.Trigger),
			sql.NullString{}, // error_json: not set at create
			opHashesJSON,
		)
		return err
	})
}

// Update writes run's status, finished, duration_ms, summary, error, and
// pinned fields. It returns errs.RunNotFound if no run with run.ID exists.
func (s *Store) Update(ctx context.Context, run *domain.Run) error {
	summaryJSON, err := store.MarshalJSON(run.Summary)
	if err != nil {
		return err
	}
	errorJSON, err := marshalPtrJSON(run.Error)
	if err != nil {
		return err
	}

	return s.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE runs SET status=?, finished=?, duration_ms=?, summary_json=?, error_json=?, pinned=? WHERE id=?`,
			string(run.Status),
			timeToNull(run.Finished),
			durationToNull(run.Finished, run.DurationMs),
			summaryJSON,
			errorJSON,
			boolToInt(run.Pinned),
			run.ID,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errs.New(errs.RunNotFound, "run %q not found", run.ID)
		}
		return nil
	})
}

// AppendStep upserts step by (run_id, step_id): a first call inserts the
// row (recording step.Index as idx); a later call with the same step_id
// updates every column except idx, which is preserved from the original
// insert. Returns errs.RunNotFound if runID does not reference an existing
// run.
func (s *Store) AppendStep(ctx context.Context, runID string, step domain.StepResult) error {
	requestJSON, err := marshalPtrJSON(step.Request)
	if err != nil {
		return err
	}
	responseJSON, err := marshalPtrJSON(step.Response)
	if err != nil {
		return err
	}
	timingsJSON, err := marshalPtrJSON(step.Timings)
	if err != nil {
		return err
	}
	errorJSON, err := marshalPtrJSON(step.Error)
	if err != nil {
		return err
	}

	var assertionsJSON sql.NullString
	if len(step.Assertions) > 0 {
		s2, err := store.MarshalJSON(step.Assertions)
		if err != nil {
			return err
		}
		assertionsJSON = sql.NullString{String: s2, Valid: true}
	}

	var outJSON sql.NullString
	if len(step.Out) > 0 {
		s2, err := store.MarshalJSON(step.Out)
		if err != nil {
			return err
		}
		outJSON = sql.NullString{String: s2, Valid: true}
	}

	return s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO run_steps (run_id, `+stepColumns+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(run_id, step_id) DO UPDATE SET
				status          = excluded.status,
				operation       = excluded.operation,
				attempts        = excluded.attempts,
				request_json    = excluded.request_json,
				response_json   = excluded.response_json,
				timings_json    = excluded.timings_json,
				assertions_json = excluded.assertions_json,
				out_json        = excluded.out_json,
				error_json      = excluded.error_json,
				started         = excluded.started,
				finished        = excluded.finished
		`,
			runID,
			step.StepID,
			step.Index,
			string(step.Status),
			store.NullString(step.Operation),
			step.Attempts,
			requestJSON,
			responseJSON,
			timingsJSON,
			assertionsJSON,
			outJSON,
			errorJSON,
			timeToNull(step.Started),
			timeToNull(step.Finished),
		)
		if err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				return errs.New(errs.RunNotFound, "run %q not found", runID)
			}
			return err
		}
		return nil
	})
}

// Get returns the run with its steps ordered by index, or errs.RunNotFound.
func (s *Store) Get(ctx context.Context, id string) (*domain.Run, error) {
	var run *domain.Run
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		row := conn.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id)
		r, err := scanRun(row)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errs.New(errs.RunNotFound, "run %q not found", id)
			}
			return err
		}
		steps, err := loadSteps(ctx, conn, id)
		if err != nil {
			return err
		}
		r.Steps = steps
		run = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

// List returns runs (without steps, but with Summary) matching f, ordered
// by started DESC, most recently started first.
func (s *Store) List(ctx context.Context, f domain.RunFilter) ([]domain.Run, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	var conds []string
	var args []any
	if f.FlowID != "" {
		conds = append(conds, "flow_id = ?")
		args = append(args, f.FlowID)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Operation != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM run_steps rs WHERE rs.run_id = runs.id AND rs.operation = ?)")
		args = append(args, f.Operation)
	}

	query := `SELECT ` + runColumns + ` FROM runs`
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY started DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	var out []domain.Run
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRun(rows)
			if err != nil {
				return err
			}
			out = append(out, *r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Pin sets a run's pinned flag. Returns errs.RunNotFound if id does not exist.
func (s *Store) Pin(ctx context.Context, id string, pinned bool) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE runs SET pinned = ? WHERE id = ?`, boolToInt(pinned), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errs.New(errs.RunNotFound, "run %q not found", id)
		}
		return nil
	})
}

// Delete removes a run and its steps (cascade via foreign key). Returns
// errs.RunNotFound if id does not exist.
func (s *Store) Delete(ctx context.Context, id string) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE id = ?`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errs.New(errs.RunNotFound, "run %q not found", id)
		}
		return nil
	})
}

// Purge deletes unpinned runs beyond the keep newest (by started DESC) and
// unpinned runs older than maxAge, cascading to their steps. keep <= 0
// disables the count-based rule; maxAge <= 0 disables the age-based rule.
// It returns the number of runs deleted.
func (s *Store) Purge(ctx context.Context, keep int, maxAge time.Duration) (int, error) {
	var deleted int
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		toDelete := map[string]struct{}{}

		if keep > 0 {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM runs WHERE pinned = 0 ORDER BY started DESC, id DESC`)
			if err != nil {
				return err
			}
			var ids []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			if len(ids) > keep {
				for _, id := range ids[keep:] {
					toDelete[id] = struct{}{}
				}
			}
		}

		if maxAge > 0 {
			cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339Nano)
			rows, err := tx.QueryContext(ctx, `SELECT id FROM runs WHERE pinned = 0 AND started < ?`, cutoff)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				toDelete[id] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}

		if len(toDelete) == 0 {
			return nil
		}

		ids := make([]string, 0, len(toDelete))
		args := make([]any, 0, len(toDelete))
		for id := range toDelete {
			ids = append(ids, id)
			args = append(args, id)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		res, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		deleted = int(n)
		return nil
	})
	return deleted, err
}

// Stats summarizes the run store's contents.
type Stats struct {
	Total    int                      `json:"total"`
	ByStatus map[domain.RunStatus]int `json:"by_status"`
	Oldest   time.Time                `json:"oldest,omitempty"`
	Newest   time.Time                `json:"newest,omitempty"`
}

// Stats returns aggregate counts across all runs.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	st := Stats{ByStatus: map[domain.RunStatus]int{}}
	err := s.db.Read(ctx, func(conn *sql.Conn) error {
		row := conn.QueryRowContext(ctx, `SELECT COUNT(*), MIN(started), MAX(started) FROM runs`)
		var oldest, newest sql.NullString
		if err := row.Scan(&st.Total, &oldest, &newest); err != nil {
			return err
		}
		var err error
		if st.Oldest, err = nullToTime(oldest); err != nil {
			return err
		}
		if st.Newest, err = nullToTime(newest); err != nil {
			return err
		}

		rows, err := conn.QueryContext(ctx, `SELECT status, COUNT(*) FROM runs GROUP BY status`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var status string
			var n int
			if err := rows.Scan(&status, &n); err != nil {
				return err
			}
			st.ByStatus[domain.RunStatus(status)] = n
		}
		return rows.Err()
	})
	return st, err
}

// --- scanning helpers ---

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func scanRun(sc rowScanner) (*domain.Run, error) {
	var (
		id                        string
		flowID, flowSnapshot, env sql.NullString
		inputsJSON                string
		status                    string
		started, finished         sql.NullString
		durationMs                sql.NullInt64
		summaryJSON               string
		pinnedInt                 int
		trigger                   sql.NullString
		errorJSON, opHashesJSON   sql.NullString
	)
	if err := sc.Scan(&id, &flowID, &flowSnapshot, &env, &inputsJSON, &status, &started, &finished, &durationMs, &summaryJSON, &pinnedInt, &trigger, &errorJSON, &opHashesJSON); err != nil {
		return nil, err
	}

	run := &domain.Run{
		ID:           id,
		FlowID:       store.StringOrEmpty(flowID),
		FlowSnapshot: store.StringOrEmpty(flowSnapshot),
		Environment:  store.StringOrEmpty(env),
		Status:       domain.RunStatus(status),
		Pinned:       pinnedInt != 0,
		Trigger:      store.StringOrEmpty(trigger),
	}
	if durationMs.Valid {
		run.DurationMs = durationMs.Int64
	}

	var err error
	if run.Started, err = nullToTime(started); err != nil {
		return nil, err
	}
	if run.Finished, err = nullToTime(finished); err != nil {
		return nil, err
	}
	if err := store.UnmarshalJSON(inputsJSON, &run.Inputs); err != nil {
		return nil, err
	}
	if err := store.UnmarshalJSON(summaryJSON, &run.Summary); err != nil {
		return nil, err
	}
	if errorJSON.Valid {
		var e domain.ErrorInfo
		if err := store.UnmarshalJSON(errorJSON.String, &e); err != nil {
			return nil, err
		}
		run.Error = &e
	}
	if opHashesJSON.Valid {
		if err := store.UnmarshalJSON(opHashesJSON.String, &run.OperationHashes); err != nil {
			return nil, err
		}
	}
	return run, nil
}

func loadSteps(ctx context.Context, conn *sql.Conn, runID string) ([]domain.StepResult, error) {
	rows, err := conn.QueryContext(ctx, `SELECT `+stepColumns+` FROM run_steps WHERE run_id = ? ORDER BY idx ASC, step_id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []domain.StepResult
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		steps = append(steps, st)
	}
	return steps, rows.Err()
}

func scanStep(sc rowScanner) (domain.StepResult, error) {
	var (
		stepID                                                                     string
		idx, attempts                                                              int
		status                                                                     string
		operation                                                                  sql.NullString
		requestJSON, responseJSON, timingsJSON, assertionsJSON, outJSON, errorJSON sql.NullString
		started, finished                                                          sql.NullString
	)
	if err := sc.Scan(&stepID, &idx, &status, &operation, &attempts, &requestJSON, &responseJSON, &timingsJSON, &assertionsJSON, &outJSON, &errorJSON, &started, &finished); err != nil {
		return domain.StepResult{}, err
	}

	step := domain.StepResult{
		StepID:    stepID,
		Index:     idx,
		Operation: store.StringOrEmpty(operation),
		Status:    domain.StepStatus(status),
		Attempts:  attempts,
	}

	var err error
	if step.Request, err = unmarshalPtrJSON[domain.RequestRecord](requestJSON); err != nil {
		return domain.StepResult{}, err
	}
	if step.Response, err = unmarshalPtrJSON[domain.ResponseRecord](responseJSON); err != nil {
		return domain.StepResult{}, err
	}
	if step.Timings, err = unmarshalPtrJSON[domain.Timings](timingsJSON); err != nil {
		return domain.StepResult{}, err
	}
	if step.Error, err = unmarshalPtrJSON[domain.ErrorInfo](errorJSON); err != nil {
		return domain.StepResult{}, err
	}
	if assertionsJSON.Valid {
		if err := store.UnmarshalJSON(assertionsJSON.String, &step.Assertions); err != nil {
			return domain.StepResult{}, err
		}
	}
	if outJSON.Valid {
		if err := store.UnmarshalJSON(outJSON.String, &step.Out); err != nil {
			return domain.StepResult{}, err
		}
	}
	if step.Started, err = nullToTime(started); err != nil {
		return domain.StepResult{}, err
	}
	if step.Finished, err = nullToTime(finished); err != nil {
		return domain.StepResult{}, err
	}
	return step, nil
}

// --- value conversion helpers ---

func timeToNull(t time.Time) sql.NullString {
	if t.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339Nano), Valid: true}
}

func nullToTime(s sql.NullString) (time.Time, error) {
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s.String)
}

// durationToNull stores duration_ms as NULL until the run has finished, since
// an in-flight run has no meaningful duration yet.
func durationToNull(finished time.Time, durationMs int64) sql.NullInt64 {
	if finished.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: durationMs, Valid: true}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// marshalPtrJSON marshals *v to a nullable JSON column, storing SQL NULL for
// a nil pointer.
func marshalPtrJSON[T any](v *T) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	s, err := store.MarshalJSON(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: s, Valid: true}, nil
}

// unmarshalPtrJSON is the inverse of marshalPtrJSON: SQL NULL decodes to a
// nil pointer.
func unmarshalPtrJSON[T any](s sql.NullString) (*T, error) {
	if !s.Valid {
		return nil, nil
	}
	var v T
	if err := store.UnmarshalJSON(s.String, &v); err != nil {
		return nil, err
	}
	return &v, nil
}
