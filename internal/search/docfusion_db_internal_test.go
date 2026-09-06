package search

// Integration-level tests for the docs-fusion experiment's wiring through
// Operations() end to end (search ranking tuning task). These live in the
// internal (package search) test binary, rather than alongside the
// project's usual search_test-package integration tests (docs_test.go,
// operations_test.go, ...), for one reason: exercising a specific
// SAPIEN_SEARCH_DOC_FUSION value deterministically needs docFusionOverride
// (see its comment in docfusion.go), which is unexported — not reachable
// from an external _test package. Fixtures here are seeded with plain SQL
// (a reduced version of seed_test.go's opFixture/docFixture machinery,
// which lives in the search_test package and so isn't importable here
// either), just enough to build the scenario each test needs.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/store"
	"github.com/gs-sinha/sapien/internal/textutil"
)

// withDocFusionOverride sets docFusionOverride for the duration of the
// calling test, restoring the previous value on cleanup. See
// docFusionOverride's doc comment for why this is necessary instead of
// t.Setenv.
func withDocFusionOverride(t *testing.T, cfg docFusionConfig) {
	t.Helper()
	old := docFusionOverride
	docFusionOverride = &cfg
	t.Cleanup(func() { docFusionOverride = old })
}

func openFusionTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fusionOp is one minimal seeded operation for these tests.
type fusionOp struct {
	id, serviceID, serviceName string
	method, path, rawOpID      string
	summary, description       string
}

func seedFusionOp(t *testing.T, db *store.DB, f fusionOp) {
	t.Helper()
	ctx := context.Background()
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO services (id, name) VALUES (?, ?)`, f.serviceID, f.serviceName); err != nil {
			return err
		}
		op := domain.Operation{
			ID: f.id, ServiceID: f.serviceID, Protocol: domain.ProtocolHTTP,
			HTTP:    &domain.HTTPBinding{Method: f.method, Path: f.path},
			RawOpID: f.rawOpID, Summary: f.summary, Description: f.description,
			Hash: "seed-" + f.id,
		}
		docJSON, err := store.MarshalJSON(op)
		if err != nil {
			return err
		}
		tagsJSON, err := store.MarshalJSON([]string{})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO operations
				(id, service_id, protocol, method, path, raw_op_id, summary, description, tags_json, deprecated, hash, doc_json)
			VALUES (?, ?, 'http', ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
			f.id, f.serviceID, f.method, f.path, f.rawOpID, f.summary, f.description, tagsJSON, "seed-"+f.id, docJSON,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO operation_aliases (method, path, operation_id) VALUES (?, ?, ?)`,
			f.method, f.path, f.id); err != nil {
			return err
		}
		opIDCol := textutil.Join(textutil.SplitIdent(f.id)) + " " + f.id + " " + f.rawOpID
		pathCol := textutil.Join(textutil.PathTokens(f.path)) + " " + f.path
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names, doc_text, memory_text)
			VALUES (?, ?, ?, ?, ?, ?, '', '', '', '', '')`,
			f.id, f.serviceName, opIDCol, pathCol, f.summary, f.description,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO operations_trigram (id, op_id, path) VALUES (?, ?, ?)`,
			f.id, strings.ToLower(f.id), strings.ToLower(f.path)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed fusion op %s: %v", f.id, err)
	}
}

// fusionDocSection is one minimal seeded doc section, optionally referencing
// operations via doc_refs.
type fusionDocSection struct {
	docID, serviceID, serviceName, docPath, docTitle string
	sectionID, heading, body                         string
	refOps                                           []string
}

