package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
)

func TestNormalizeTasks_ConciseAndAdvancedForms(t *testing.T) {
	ops := []domain.Operation{{ID: "delivery.completeTrip"}, {ID: "delivery.completeTripFromApp"}}
	authored := []domain.Task{
		{Phrase: "mark delivered, proof of delivery", Operation: "completeTrip"},
		{
			ID:      "finish-drop",
			Phrases: []string{"finish the customer handoff"},
			Targets: []domain.TaskTarget{{Operation: "completeTrip", When: "console"}, {Operation: "completeTripFromApp", When: "rider app"}},
			Tests:   []domain.TaskTest{{Query: "finish parcel handoff", ExpectAny: []string{"completeTrip", "completeTripFromApp"}}},
		},
	}

	tasks, coverage, warnings := normalizeTasks("delivery", authored, ops)
	require.Len(t, tasks, 2)
	assert.Equal(t, "mark-delivered", tasks[0].ID)
	assert.Equal(t, []string{"mark delivered", "proof of delivery"}, tasks[0].Phrases)
	assert.Equal(t, "delivery.completeTrip", tasks[0].Targets[0].Operation)
	assert.Equal(t, []string{"delivery.completeTrip", "delivery.completeTripFromApp"}, tasks[1].Tests[0].ExpectAny)
	assert.Equal(t, 3, tasks[1].Tests[0].TopK)
	assert.Equal(t, domain.TaskCoverage{Tasks: 2, Assertions: 1}, coverage)
	assert.Empty(t, warnings)
}

func TestNormalizeTasks_WarnsOnStaleAndAmbiguousTargets(t *testing.T) {
	tasks, _, warnings := normalizeTasks("delivery", []domain.Task{{ID: "finish", Phrases: []string{"finish"}, Targets: []domain.TaskTarget{{Operation: "missing"}, {Operation: "alsoMissing", When: "app"}}}}, nil)
	require.Len(t, tasks, 1)
	codes := []string{}
	for _, warning := range warnings {
		codes = append(codes, warning.Code)
	}
	assert.Contains(t, codes, "STALE_TASK_TARGET")
	assert.Contains(t, codes, "AMBIGUOUS_TASK")
}
