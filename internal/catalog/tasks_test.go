package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/catalog"
	"github.com/gs-sinha/sapien/internal/domain"
)

func TestApply_ReplacesTaskIndexWithoutGhosts(t *testing.T) {
	db := openTestDB(t)
	c := catalog.New(db)
	snap := orderServiceSnapshot()
	snap.Tasks = []domain.Task{{ID: "complete-delivery", Phrases: []string{"proof of delivery"}, Targets: []domain.TaskTarget{{Operation: "order-service.createOrder"}}}}
	_, err := c.Apply(t.Context(), snap)
	require.NoError(t, err)

	tasks, err := c.ListTasks(t.Context(), "order-service")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "complete-delivery", tasks[0].ID)
	stats, err := c.Stats(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Tasks)

	snap.Tasks = []domain.Task{{ID: "cancel-order", Phrases: []string{"stop an order"}, Targets: []domain.TaskTarget{{Operation: "order-service.getOrder"}}}}
	_, err = c.Apply(t.Context(), snap)
	require.NoError(t, err)
	tasks, err = c.ListTasks(t.Context(), "order-service")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "cancel-order", tasks[0].ID)
	var ghosts int
	require.NoError(t, db.SQL().QueryRow(`SELECT COUNT(*) FROM tasks_fts WHERE tasks_fts MATCH 'delivery'`).Scan(&ghosts))
	assert.Zero(t, ghosts)

	_, err = db.SQL().Exec(`DELETE FROM tasks_fts`)
	require.NoError(t, err)
	require.NoError(t, c.Reindex(t.Context()))
	var rebuilt int
	require.NoError(t, db.SQL().QueryRow(`SELECT COUNT(*) FROM tasks_fts WHERE tasks_fts MATCH 'stop'`).Scan(&rebuilt))
	assert.Equal(t, 1, rebuilt)
}
