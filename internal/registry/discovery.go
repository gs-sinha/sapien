package registry

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/spec"
)

// serviceMetadataFile is the well-known service metadata file name (PLAN §6).
const serviceMetadataFile = "service.yaml"

// openAPIFileNames are the contract file names DiscoverPackage looks for, in
// preference order, when no explicit override or service.yaml contracts list
// applies.
var openAPIFileNames = []string{"openapi.yaml", "openapi.yml", "openapi.json"}

// Package is a discovered API package on disk (PLAN §6): the directory that
// holds a service's contract(s), optional metadata, and optional
// docs/flows/memories subtrees.
type Package struct {
	Dir          string   // absolute
	Contracts    []string // absolute paths; the auto-discovered (or overridden) default contract set
	MetadataFile string   // absolute path of service.yaml, or "" if absent
	DocsDir      string   // absolute path of docs/, or "" if absent
	FlowsDir     string   // absolute path of flows/, or "" if absent
	MemoriesDir  string   // absolute path of memories/, or "" if absent
}

// DiscoverPackage locates a service's API package under root (PLAN §6).
//
// Discovery order: "<root>/api/" (accepted if it holds an openapi.{yaml,yml,json}
// file or a service.yaml), else an openapi.{yaml,yml,json} file directly in
// root (root itself is the package -- this also covers the case where root
// already points at the package directory), else the same file inside
// root/docs, root/spec, or root/openapi, in that order.
//
// contractOverride (relative to root, or absolute) wins over all of the
// above and nothing else is searched: the package directory becomes the
// override file's directory.
//
// Returns errs.ServiceSource, with a hint listing what was looked for, when
// no package can be found (or the override does not exist).
func DiscoverPackage(root string, contractOverride string) (*Package, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "resolving package root %q", root)
	}

	if contractOverride != "" {
		p := contractOverride
		if !filepath.IsAbs(p) {
			p = filepath.Join(absRoot, p)
		}
		info, statErr := os.Stat(p)
		if statErr != nil || info.IsDir() {
			return nil, errs.New(errs.ServiceSource, "contract override not found: %s", p).
				WithDetail("path", p).
				WithHint("--contract must be a file, relative to the service root or absolute")
		}
		return buildPackage(filepath.Dir(p), p), nil
	}

	apiDir := filepath.Join(absRoot, domain.ServicePackageDir)
	if f := findOpenAPIFile(apiDir); f != "" {
		return buildPackage(apiDir, f), nil
	}
	if hasFile(filepath.Join(apiDir, serviceMetadataFile)) {
		return buildPackage(apiDir, ""), nil
	}

	if f := findOpenAPIFile(absRoot); f != "" {
		return buildPackage(absRoot, f), nil
	}

	for _, sub := range []string{"docs", "spec", "openapi"} {
		if f := findOpenAPIFile(filepath.Join(absRoot, sub)); f != "" {
			return buildPackage(absRoot, f), nil
		}
	}

	return nil, errs.New(errs.ServiceSource, "no API package found under %s", absRoot).
		WithDetail("root", absRoot).
		WithHint("looked for api/openapi.{yaml,yml,json} (or api/service.yaml), " +
			"openapi.{yaml,yml,json} in the root, and docs/, spec/, openapi/ subdirectories; " +
			"pass --contract to point at the file directly")
}

// buildPackage assembles a Package rooted at dir, with contract (if non-empty)
// as its sole default contract, filling in MetadataFile/DocsDir/FlowsDir/
// MemoriesDir from whatever exists on disk.
func buildPackage(dir, contract string) *Package {
	pkg := &Package{Dir: dir}
	if contract != "" {
		pkg.Contracts = []string{contract}
	}
	if meta := filepath.Join(dir, serviceMetadataFile); hasFile(meta) {
		pkg.MetadataFile = meta
	}
	if d := filepath.Join(dir, "docs"); isDir(d) {
		pkg.DocsDir = d
	}
	if d := filepath.Join(dir, domain.FlowsDir); isDir(d) {
		pkg.FlowsDir = d
	}
	if d := filepath.Join(dir, domain.MemoriesDir); isDir(d) {
		pkg.MemoriesDir = d
	}
	return pkg
}

// findOpenAPIFile returns the absolute path of the first openAPIFileNames
// entry present in dir, or "" if none is.
func findOpenAPIFile(dir string) string {
	for _, name := range openAPIFileNames {
		p := filepath.Join(dir, name)
		if hasFile(p) {
			return p
		}
	}
	return ""
}

func hasFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// LoadMetadata reads and validates pkg's service.yaml against the spec.Service
// schema. It returns a zero-value ServiceMetadata and a nil error when
// pkg.MetadataFile is empty (no service.yaml present). A validation failure
// is returned as errs.Invalid, annotated with the source line of the first
// problem when one can be found.
func LoadMetadata(pkg *Package) (domain.ServiceMetadata, error) {
	if pkg.MetadataFile == "" {
		return domain.ServiceMetadata{}, nil
	}

	data, err := os.ReadFile(pkg.MetadataFile)
	if err != nil {
		return domain.ServiceMetadata{}, errs.Wrap(errs.Internal, err, "reading %s", pkg.MetadataFile)
	}

	if problems := spec.ValidateYAML(spec.Service, data); len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.String()
		}
		e := errs.New(errs.Invalid, "service.yaml: %s", strings.Join(msgs, "; ")).
			WithDetail("file", pkg.MetadataFile).
			WithDetail("problems", msgs)
		if problems[0].Line > 0 {
			e = e.WithSource(domain.SourceLoc{File: pkg.MetadataFile, Line: problems[0].Line})
		}
		return domain.ServiceMetadata{}, e
	}

	var meta domain.ServiceMetadata
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return domain.ServiceMetadata{}, errs.Wrap(errs.Invalid, err, "parsing %s", pkg.MetadataFile)
	}
	return meta, nil
}

// ServiceName picks a service's name: ref.Name, else meta.Name, else a slug
// of ingestTitle (the ingested contract's info.title).
func ServiceName(ref domain.ServiceRef, meta domain.ServiceMetadata, ingestTitle string) string {
	if ref.Name != "" {
		return ref.Name
	}
	if meta.Name != "" {
		return meta.Name
	}
	return slugify(ingestTitle)
}

// slugify lower-cases s and collapses runs of non [a-z0-9] characters into a
// single '-', trimming any trailing '-'. It returns "" for a blank or
// all-punctuation input.
func slugify(s string) string {
	var b strings.Builder
	prevDash := true // avoid a leading dash
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
