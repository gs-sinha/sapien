package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/errs"
	"github.com/growsimplee/sapien/internal/registry"
)

// fixtureWorkspace returns a workspace rooted at fixtures/logistics, so a
// ServiceRef{Source: {Kind: local, Path: "<service>-service"}} resolves
// directly to that fixture's directory.
func fixtureWorkspace(t *testing.T) *domain.Workspace {
	t.Helper()
	return &domain.Workspace{Version: 1, Name: "logistics", Dir: fixturesRoot(t)}
}

func fixtureRef(name string) domain.ServiceRef {
	return domain.ServiceRef{
		Name:   name,
		Source: domain.Source{Kind: domain.SourceLocal, Path: name},
	}
}

func findOp(ops []domain.Operation, id string) (domain.Operation, bool) {
	for _, o := range ops {
		if o.ID == id {
			return o, true
		}
	}
	return domain.Operation{}, false
}

func findDoc(docsList []domain.Doc, path string) (domain.Doc, bool) {
	for _, d := range docsList {
		if d.Path == path {
			return d, true
		}
	}
	return domain.Doc{}, false
}

func docHasRef(d domain.Doc, kind domain.RefKind, value string) bool {
	for _, sec := range d.Sections {
		for _, r := range sec.Refs {
			if r.Kind == kind && r.Value == value {
				return true
			}
		}
	}
	return false
}

func TestBuilder_Build_Fixtures(t *testing.T) {
	ws := fixtureWorkspace(t)
	b := registry.NewBuilder(ws)

	for _, name := range []string{"allocation-service", "order-service", "rider-service"} {
		t.Run(name, func(t *testing.T) {
			snap, err := b.Build(context.Background(), fixtureRef(name))
			require.NoError(t, err)
			require.NotNil(t, snap)

			assert.Equal(t, name, snap.Service.Name)
			assert.Equal(t, name, snap.Service.ID)
			assert.Equal(t, domain.SyncOK, snap.Service.Status)
			assert.Greater(t, len(snap.Operations), 0)
			assert.Equal(t, len(snap.Operations), snap.Service.OperationCount)
			assert.NotEmpty(t, snap.Service.Description)

			require.Contains(t, snap.ContractFiles, "openapi.yaml")
			assert.Len(t, snap.ContractFiles["openapi.yaml"], 64) // sha256 hex
			assert.Equal(t, []string{"openapi.yaml"}, snap.Service.ContractFiles)
		})
	}
}

func TestBuilder_Build_AllocationService_SynthesizedAndDeprecated(t *testing.T) {
	ws := fixtureWorkspace(t)
	b := registry.NewBuilder(ws)

	snap, err := b.Build(context.Background(), fixtureRef("allocation-service"))
	require.NoError(t, err)

	synth, ok := findOp(snap.Operations, "allocation-service.get_v1_allocations_stats")
	require.True(t, ok, "expected a synthesized operation id for GET /v1/allocations/stats")
	assert.True(t, synth.Synthesized)

	dep, ok := findOp(snap.Operations, "allocation-service.allocateV1")
	require.True(t, ok)
	assert.True(t, dep.Deprecated)
}

func TestBuilder_Build_Docs_Refs(t *testing.T) {
	ws := fixtureWorkspace(t)
	b := registry.NewBuilder(ws)

	allocSnap, err := b.Build(context.Background(), fixtureRef("allocation-service"))
	require.NoError(t, err)

	allocDoc, ok := findDoc(allocSnap.Docs, "docs/allocation.md")
	require.True(t, ok)
	assert.True(t, docHasRef(allocDoc, domain.RefOperation, "allocation-service.allocate"),
		"expected docs/allocation.md to reference allocation-service.allocate")

	riderSnap, err := b.Build(context.Background(), fixtureRef("rider-service"))
	require.NoError(t, err)

	riderDoc, ok := findDoc(riderSnap.Docs, "docs/riders.md")
	require.True(t, ok)
	assert.True(t, docHasRef(riderDoc, domain.RefField, "qcomSkill"),
		"expected docs/riders.md to reference the qcomSkill field")

	// Contract-embedded docs (info/tag descriptions) are re-parsed through
	// docs.Parse and must carry Refs too, not just a single unstructured
	// section.
	infoDoc, ok := findDoc(riderSnap.Docs, "contract#info")
	require.True(t, ok)
	assert.Equal(t, domain.DocSourceContractInfo, infoDoc.Source)
	assert.True(t, docHasRef(infoDoc, domain.RefField, "qcomSkill"),
		"expected the embedded contract#info doc to carry Refs")
}

