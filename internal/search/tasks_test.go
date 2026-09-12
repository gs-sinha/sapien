package search_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/search"
	"github.com/gs-sinha/sapien/internal/store"
)

func TestOperations_TaskPhraseRanksTargetAndExplainsWhy(t *testing.T) {
	db := openTestDB(t)
	seedOperations(t, db, logisticsOperations())
	task := domain.Task{ID: "complete-delivery", Phrases: []string{"mark a delivery as delivered", "complete a drop", "proof of delivery"}, Targets: []domain.TaskTarget{{Operation: "order-service.createOrder", When: "customer handoff is complete"}}}
	docJSON, err := store.MarshalJSON(task)
	require.NoError(t, err)
	require.NoError(t, db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO tasks (id, service_id, raw_id, doc_json) VALUES ('task.complete', 'svc_order', 'complete-delivery', ?)`, docJSON); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO task_targets (task_id, operation_id, when_text, ord) VALUES ('task.complete', 'order-service.createOrder', 'customer handoff is complete', 0)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO tasks_fts (task_id, service, phrases) VALUES ('task.complete', 'order-service', 'mark a delivery as delivered complete a drop proof of delivery')`)
		return err
	}))

	results, err := search.New(db).Operations(context.Background(), "proof of delivery", domain.SearchOptions{Service: "order-service", Limit: 3})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "order-service.createOrder", results[0].Operation.ID)
	assert.Contains(t, results[0].MatchedOn, "task:complete-delivery")
	require.Len(t, results[0].Tasks, 1)
	assert.Equal(t, "proof of delivery", results[0].Tasks[0].Phrase)
	assert.Equal(t, "customer handoff is complete", results[0].Tasks[0].When)

	filtered, err := search.New(db).Operations(context.Background(), "proof of delivery", domain.SearchOptions{Service: "ORDER-SERVICE", Method: "DELETE", Limit: 3})
	require.NoError(t, err)
	for _, result := range filtered {
		assert.NotEqual(t, "order-service.createOrder", result.Operation.ID)
	}
}