func seedFusionDoc(t *testing.T, db *store.DB, f fusionDocSection) {
	t.Helper()
	ctx := context.Background()
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO services (id, name) VALUES (?, ?)`, f.serviceID, f.serviceName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO docs (id, service_id, path, title) VALUES (?, ?, ?, ?)`,
			f.docID, f.serviceID, f.docPath, f.docTitle); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO doc_sections (id, doc_id, ord, heading, level, body) VALUES (?, ?, 0, ?, 2, ?)`,
			f.sectionID, f.docID, f.heading, f.body); err != nil {
			return err
		}
		for _, opID := range f.refOps {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO doc_refs (section_id, kind, value) VALUES (?, 'operation', ?)`,
				f.sectionID, opID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO docs_fts (section_id, service, title, heading, body) VALUES (?, ?, ?, ?, ?)`,
			f.sectionID, f.serviceName, f.docTitle, f.heading, f.body); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed fusion doc section %s: %v", f.sectionID, err)
	}
}

// TestOperations_DocFusionOff_DefaultBehaviorUnchanged confirms that with no
// override and no env var set (the default in every other test in this
// package), a query with zero lexical/trigram signal still returns nothing
// — i.e. moving the doc-fusion computation ahead of the early "nothing
// matched" return (so an on-flag query can be rescued) did not change the
// off-flag behavior at all.
func TestOperations_DocFusionOff_NoLexicalSignalReturnsNil(t *testing.T) {
	// Fusion is on by default now (weighted 0.7); pin it off for this test,
	// which checks the operation-only ranking has no signal for the query.
	withDocFusionOverride(t, docFusionConfig{mode: docFusionOff})
	db := openFusionTestDB(t)
	seedFusionOp(t, db, fusionOp{
		id: "shipping-service.trackShipment", serviceID: "svc_ship", serviceName: "shipping-service",
		method: "GET", path: "/v1/shipments/{id}/track", rawOpID: "trackShipment",
		summary: "Track a shipment", description: "Returns the current tracking status for a shipment.",
	})
	seedFusionDoc(t, db, fusionDocSection{
		docID: "doc_ship", serviceID: "svc_ship", serviceName: "shipping-service",
		docPath: "shipping.md", docTitle: "Shipping Guide",
		sectionID: "sec_carrier_delay", heading: "Carrier delay handling",
		body:   "When a carrier reports a delay, the shipment tracker flags it for support review.",
		refOps: []string{"shipping-service.trackShipment"},
	})

	s := New(db)
	results, err := s.Operations(context.Background(), "carrier delay", domain.SearchOptions{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results with doc fusion off (no lexical/trigram signal at all), got %+v", results)
	}
}

// TestOperations_DocFusionRRF_RescuesDocOnlyCandidate is the core
// docs-fusion hypothesis test at unit scale: a query with zero lexical or
// trigram signal for any operation, but a docs_fts hit whose section
// references one operation, must surface that operation — labelled "docs"
// — once SAPIEN_SEARCH_DOC_FUSION=rrf is active.
func TestOperations_DocFusionRRF_RescuesDocOnlyCandidate(t *testing.T) {
	db := openFusionTestDB(t)
	seedFusionOp(t, db, fusionOp{
		id: "shipping-service.trackShipment", serviceID: "svc_ship", serviceName: "shipping-service",
		method: "GET", path: "/v1/shipments/{id}/track", rawOpID: "trackShipment",
		summary: "Track a shipment", description: "Returns the current tracking status for a shipment.",
	})
	seedFusionOp(t, db, fusionOp{
		id: "shipping-service.getShipment", serviceID: "svc_ship", serviceName: "shipping-service",
		method: "GET", path: "/v1/shipments/{id}", rawOpID: "getShipment",
		summary: "Get a shipment by ID", description: "Fetch a single shipment record.",
	})
	seedFusionDoc(t, db, fusionDocSection{
		docID: "doc_ship", serviceID: "svc_ship", serviceName: "shipping-service",
		docPath: "shipping.md", docTitle: "Shipping Guide",
		sectionID: "sec_carrier_delay", heading: "Carrier delay handling",
		body:   "When a carrier reports a delay, the shipment tracker flags it for support review.",
		refOps: []string{"shipping-service.trackShipment"},
	})

	withDocFusionOverride(t, docFusionConfig{mode: docFusionRRF, scope: docFusionScopeAll})

	s := New(db)
	results, err := s.Operations(context.Background(), "carrier delay", domain.SearchOptions{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected the docs ranker to rescue trackShipment; got no results")
	}
	idx := indexOfID(results, "shipping-service.trackShipment")
	if idx < 0 {
		t.Fatalf("trackShipment must be a result: %+v", results)
	}
	if !containsStr(results[idx].MatchedOn, "docs") {
		t.Errorf("expected matched_on to contain %q for a doc-fusion-rescued operation, got %v", "docs", results[idx].MatchedOn)
	}
	for _, r := range results {
		if r.Operation.ID == "shipping-service.getShipment" {
			t.Errorf("getShipment has no doc_refs and no lexical signal for this query; it must not appear: %+v", results)
		}
	}
}

// TestOperations_DocFusionWeighted_TiltsRankingTowardDocs seeds two
// operations that both match the query lexically (so both are lexical
// candidates), with the lexical index alone ranking "getShipment" first
// (its op_id/summary echo the query terms more directly) — but a docs
// section strongly, and exclusively, references "trackShipment". A high
// lexical weight (0.9) must keep the lexical order; a low one (0.1) must
// flip it.
func TestOperations_DocFusionWeighted_TiltsRankingTowardDocs(t *testing.T) {
	newDB := func(t *testing.T) *store.DB {
		db := openFusionTestDB(t)
		seedFusionOp(t, db, fusionOp{
			id: "shipping-service.getShipment", serviceID: "svc_ship", serviceName: "shipping-service",
			method: "GET", path: "/v1/shipments/{id}", rawOpID: "getShipment",
			summary: "Get shipment status", description: "Fetch the current status of a shipment.",
		})
		seedFusionOp(t, db, fusionOp{
			id: "shipping-service.trackShipment", serviceID: "svc_ship", serviceName: "shipping-service",
			method: "GET", path: "/v1/shipments/{id}/track", rawOpID: "trackShipment",
			summary: "Track shipment status history", description: "Returns the status history of a shipment.",
		})
		seedFusionDoc(t, db, fusionDocSection{
			docID: "doc_ship", serviceID: "svc_ship", serviceName: "shipping-service",
			docPath: "shipping.md", docTitle: "Shipping Guide",
			sectionID: "sec_status", heading: "Shipment status",
			body:   "Shipment status queries are the most common support request; status is best read from the track endpoint.",
			refOps: []string{"shipping-service.trackShipment"},
		})
		return db
	}

	t.Run("mostly lexical keeps lexical order", func(t *testing.T) {
		db := newDB(t)
		withDocFusionOverride(t, docFusionConfig{mode: docFusionWeighted, weight: 0.9, scope: docFusionScopeAll})
		s := New(db)
		results, err := s.Operations(context.Background(), "shipment status", domain.SearchOptions{})
		if err != nil {
			t.Fatalf("Operations: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		if results[0].Operation.ID != "shipping-service.getShipment" {
			t.Errorf("weight 0.9 (mostly lexical): top = %s, want getShipment", results[0].Operation.ID)
		}
	})

	t.Run("mostly docs flips to the docs-favored operation", func(t *testing.T) {
		db := newDB(t)
		withDocFusionOverride(t, docFusionConfig{mode: docFusionWeighted, weight: 0.1, scope: docFusionScopeAll})
		s := New(db)
		results, err := s.Operations(context.Background(), "shipment status", domain.SearchOptions{})
		if err != nil {
			t.Fatalf("Operations: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}
		if results[0].Operation.ID != "shipping-service.trackShipment" {
			t.Errorf("weight 0.1 (mostly docs): top = %s, want trackShipment", results[0].Operation.ID)
		}
	})
}

// TestHeadingScopedOps_RestrictsToHeadingAndFirstParagraph exercises the
// cross-talk control end to end against a real DB: one section references
// two operations via doc_refs, but only one of them is actually named in
// the section's heading/first paragraph — the other is named only later in
// the body. Under scope=heading, only the named-early operation should
// survive as an allowed vote for that section.
func TestHeadingScopedOps_RestrictsToHeadingAndFirstParagraph(t *testing.T) {
	db := openFusionTestDB(t)
	seedFusionOp(t, db, fusionOp{
		id: "shipping-service.trackShipment", serviceID: "svc_ship", serviceName: "shipping-service",
		method: "GET", path: "/v1/shipments/{id}/track", rawOpID: "trackShipment",
		summary: "Track a shipment", description: "Returns tracking status.",
	})
	seedFusionOp(t, db, fusionOp{
		id: "shipping-service.cancelShipment", serviceID: "svc_ship", serviceName: "shipping-service",
		method: "POST", path: "/v1/shipments/{id}/cancel", rawOpID: "cancelShipment",
		summary: "Cancel a shipment", description: "Cancels an in-flight shipment.",
	})
	seedFusionDoc(t, db, fusionDocSection{
		docID: "doc_ship", serviceID: "svc_ship", serviceName: "shipping-service",
		docPath: "shipping.md", docTitle: "Shipping Guide",
		sectionID: "sec_mixed", heading: "Tracking a shipment",
		body: "Use trackShipment to poll delivery status in real time.\n\n" +
			"Unrelated aside: cancelShipment exists for a separate cancellation flow, described elsewhere.",
		refOps: []string{"shipping-service.trackShipment", "shipping-service.cancelShipment"},
	})

	s := New(db)
	ctx := context.Background()
	refsBySection, err := s.loadDocRefs(ctx, []string{"sec_mixed"})
	if err != nil {
		t.Fatalf("loadDocRefs: %v", err)
	}

	allowed, err := s.headingScopedOps(ctx, []string{"sec_mixed"}, refsBySection)
	if err != nil {
		t.Fatalf("headingScopedOps: %v", err)
	}

	got := allowed["sec_mixed"]
	if !got["shipping-service.trackShipment"] {
		t.Errorf("trackShipment is named in the heading/first paragraph; expected it allowed. allowed=%v", got)
	}
	if got["shipping-service.cancelShipment"] {
		t.Errorf("cancelShipment is named only in the second paragraph; expected it excluded. allowed=%v", got)
	}
}

// indexOfID returns the index of the result whose Operation.ID == id, or -1.
func indexOfID(results []domain.SearchResult, id string) int {
	for i, r := range results {
		if r.Operation.ID == id {
			return i
		}
	}
	return -1
}

// containsStr reports whether s is an element of list.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestOperations_DefaultFusion_RescuesDocOnlyCandidate pins the shipped
// default: with SAPIEN_SEARCH_DOC_FUSION unset, a query with no lexical or
// trigram signal but a docs hit whose section references one operation
// surfaces that operation, labelled "docs".
func TestOperations_DefaultFusion_RescuesDocOnlyCandidate(t *testing.T) {
	withDocFusionOverride(t, defaultDocFusionConfig())
	db := openFusionTestDB(t)
	seedFusionOp(t, db, fusionOp{
		id: "shipping-service.trackShipment", serviceID: "svc_ship", serviceName: "shipping-service",
		method: "GET", path: "/v1/shipments/{id}/track", rawOpID: "trackShipment",
		summary: "Track a shipment", description: "Returns the current tracking status for a shipment.",
	})
	seedFusionDoc(t, db, fusionDocSection{
		docID: "doc_ship", serviceID: "svc_ship", serviceName: "shipping-service",
		docPath: "shipping.md", docTitle: "Shipping Guide",
		sectionID: "sec_carrier_delay", heading: "Carrier delay handling",
		body:   "When a carrier reports a delay, the shipment tracker flags it for support review.",
		refOps: []string{"shipping-service.trackShipment"},
	})

	s := New(db)
	results, err := s.Operations(context.Background(), "carrier delay", domain.SearchOptions{})
	if err != nil {
		t.Fatalf("Operations: %v", err)
	}
	if len(results) != 1 || results[0].Operation.ID != "shipping-service.trackShipment" {
		t.Fatalf("expected the doc-referenced operation, got %+v", results)
	}
	found := false
	for _, m := range results[0].MatchedOn {
		if m == "docs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected matched_on to contain docs, got %v", results[0].MatchedOn)
	}
}
