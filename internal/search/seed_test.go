package search_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/store"
	"github.com/growsimplee/sapien/internal/textutil"
)

// openTestDB opens a fresh, migrated in-memory store for one test.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---- FTS row derivation, honestly reproducing the catalog contract the
// task spec pins down, using the same internal/textutil helpers the real
// catalog indexer uses. This is deliberately duplicated here (rather than
// imported) since internal/catalog does not exist yet.

// opIDColumn builds operations_fts.op_id: Join(SplitIdent(fullID)) + " " +
// fullID + " " + rawOpID.
func opIDColumn(fullID, rawOpID string) string {
	return textutil.Join(textutil.SplitIdent(fullID)) + " " + fullID + " " + rawOpID
}

// pathTokensColumn builds operations_fts.path_tokens: Join(PathTokens(path))
// + " " + raw path.
func pathTokensColumn(path string) string {
	return textutil.Join(textutil.PathTokens(path)) + " " + path
}

// namesColumn builds operations_fts.{tags,param_names,field_names}:
// Join(Tokens(names)) over the space-joined name list.
func namesColumn(names []string) string {
	return textutil.Join(textutil.Tokens(strings.Join(names, " ")))
}

// opFixture is one seeded operation, covering the operations, operations_fts,
// operations_trigram, operation_aliases, and (optionally) fields tables.
type opFixture struct {
	id          string
	serviceID   string
	serviceName string
	method      string
	path        string
	rawOpID     string
	summary     string
	description string
	tags        []string
	concepts    []string // service.yaml concepts, copied onto every op of the service by real ingest
	deprecated  bool
	params      []domain.Param
	fieldPaths  []string // extra fields-table rows (operation_id, field_path)

	// docText/memoryText are written verbatim to operations_fts'
	// doc_text/memory_text columns (search ranking tuning task, part 1:
	// folding docs and memories into the operations index). Real callers
	// go through internal/catalog.RefreshOperationKnowledge; seeding them
	// directly here is an honest stand-in the same way every other column
	// above is derived with the exact helpers the real indexer uses.
	docText    string
	memoryText string
}

// logisticsOperations is the shared operation fixture used by every
// Operations() test: three services (order-service, allocation-service,
// rider-service), 8 operations, mirroring PLAN.md §16's examples.
func logisticsOperations() []opFixture {
	return []opFixture{
		{
			id: "order-service.createOrder", serviceID: "svc_order", serviceName: "order-service",
			method: "POST", path: "/v1/orders", rawOpID: "createOrder",
			summary: "Create an order", description: "Places a new order for a customer.",
			tags:     []string{"orders", "write"},
			concepts: []string{"orders", "delivery timeline", "order lifecycle"},
		},
		{
			id: "order-service.getOrder", serviceID: "svc_order", serviceName: "order-service",
			method: "GET", path: "/v1/orders/{orderId}", rawOpID: "getOrder",
			summary: "Get an order by ID", description: "Fetch a single order by its identifier.",
			tags:     []string{"orders", "read"},
			concepts: []string{"orders", "delivery timeline", "order lifecycle"},
			params:   []domain.Param{{Name: "orderId", In: domain.InPath, Required: true}},
		},
		{
			id: "order-service.cancelOrder", serviceID: "svc_order", serviceName: "order-service",
			method: "DELETE", path: "/v1/orders/{orderId}", rawOpID: "cancelOrder",
			summary: "Cancel an order", description: "Cancels an order before it is delivered.",
			tags:     []string{"orders", "write"},
			concepts: []string{"orders", "delivery timeline", "order lifecycle"},
			params:   []domain.Param{{Name: "orderId", In: domain.InPath, Required: true}},
		},
		{
			id: "allocation-service.allocate", serviceID: "svc_alloc", serviceName: "allocation-service",
			method: "POST", path: "/v1/allocations", rawOpID: "allocate",
			summary: "Allocate a rider to an order",
			description: "Picks an eligible, online rider for the given order. For QCOM orders, " +
				"only riders with qcomSkill=true are eligible. Returns 409 NO_RIDER_AVAILABLE if none are free.",
			tags:     []string{"allocation", "write"},
			concepts: []string{"rider allocation", "dispatch", "matching", "qcom"},
		},
		{
			id: "allocation-service.allocateV1", serviceID: "svc_alloc", serviceName: "allocation-service",
			method: "POST", path: "/v1/allocations/v1", rawOpID: "allocateV1",
			summary: "Allocate a rider to an order (legacy)", description: "Deprecated legacy allocation endpoint.",
			tags:       []string{"allocation", "legacy"},
			concepts:   []string{"rider allocation", "dispatch", "matching", "qcom"},
			deprecated: true,
		},
		{
			id: "allocation-service.getAllocationStats", serviceID: "svc_alloc", serviceName: "allocation-service",
			method: "GET", path: "/v1/allocations/stats", rawOpID: "get_v1_allocations_stats",
			summary: "Get allocation stats", description: "Returns aggregate allocation statistics.",
			tags:     []string{"allocation", "stats"},
			concepts: []string{"rider allocation", "dispatch", "matching", "qcom"},
		},
		{
			id: "rider-service.getRider", serviceID: "svc_rider", serviceName: "rider-service",
			method: "GET", path: "/v1/riders/{riderId}", rawOpID: "getRider",
			summary: "Get a rider by ID", description: "Fetch a single rider profile.",
			tags:     []string{"riders", "read"},
			concepts: []string{"riders", "availability", "qcom skill"},
			params:   []domain.Param{{Name: "riderId", In: domain.InPath, Required: true}},
		},
		{
			id: "rider-service.searchRiders", serviceID: "svc_rider", serviceName: "rider-service",
			method: "POST", path: "/v1/riders/search", rawOpID: "searchRiders",
			summary:     "Find riders around a pickup location matching skill and radius",
			description: "Searches for available riders near a pickup point.",
			tags:        []string{"riders", "search"},
			concepts:    []string{"riders", "availability", "qcom skill"},
			fieldPaths:  []string{"request.body.qcomSkill", "request.body.radiuskm"},
		},
	}
}

