package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gs-sinha/sapien/internal/domain"
)

// taskOperationRanking searches authored caller phrases and maps matching
// tasks to their operation targets. Tests are absent from tasks_fts, so a
// held-out assertion cannot improve its own result.
func (s *Searcher) taskOperationRanking(ctx context.Context, tokens []string, service string) (map[string]float64, map[string][]domain.TaskMatch, error) {
	raw := map[string]float64{}
	andRows, err := s.ftsTasksRaw(ctx, BuildMatch(tokens, "and"), service)
	if err != nil {
		return nil, nil, err
	}
	for id, score := range normalizeBM25(andRows) {
		raw[id] += score
	}
	orRows, err := s.ftsTasksRaw(ctx, BuildMatch(tokens, "or"), service)
	if err != nil {
		return nil, nil, err
	}
	for id, score := range normalizeBM25(orRows) {
		raw[id] += score * 0.6
	}
	if len(raw) == 0 {
		return map[string]float64{}, map[string][]domain.TaskMatch{}, nil
	}

	taskIDs := make([]string, 0, len(raw))
	for id := range raw {
		taskIDs = append(taskIDs, id)
	}
	sort.Strings(taskIDs)
	ph, args := placeholdersFor(taskIDs)
	q := `SELECT tt.task_id, tt.operation_id, tt.when_text, t.raw_id, t.doc_json
	      FROM task_targets tt JOIN tasks t ON t.id = tt.task_id
	      WHERE tt.task_id IN (` + ph + `) ORDER BY tt.task_id, tt.ord`
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, wrapf(err, "load task targets")
	}
	defer rows.Close()

	scores := map[string]float64{}
	matches := map[string][]domain.TaskMatch{}
	for rows.Next() {
		var taskID, opID, when, rawID, docJSON string
		if err := rows.Scan(&taskID, &opID, &when, &rawID, &docJSON); err != nil {
			return nil, nil, wrapf(err, "scan task target")
		}
		var task domain.Task
		if err := json.Unmarshal([]byte(docJSON), &task); err != nil {
			return nil, nil, fmt.Errorf("search: decode task %q: %w", taskID, err)
		}
		scores[opID] += raw[taskID]
		matches[opID] = append(matches[opID], domain.TaskMatch{ID: rawID, Phrase: bestTaskPhrase(task.Phrases, tokens), When: when})
	}
	return scores, matches, wrapf(rows.Err(), "load task targets rows")
}

func (s *Searcher) ftsTasksRaw(ctx context.Context, match, service string) (map[string]float64, error) {
	out := map[string]float64{}
	if match == "" {
		return out, nil
	}
	q := `SELECT task_id, bm25(tasks_fts, 0, 1, 8) FROM tasks_fts WHERE tasks_fts MATCH ?`
	args := []any{match}
	if service != "" {
		q += ` AND lower(service) = lower(?)`
		args = append(args, service)
	}
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, wrapf(err, "tasks fts query")
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, wrapf(err, "scan tasks fts row")
		}
		out[id] = score
	}
	return out, wrapf(rows.Err(), "tasks fts rows")
}

func bestTaskPhrase(phrases, tokens []string) string {
	best, bestHits := "", -1
	for _, phrase := range phrases {
		lower, hits := strings.ToLower(phrase), 0
		for _, token := range tokens {
			if strings.Contains(lower, token) {
				hits++
			}
		}
		if hits > bestHits {
			best, bestHits = phrase, hits
		}
	}
	return best
}
