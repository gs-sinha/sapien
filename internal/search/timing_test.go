package search_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/search"
)

// fillerOperations generates n synthetic, distinct operations spread across
// several services, none of which mention "allocate" or "rider", so the
// timing query still has to rank the real logistics fixture out of a large
// candidate pool.
func fillerOperations(n int) []opFixture {
	out := make([]opFixture, 0, n)
	services := []string{"billing-service", "catalog-service", "inventory-service", "payments-service", "notification-service"}
	for i := 0; i < n; i++ {
		svc := services[i%len(services)]
		id := fmt.Sprintf("%s.op%d", svc, i)
		out = append(out, opFixture{
			id: id, serviceID: "svc_filler_" + svc, serviceName: svc,
			method: "GET", path: fmt.Sprintf("/v1/resource%d/{id}", i),
			rawOpID:     fmt.Sprintf("op%d", i),
			summary:     fmt.Sprintf("Fetch resource %d by id", i),
			description: fmt.Sprintf("Returns resource %d and its metadata for the %s catalog.", i, svc),
			tags:        []string{"catalog", svc},
		})
	}
	return out
}

func TestOperations_TimingAtScale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in -short mode")
	}

	db := openTestDB(t)
	fixtures := append(logisticsOperations(), fillerOperations(2000)...)
	seedOperations(t, db, fixtures)
	s := search.New(db)
	ctx := context.Background()

	const runs = 20
	durations := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		results, err := s.Operations(ctx, "allocate rider", domain.SearchOptions{})
		require.NoError(t, err)
		require.NotEmpty(t, results)
		durations = append(durations, time.Since(start))
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95Idx := int(float64(len(durations))*0.95) - 1
	if p95Idx < 0 {
		p95Idx = 0
	}
	p95 := durations[p95Idx]
	t.Logf("p95 over %d runs at %d operations: %s", runs, len(fixtures), p95)
	budget := 50 * time.Millisecond
	if raceEnabled {
		budget = 500 * time.Millisecond // PLAN §32's 50 ms is the production budget; -race plus a loaded machine is not
	}
	require.Lessf(t, p95, budget, "p95 latency %s exceeded %s budget", p95, budget)
}