func TestBuilder_Build_SmokeFlow(t *testing.T) {
	ws := fixtureWorkspace(t)
	b := registry.NewBuilder(ws)

	snap, err := b.Build(context.Background(), fixtureRef("allocation-service"))
	require.NoError(t, err)

	require.Len(t, snap.Flows, 1)
	flow := snap.Flows[0]
	assert.Equal(t, "smoke", flow.ID)
	assert.Equal(t, "Allocation smoke test", flow.Name)
	assert.Equal(t, "service", flow.OwnerKind)
	assert.Equal(t, "allocation-service", flow.OwnerID)
	assert.Equal(t, "flows/smoke.flow.yaml", flow.Path)
	assert.Equal(t, []string{"allocation-service.allocate", "allocation-service.getAllocation"}, flow.Operations)
	assert.Equal(t, 2, flow.StepCount)
	assert.NotEmpty(t, flow.Hash)
	assert.False(t, flow.Updated.IsZero())
}

func TestBuilder_Build_GitSourceNotImplemented(t *testing.T) {
	ws := fixtureWorkspace(t)
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{
		Name:   "allocation-service",
		Source: domain.Source{Kind: domain.SourceGit, URL: "git@example.com:x/y.git"},
	}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.NotImplemented, errs.CodeOf(err))
	assert.Contains(t, err.Error(), "git sources arrive in Phase 5")
}

func TestBuilder_Build_ContractParseError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), "not: valid: yaml: [")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Name: "broken", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.ContractParse, errs.CodeOf(err))
}

func TestBuilder_Build_DescriptionFallsBackToIngest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), `
openapi: 3.1.0
info:
  title: Widget Service
  version: "1.0.0"
  description: |
    Widgets, but described in the contract only.
    Second line of the description.
paths: {}
`)
	// No service.yaml at all -- Description must fall back to the ingest
	// description's first line.

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Name: "widget-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	snap, err := b.Build(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "Widgets, but described in the contract only.", snap.Service.Description)
}

func TestBuilder_Build_MergeAcrossFiles_DuplicateWarning(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "one.yaml"), `
openapi: 3.1.0
info: { title: Two Files, version: "1.0.0" }
paths:
  /v1/widgets:
    get:
      operationId: listWidgets
      summary: List widgets
      responses: { "200": { description: ok } }
`)
	writeFile(t, filepath.Join(root, "api", "two.yaml"), `
openapi: 3.1.0
info: { title: Two Files, version: "1.0.0" }
paths:
  /v1/widgets/{id}:
    get:
      operationId: listWidgets
      summary: Get a widget
      responses: { "200": { description: ok } }
`)
	writeFile(t, filepath.Join(root, "api", "service.yaml"), `
version: 1
name: two-files-service
contracts: [one.yaml, two.yaml]
`)

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Name: "two-files-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	snap, err := b.Build(context.Background(), ref)
	require.NoError(t, err)

	// Both operations are appended, uncollided/unrenamed, across both files.
	require.Len(t, snap.Operations, 2)
	assert.Equal(t, "two-files-service.listWidgets", snap.Operations[0].ID)
	assert.Equal(t, "two-files-service.listWidgets", snap.Operations[1].ID)

	require.Len(t, snap.Service.Warnings, 1)
	assert.Equal(t, "DUPLICATE_OPERATION_ID", snap.Service.Warnings[0].Code)

	assert.ElementsMatch(t, []string{"one.yaml", "two.yaml"}, snap.Service.ContractFiles)
}

