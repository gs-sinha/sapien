package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/registry"
)

const minimalOpenAPI = `
openapi: 3.1.0
info:
  title: Minimal
  version: "1.0.0"
paths: {}
`

const validServiceYAML = `
version: 1
name: allocation-service
owners: [allocation-platform]
concepts: [rider allocation]
tasks:
  - phrase: allocate a rider, dispatch an order
    operation: allocate
environments:
  local: { base_url: http://localhost:8080 }
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func fixturesRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..", "fixtures", "logistics")
}

func TestDiscoverPackage_APIDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "openapi.yaml"), minimalOpenAPI)
	writeFile(t, filepath.Join(root, "api", "service.yaml"), validServiceYAML)

	pkg, err := registry.DiscoverPackage(root, "")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(root, "api"), pkg.Dir)
	require.Len(t, pkg.Contracts, 1)
	assert.Equal(t, filepath.Join(root, "api", "openapi.yaml"), pkg.Contracts[0])
	assert.Equal(t, filepath.Join(root, "api", "service.yaml"), pkg.MetadataFile)
}

func TestDiscoverPackage_APIDir_ServiceYAMLOnly(t *testing.T) {
	// api/ qualifies on service.yaml alone, even with no openapi.* file yet
	// (e.g. a custom contract name declared in service.yaml).
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "api", "service.yaml"), validServiceYAML)

	pkg, err := registry.DiscoverPackage(root, "")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(root, "api"), pkg.Dir)
	assert.Empty(t, pkg.Contracts)
	assert.Equal(t, filepath.Join(root, "api", "service.yaml"), pkg.MetadataFile)
}

func TestDiscoverPackage_RootOpenAPI(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "openapi.yaml"), minimalOpenAPI)

	pkg, err := registry.DiscoverPackage(root, "")
	require.NoError(t, err)

	assert.Equal(t, root, pkg.Dir)
	require.Len(t, pkg.Contracts, 1)
	assert.Equal(t, filepath.Join(root, "openapi.yaml"), pkg.Contracts[0])
	assert.Empty(t, pkg.MetadataFile)
}

func TestDiscoverPackage_DocsSubdir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "openapi.json"), `{"openapi":"3.1.0","info":{"title":"Minimal","version":"1.0.0"},"paths":{}}`)

	pkg, err := registry.DiscoverPackage(root, "")
	require.NoError(t, err)

	// The package directory is the root, not the docs/ subdirectory --
	// docs/, flows/, memories/ are always resolved relative to it.
	assert.Equal(t, root, pkg.Dir)
	require.Len(t, pkg.Contracts, 1)
	assert.Equal(t, filepath.Join(root, "docs", "openapi.json"), pkg.Contracts[0])
}

func TestDiscoverPackage_Override(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "custom", "spec.yaml"), minimalOpenAPI)
	// A decoy default contract that must NOT be picked when an override is given.
	writeFile(t, filepath.Join(root, "openapi.yaml"), minimalOpenAPI)

	pkg, err := registry.DiscoverPackage(root, "custom/spec.yaml")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(root, "custom"), pkg.Dir)
	require.Len(t, pkg.Contracts, 1)
	assert.Equal(t, filepath.Join(root, "custom", "spec.yaml"), pkg.Contracts[0])

	// Absolute override path works too.
	abs := filepath.Join(root, "custom", "spec.yaml")
	pkg2, err := registry.DiscoverPackage(root, abs)
	require.NoError(t, err)
	assert.Equal(t, pkg.Dir, pkg2.Dir)
	assert.Equal(t, pkg.Contracts, pkg2.Contracts)
}

func TestDiscoverPackage_OverrideMissing(t *testing.T) {
	root := t.TempDir()

	_, err := registry.DiscoverPackage(root, "does/not/exist.yaml")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))
}

func TestDiscoverPackage_RootIsThePackage(t *testing.T) {
	// Calling DiscoverPackage directly on a fixture's api/ directory (as
	// opposed to its repo root) must be accepted the same way a bare
	// "openapi.yaml in root" layout is.
	apiDir := filepath.Join(fixturesRoot(t), "allocation-service", "api")

	pkg, err := registry.DiscoverPackage(apiDir, "")
	require.NoError(t, err)

	assert.Equal(t, apiDir, pkg.Dir)
	require.Len(t, pkg.Contracts, 1)
	assert.Equal(t, filepath.Join(apiDir, "openapi.yaml"), pkg.Contracts[0])
	assert.Equal(t, filepath.Join(apiDir, "service.yaml"), pkg.MetadataFile)
	assert.Equal(t, filepath.Join(apiDir, "docs"), pkg.DocsDir)
	assert.Equal(t, filepath.Join(apiDir, "flows"), pkg.FlowsDir)
}

func TestDiscoverPackage_Missing(t *testing.T) {
	root := t.TempDir()

	_, err := registry.DiscoverPackage(root, "")
	require.Error(t, err)
	assert.Equal(t, errs.ServiceSource, errs.CodeOf(err))

	e := errs.As(err)
	assert.NotEmpty(t, e.Hint)
}

func TestLoadMetadata_Absent(t *testing.T) {
	pkg := &registry.Package{Dir: t.TempDir()}
	meta, err := registry.LoadMetadata(pkg)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceMetadata{}, meta)
}

func TestLoadMetadata_Valid(t *testing.T) {
	root := t.TempDir()
	metaPath := filepath.Join(root, "service.yaml")
	writeFile(t, metaPath, validServiceYAML)

	pkg := &registry.Package{Dir: root, MetadataFile: metaPath}
	meta, err := registry.LoadMetadata(pkg)
	require.NoError(t, err)

	assert.Equal(t, "allocation-service", meta.Name)
	assert.Equal(t, []string{"allocation-platform"}, meta.Owners)
	assert.Equal(t, []string{"rider allocation"}, meta.Concepts)
	require.Len(t, meta.Tasks, 1)
	assert.Equal(t, "allocate", meta.Tasks[0].Operation)
	require.Contains(t, meta.Environments, "local")
	assert.Equal(t, "http://localhost:8080", meta.Environments["local"].BaseURL)
}

func TestLoadMetadata_Invalid_HasLine(t *testing.T) {
	root := t.TempDir()
	metaPath := filepath.Join(root, "service.yaml")
	// "endpoint" is not a recognized key (see spec/examples).
	writeFile(t, metaPath, "version: 1\nname: allocation-service\nendpoint: http://localhost:8080\n")

	pkg := &registry.Package{Dir: root, MetadataFile: metaPath}
	_, err := registry.LoadMetadata(pkg)
	require.Error(t, err)
	assert.Equal(t, errs.Invalid, errs.CodeOf(err))

	e := errs.As(err)
	require.NotNil(t, e.Source)
	assert.Greater(t, e.Source.Line, 0)
	assert.Equal(t, metaPath, e.Source.File)
}

func TestServiceName(t *testing.T) {
	cases := []struct {
		name        string
		ref         domain.ServiceRef
		meta        domain.ServiceMetadata
		ingestTitle string
		want        string
	}{
		{
			name: "ref wins",
			ref:  domain.ServiceRef{Name: "explicit-name"},
			meta: domain.ServiceMetadata{Name: "meta-name"},
			want: "explicit-name",
		},
		{
			name: "meta wins over title",
			meta: domain.ServiceMetadata{Name: "meta-name"},
			want: "meta-name",
		},
		{
			name:        "slug of title",
			ingestTitle: "Allocation Service!",
			want:        "allocation-service",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := registry.ServiceName(tc.ref, tc.meta, tc.ingestTitle)
			assert.Equal(t, tc.want, got)
		})
	}
}