// seedOperations inserts fixtures into services, operations, operation_aliases,
// operations_fts, operations_trigram, and fields, deriving every FTS column
// via the same textutil helpers the catalog indexer uses (query.go / the
// task spec's contract), so the seed is an honest stand-in for the real
// indexer rather than a shortcut.
func seedOperations(t *testing.T, db *store.DB, fixtures []opFixture) {
	t.Helper()
	ctx := context.Background()

	err := db.Write(ctx, func(tx *sql.Tx) error {
		seenService := map[string]bool{}
		for _, f := range fixtures {
			if !seenService[f.serviceID] {
				seenService[f.serviceID] = true
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO services (id, name) VALUES (?, ?)`,
					f.serviceID, f.serviceName); err != nil {
					return err
				}
			}

			paramNames := make([]string, len(f.params))
			for i, p := range f.params {
				paramNames[i] = p.Name
			}

			var fieldLeafNames []string
			for _, fp := range f.fieldPaths {
				leaf := fp
				if i := strings.LastIndexByte(leaf, '.'); i >= 0 {
					leaf = leaf[i+1:]
				}
				fieldLeafNames = append(fieldLeafNames, leaf)
			}

			op := domain.Operation{
				ID:          f.id,
				ServiceID:   f.serviceID,
				Protocol:    domain.ProtocolHTTP,
				HTTP:        &domain.HTTPBinding{Method: f.method, Path: f.path},
				RawOpID:     f.rawOpID,
				Summary:     f.summary,
				Description: f.description,
				Tags:        f.tags,
				Concepts:    f.concepts,
				Params:      f.params,
				Deprecated:  f.deprecated,
				Hash:        "seed-" + f.id,
			}
			docJSON, err := store.MarshalJSON(op)
			if err != nil {
				return err
			}

			deprecatedInt := 0
			if f.deprecated {
				deprecatedInt = 1
			}
			tagsJSON, err := store.MarshalJSON(f.tags)
			if err != nil {
				return err
			}

			if _, err := tx.ExecContext(ctx, `
				INSERT INTO operations
					(id, service_id, protocol, method, path, raw_op_id, summary, description, tags_json, deprecated, hash, doc_json)
				VALUES (?, ?, 'http', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				f.id, f.serviceID, f.method, f.path, f.rawOpID, f.summary, f.description, tagsJSON, deprecatedInt, "seed-"+f.id, docJSON,
			); err != nil {
				return err
			}

			if _, err := tx.ExecContext(ctx,
				`INSERT INTO operation_aliases (method, path, operation_id) VALUES (?, ?, ?)`,
				f.method, f.path, f.id); err != nil {
				return err
			}

			tagsAndConcepts := append(append([]string{}, f.tags...), f.concepts...)

			if _, err := tx.ExecContext(ctx, `
				INSERT INTO operations_fts (id, service, op_id, path_tokens, summary, description, tags, param_names, field_names, doc_text, memory_text)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				f.id, f.serviceName, opIDColumn(f.id, f.rawOpID), pathTokensColumn(f.path),
				f.summary, f.description, namesColumn(tagsAndConcepts), namesColumn(paramNames), namesColumn(fieldLeafNames),
				f.docText, f.memoryText,
			); err != nil {
				return err
			}

			if _, err := tx.ExecContext(ctx,
				`INSERT INTO operations_trigram (id, op_id, path) VALUES (?, ?, ?)`,
				f.id, strings.ToLower(f.id), strings.ToLower(f.path)); err != nil {
				return err
			}

			for _, fp := range f.fieldPaths {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO fields (operation_id, field_path, type) VALUES (?, ?, 'string')`,
					f.id, fp); err != nil {
					return err
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// docSectionFixture is one seeded doc_sections row.
type docSectionFixture struct {
	id      string
	ord     int
	heading string
	level   int
	body    string
	refs    []domain.DocRef
}

// docFixture is one seeded docs row plus its sections.
type docFixture struct {
	id          string
	serviceID   string
	serviceName string
	path        string
	title       string
	sections    []docSectionFixture
}

// logisticsDocs is the shared doc fixture: 2 docs, 4 sections, with one
// operation ref each on two of the sections. "allocate" is tagged onto the
// allocate operation's tags fixture so that Operations("allocation rules")
// (an OR fallback, since "rules" never appears on any operation) still
// surfaces allocation-service.allocate for the docs ref-boost test.
func logisticsDocs() []docFixture {
	return []docFixture{
		{
			id: "doc_allocation", serviceID: "svc_alloc", serviceName: "allocation-service",
			path: "allocation.md", title: "Allocation Guide",
			sections: []docSectionFixture{
				{
					id: "sec_allocation_rules", ord: 0, heading: "Allocation rules", level: 2,
					body: "Riders are allocated to orders within a rolling window based on skill match. " +
						"See the allocation rules for retry timing.",
					refs: []domain.DocRef{{Kind: domain.RefOperation, Value: "allocation-service.allocate"}},
				},
				{
					id: "sec_retry_policy", ord: 1, heading: "Retry policy", level: 2,
					body: "If allocation fails the system retries up to three times before escalating.",
				},
			},
		},
		{
			id: "doc_riders", serviceID: "svc_rider", serviceName: "rider-service",
			path: "riders.md", title: "Rider Matching Guide",
			sections: []docSectionFixture{
				{
					id: "sec_rider_filters", ord: 0, heading: "Rider search filters", level: 2,
					body: "Search riders using qcomSkill and radius filters to narrow candidates.",
					refs: []domain.DocRef{{Kind: domain.RefOperation, Value: "rider-service.searchRiders"}},
				},
				{
					id: "sec_rider_profile", ord: 1, heading: "Rider profile fields", level: 2,
					body: "Each rider profile includes contact info and vehicle details.",
				},
			},
		},
	}
}

// seedDocs inserts fixtures into docs, doc_sections, doc_refs, and docs_fts.
// It does not insert services; call seedOperations (or seed the relevant
// services directly) first when the doc fixtures reference service ids that
// aren't already present.
func seedDocs(t *testing.T, db *store.DB, fixtures []docFixture) {
	t.Helper()
	ctx := context.Background()

	err := db.Write(ctx, func(tx *sql.Tx) error {
		for _, d := range fixtures {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO docs (id, service_id, path, title) VALUES (?, ?, ?, ?)`,
				d.id, d.serviceID, d.path, d.title); err != nil {
				return err
			}
			for _, sec := range d.sections {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO doc_sections (id, doc_id, ord, heading, level, body) VALUES (?, ?, ?, ?, ?, ?)`,
					sec.id, d.id, sec.ord, sec.heading, sec.level, sec.body); err != nil {
					return err
				}
				for _, r := range sec.refs {
					if _, err := tx.ExecContext(ctx,
						`INSERT INTO doc_refs (section_id, kind, value) VALUES (?, ?, ?)`,
						sec.id, string(r.Kind), r.Value); err != nil {
						return err
					}
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO docs_fts (section_id, service, title, heading, body) VALUES (?, ?, ?, ?, ?)`,
					sec.id, d.serviceName, d.title, sec.heading, sec.body); err != nil {
					return err
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
}

// feedbackFixture is one seeded search_feedback row.
type feedbackFixture struct {
	term  string
	opID  string
	count int
}

// seedFeedback inserts fixtures into search_feedback, the table
// internal/engine/local's usage-feedback recorder writes and
// internal/search's lexical ranking reads (search ranking tuning task,
// part 2).
func seedFeedback(t *testing.T, db *store.DB, fixtures []feedbackFixture) {
	t.Helper()
	ctx := context.Background()

	err := db.Write(ctx, func(tx *sql.Tx) error {
		for _, f := range fixtures {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO search_feedback (term, op_id, count, updated) VALUES (?, ?, ?, ?)`,
				f.term, f.opID, f.count, "2026-09-05T00:00:00Z",
			); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, err)
}