func TestBuilder_Build_UnsupportedSourceKind(t *testing.T) {
	ws := fixtureWorkspace(t)
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Name: "x", Source: domain.Source{Kind: "weird"}}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestBuilder_Build_NameInferredFromTitle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), `
openapi: 3.1.0
info:
  title: Gadget Service
  version: "1.0.0"
paths: {}
`)
	// No service.yaml, and the workspace entry has no explicit name either:
	// the name must be inferred from the ingested contract's info.title.
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	snap, err := b.Build(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, "gadget-service", snap.Service.Name)
}

func TestBuilder_Build_NameUndeterminable(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), `
openapi: 3.1.0
info:
  version: "1.0.0"
paths: {}
`)
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestBuilder_Build_DeclaredContractMissing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), minimalOpenAPI)
	writeFile(t, filepath.Join(root, "api", "service.yaml"), "version: 1\nname: x\ncontracts: [missing.yaml]\n")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Name: "x", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

func TestBuilder_Build_NoContractFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "service.yaml"), "version: 1\nname: x\n")

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Name: "x", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

func TestBuilder_Build_ServiceSourceMissing(t *testing.T) {
	ws := &domain.Workspace{Version: 1, Name: "w", Dir: t.TempDir()}
	b := registry.NewBuilder(ws)

	ref := domain.ServiceRef{Name: "nope", Source: domain.Source{Kind: domain.SourceLocal, Path: "does-not-exist"}}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

func TestBuilder_Build_ContextCancelled(t *testing.T) {
	ws := fixtureWorkspace(t)
	b := registry.NewBuilder(ws)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := b.Build(ctx, fixtureRef("allocation-service"))
	require.Error(t, err)
}

// textPlainOpenAPI declares one operation with no summary (MISSING_SUMMARY)
// whose 200 response is text/plain (UNSUPPORTED_MEDIA_TYPE), so a build
// against it always emits exactly those two warnings -- used to exercise
// service.yaml's accepted_warnings partitioning.
const textPlainOpenAPI = `
openapi: 3.1.0
info:
  title: Text Plain Service
  version: "1.0.0"
paths:
  /v1/status:
    get:
      operationId: getStatus
      responses:
        "200":
          description: ok
          content:
            text/plain:
              schema: { type: string }
`

func TestBuilder_Build_AcceptedWarnings_ByCode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), textPlainOpenAPI)
	writeFile(t, filepath.Join(root, "api", "service.yaml"), `
version: 1
name: text-service
accepted_warnings:
  - code: UNSUPPORTED_MEDIA_TYPE
    reason: "This endpoint returns text/plain by design; the contract describes the wire faithfully."
`)

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)
	ref := domain.ServiceRef{Name: "text-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	snap, err := b.Build(context.Background(), ref)
	require.NoError(t, err)

	// Status stays ok: acceptance never changes sync status.
	assert.Equal(t, domain.SyncOK, snap.Service.Status)

	require.Len(t, snap.Service.AcceptedWarnings, 1)
	assert.Equal(t, "UNSUPPORTED_MEDIA_TYPE", snap.Service.AcceptedWarnings[0].Code)
	assert.Equal(t, "This endpoint returns text/plain by design; the contract describes the wire faithfully.",
		snap.Service.AcceptedWarnings[0].Reason)

	// MISSING_SUMMARY was never accepted, so it stays in Warnings.
	require.Len(t, snap.Service.Warnings, 1)
	assert.Equal(t, "MISSING_SUMMARY", snap.Service.Warnings[0].Code)
}

func TestBuilder_Build_AcceptedWarnings_ByMatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), textPlainOpenAPI)

	t.Run("match on message accepts", func(t *testing.T) {
		writeFile(t, filepath.Join(root, "api", "service.yaml"), `
version: 1
name: text-service
accepted_warnings:
  - code: UNSUPPORTED_MEDIA_TYPE
    match: "TEXT/PLAIN"
    reason: "Faithful to the wire."
`)
		ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
		b := registry.NewBuilder(ws)
		ref := domain.ServiceRef{Name: "text-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
		snap, err := b.Build(context.Background(), ref)
		require.NoError(t, err)
		require.Len(t, snap.Service.AcceptedWarnings, 1)
		assert.Equal(t, "UNSUPPORTED_MEDIA_TYPE", snap.Service.AcceptedWarnings[0].Code)
	})

	t.Run("match that misses leaves the warning unaccepted and flags the entry stale", func(t *testing.T) {
		writeFile(t, filepath.Join(root, "api", "service.yaml"), `
version: 1
name: text-service
accepted_warnings:
  - code: UNSUPPORTED_MEDIA_TYPE
    match: "application/xml"
    reason: "Never actually matches this fixture."
`)
		ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
		b := registry.NewBuilder(ws)
		ref := domain.ServiceRef{Name: "text-service", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
		snap, err := b.Build(context.Background(), ref)
		require.NoError(t, err)

		assert.Empty(t, snap.Service.AcceptedWarnings)

		var codes []string
		for _, w := range snap.Service.Warnings {
			codes = append(codes, w.Code)
		}
		assert.Contains(t, codes, "UNSUPPORTED_MEDIA_TYPE")
		assert.Contains(t, codes, "STALE_ACCEPTANCE")

		var stale domain.LintWarning
		for _, w := range snap.Service.Warnings {
			if w.Code == "STALE_ACCEPTANCE" {
				stale = w
			}
		}
		assert.Contains(t, stale.Message, "UNSUPPORTED_MEDIA_TYPE")
		assert.Contains(t, stale.Message, `"application/xml"`)
		assert.Contains(t, stale.Message, "remove it")
	})
}

func TestBuilder_Build_AcceptedWarnings_StaleWhenCodeNeverFires(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), minimalOpenAPI)
	writeFile(t, filepath.Join(root, "api", "service.yaml"), `
version: 1
name: x
accepted_warnings:
  - code: SOME_CODE_THAT_NEVER_FIRES
    reason: "Stale by construction."
`)

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)
	ref := domain.ServiceRef{Name: "x", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	snap, err := b.Build(context.Background(), ref)
	require.NoError(t, err)

	require.Len(t, snap.Service.Warnings, 1)
	assert.Equal(t, "STALE_ACCEPTANCE", snap.Service.Warnings[0].Code)
	assert.Empty(t, snap.Service.AcceptedWarnings)
}

func TestBuilder_Build_AcceptedWarnings_MissingReasonRejectedBySchema(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), minimalOpenAPI)
	writeFile(t, filepath.Join(root, "api", "service.yaml"), `
version: 1
name: x
accepted_warnings:
  - code: UNSUPPORTED_MEDIA_TYPE
`)

	ws := &domain.Workspace{Version: 1, Name: "w", Dir: root}
	b := registry.NewBuilder(ws)
	ref := domain.ServiceRef{Name: "x", Source: domain.Source{Kind: domain.SourceLocal, Path: "."}}
	_, err := b.Build(context.Background(), ref)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))
}

func TestPackageFingerprint_ChangesOnDocEdit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), minimalOpenAPI)
	writeFile(t, filepath.Join(root, "api", "docs", "a.md"), "# A\n\nHello.\n")

	pkg, err := registry.DiscoverPackage(root, "")
	require.NoError(t, err)

	fp1, err := registry.PackageFingerprint(pkg)
	require.NoError(t, err)
	require.NotEmpty(t, fp1)

	// Re-discovering should be stable when nothing changed.
	pkg2, err := registry.DiscoverPackage(root, "")
	require.NoError(t, err)
	fp1b, err := registry.PackageFingerprint(pkg2)
	require.NoError(t, err)
	assert.Equal(t, fp1, fp1b)

	require.NoError(t, os.WriteFile(filepath.Join(root, "api", "docs", "a.md"), []byte("# A\n\nHello, changed.\n"), 0o644))

	fp2, err := registry.PackageFingerprint(pkg)
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2)
}
