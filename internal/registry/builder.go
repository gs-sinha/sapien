package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/gs-sinha/sapien/internal/gitsrc"
	"github.com/gs-sinha/sapien/internal/ingest/docs"
	"github.com/gs-sinha/sapien/internal/ingest/openapi"
	"github.com/gs-sinha/sapien/internal/workspace"
)

// Builder turns one workspace service reference into a Snapshot.
type Builder struct {
	ws  *domain.Workspace
	git *gitsrc.Manager // optional; required for git-sourced services (PLAN §18)
}

// NewBuilder returns a Builder that resolves service sources relative to ws.
func NewBuilder(ws *domain.Workspace) *Builder {
	return &Builder{ws: ws}
}

// WithGit sets the git manager Build uses to resolve git-sourced services.
// Optional: when unset, a git source still returns errs.NotImplemented, as
// before Phase 5's gitsrc package existed.
func (b *Builder) WithGit(m *gitsrc.Manager) *Builder {
	b.git = m
	return b
}

// mergedContracts is the accumulated result of ingesting every contract file
// belonging to one service.
type mergedContracts struct {
	Title       string
	Version     string
	Description string
	Operations  []domain.Operation
	Schemas     []domain.NamedSchema
	Fields      []domain.Field
	Aliases     []domain.Alias
	Docs        []domain.Doc
	Warnings    []domain.LintWarning
}

// Build resolves ref's source, discovers its API package, loads its
// metadata, ingests its contract(s), and assembles docs and flow summaries
// into a Snapshot.
//
// A git source requires WithGit to have been called; without a Manager it
// returns errs.NotImplemented, as it always has. With one, Build resolves
// the managed clone via Manager.Ensure (cloning it on first use; PLAN §18)
// and discovers the package under its PackageDir, honoring
// ref.Source.Contract the same way a local source does. The resolved
// commit is recorded on the returned Service.
//
// A contract parse failure is returned as errs.ContractParse (or whatever
// code the failing step already uses); the caller (Syncer) is responsible
// for turning that into a MarkServiceError call.
func (b *Builder) Build(ctx context.Context, ref domain.ServiceRef) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var root string
	var gitCommit string

	switch ref.Source.Kind {
	case domain.SourceGit:
		if b.git == nil {
			return nil, errs.New(errs.NotImplemented, "git sources arrive in Phase 5").
				WithDetail("service", ref.Name)
		}
		checkout, err := b.git.Ensure(ctx, ref.Source)
		if err != nil {
			return nil, err
		}
		root = checkout.PackageDir
		gitCommit = checkout.Commit
	case domain.SourceLocal:
		r, err := workspace.ResolveSourcePath(b.ws, ref.Source)
		if err != nil {
			return nil, err
		}
		root = r
	default:
		return nil, errs.New(errs.Invalid, "service %q: unsupported source kind %q", ref.Name, ref.Source.Kind)
	}

	pkg, err := DiscoverPackage(root, ref.Source.Contract)
	if err != nil {
		return nil, err
	}

	meta, err := LoadMetadata(pkg)
	if err != nil {
		return nil, err
	}

	contracts, err := resolveContracts(pkg, ref.Source.Contract, meta)
	if err != nil {
		return nil, err
	}

	name := ref.Name
	if name == "" {
		name = meta.Name
	}

	var merged *mergedContracts
	if name != "" {
		merged, err = ingestContracts(name, pkg.Dir, contracts, meta.Concepts)
		if err != nil {
			return nil, err
		}
	} else {
		// Neither the workspace entry nor service.yaml names the service;
		// ingest once under a placeholder id to learn the contract's title,
		// then re-ingest under the real, derived name so operation ids are
		// prefixed correctly.
		peek, peekErr := ingestContracts("_pending", pkg.Dir, contracts, meta.Concepts)
		if peekErr != nil {
			return nil, peekErr
		}
		name = ServiceName(ref, meta, peek.Title)
		if name == "" {
			return nil, errs.New(errs.Invalid, "unable to determine a name for the service at %s", pkg.Dir).
				WithHint("set `name` in the workspace entry or in service.yaml")
		}
		merged, err = ingestContracts(name, pkg.Dir, contracts, meta.Concepts)
		if err != nil {
			return nil, err
		}
	}

	known := buildKnownRefs(name, meta, merged)

	finalDocs, err := buildDocs(name, pkg, merged, known)
	if err != nil {
		return nil, err
	}

	flows, err := scanFlows(name, pkg)
	if err != nil {
		return nil, err
	}

	contractHashes := make(map[string]string, len(contracts))
	relContracts := make([]string, 0, len(contracts))
	for _, c := range contracts {
		rel := relPath(pkg.Dir, c)
		h, herr := hashFileHex(c)
		if herr != nil {
			return nil, errs.Wrap(errs.Internal, herr, "hashing contract %s", c)
		}
		contractHashes[rel] = h
		relContracts = append(relContracts, rel)
	}
	sort.Strings(relContracts)

	description := meta.Description
	if description == "" {
		description = firstLine(merged.Description)
	}

	unaccepted, accepted := partitionWarnings(merged.Warnings, meta.AcceptedWarnings)

	svc := domain.Service{
		ID:               name,
		Name:             name,
		Description:      description,
		Owners:           meta.Owners,
		Concepts:         meta.Concepts,
		Source:           ref.Source,
		PackageDir:       pkg.Dir,
		ContractFiles:    relContracts,
		Environments:     meta.Environments,
		Status:           domain.SyncOK,
		Warnings:         unaccepted,
		AcceptedWarnings: accepted,
		LastIndexed:      time.Now(),
		Commit:           gitCommit,
		OperationCount:   len(merged.Operations),
	}

	return &Snapshot{
		Service:       svc,
		Operations:    merged.Operations,
		Schemas:       merged.Schemas,
		Fields:        merged.Fields,
		Aliases:       merged.Aliases,
		Docs:          finalDocs,
		Flows:         flows,
		ContractFiles: contractHashes,
	}, nil
}

// resolveContracts decides the final, absolute contract file list: the
// override alone when one was given, else service.yaml's `contracts:` list
// (resolved against pkg.Dir) when non-empty, else pkg's auto-discovered
// default.
func resolveContracts(pkg *Package, override string, meta domain.ServiceMetadata) ([]string, error) {
	if override != "" {
		return pkg.Contracts, nil
	}
	if len(meta.Contracts) > 0 {
		out := make([]string, 0, len(meta.Contracts))
		for _, c := range meta.Contracts {
			p := c
			if !filepath.IsAbs(p) {
				p = filepath.Join(pkg.Dir, p)
			}
			if !hasFile(p) {
				return nil, errs.New(errs.ServiceSource, "contract %q declared in service.yaml not found", c).
					WithDetail("path", p)
			}
			out = append(out, p)
		}
		return out, nil
	}
	if len(pkg.Contracts) == 0 {
		return nil, errs.New(errs.ServiceSource, "no contract files found in %s", pkg.Dir).
			WithHint("add an openapi.yaml, or declare `contracts:` in service.yaml")
	}
	return pkg.Contracts, nil
}

// ingestContracts ingests every file in contracts (in order) under a shared
// serviceID, merging their results. An operation id already seen in an
// earlier file is still appended (never renamed), but earns a
// DUPLICATE_OPERATION_ID warning.
func ingestContracts(serviceID, pkgDir string, contracts []string, concepts []string) (*mergedContracts, error) {
	merged := &mergedContracts{}
	seen := make(map[string]bool)
	first := true

	for _, c := range contracts {
		rel := relPath(pkgDir, c)
		res, err := openapi.IngestFile(c, openapi.Options{
			ServiceID: serviceID,
			File:      rel,
			Concepts:  concepts,
		})
		if err != nil {
			return nil, err
		}

		if first {
			merged.Title, merged.Version, merged.Description = res.Title, res.Version, res.Description
			first = false
		}

		for _, op := range res.Operations {
			if seen[op.ID] {
				merged.Warnings = append(merged.Warnings, domain.LintWarning{
					Code:    "DUPLICATE_OPERATION_ID",
					Message: fmt.Sprintf("operation id %q is also defined in %s", op.ID, rel),
					Source:  &domain.SourceLoc{File: rel},
				})
			}
			seen[op.ID] = true
			merged.Operations = append(merged.Operations, op)
		}
		merged.Schemas = append(merged.Schemas, res.Schemas...)
		merged.Fields = append(merged.Fields, res.Fields...)
		merged.Aliases = append(merged.Aliases, res.Aliases...)
		merged.Docs = append(merged.Docs, res.Docs...)
		merged.Warnings = append(merged.Warnings, res.Warnings...)
	}

	return merged, nil
}

// partitionWarnings splits warnings into those left for a reviewer
// (unaccepted) and those a service.yaml accepted_warnings rule matched
// (accepted, each carrying the reason it was accepted -- see
// domain.AcceptedWarning). A rule that matches none of warnings is stale:
// it produces its own STALE_ACCEPTANCE warning (appended to unaccepted) so
// an acceptance nobody prunes doesn't quietly rot once the warning it once
// matched stops firing (a fixed typo, a removed endpoint, ...). Warning
// order and rule order are both preserved; a warning matched by more than
// one rule is accepted under the first rule that matches it.
func partitionWarnings(warnings []domain.LintWarning, rules []domain.AcceptedWarning) (unaccepted []domain.LintWarning, accepted []domain.AcceptedLintWarning) {
	matched := make([]bool, len(rules))
	for i, rule := range rules {
		for _, w := range warnings {
			if warningMatchesRule(w, rule) {
				matched[i] = true
				break
			}
		}
	}

	for _, w := range warnings {
		reason, ok := firstMatchingReason(w, rules)
		if !ok {
			unaccepted = append(unaccepted, w)
			continue
		}
		accepted = append(accepted, domain.AcceptedLintWarning{LintWarning: w, Reason: reason})
	}

	for i, rule := range rules {
		if matched[i] {
			continue
		}
		unaccepted = append(unaccepted, domain.LintWarning{
			Code: "STALE_ACCEPTANCE",
			Message: fmt.Sprintf(
				"accepted_warnings entry for %s (match %q) matches no warning; remove it", rule.Code, rule.Match),
		})
	}

	return unaccepted, accepted
}

// firstMatchingReason returns the reason of the first rule (in service.yaml
// order) that matches w, and whether any rule matched at all.
func firstMatchingReason(w domain.LintWarning, rules []domain.AcceptedWarning) (string, bool) {
	for _, rule := range rules {
		if warningMatchesRule(w, rule) {
			return rule.Reason, true
		}
	}
	return "", false
}

// warningMatchesRule reports whether an accepted_warnings entry accepts w:
// the codes must match exactly, and, when rule.Match is set, it must occur
// (case-insensitively) as a substring of w.Message or of w.Source's File or
// Pointer. An empty Match accepts every warning with the rule's Code.
func warningMatchesRule(w domain.LintWarning, rule domain.AcceptedWarning) bool {
	if w.Code != rule.Code {
		return false
	}
	if rule.Match == "" {
		return true
	}
	m := strings.ToLower(rule.Match)
	if strings.Contains(strings.ToLower(w.Message), m) {
		return true
	}
	if w.Source != nil {
		if strings.Contains(strings.ToLower(w.Source.File), m) {
			return true
		}
		if strings.Contains(strings.ToLower(w.Source.Pointer), m) {
			return true
		}
	}
	return false
}

// buildKnownRefs builds the docs.KnownRefs catalog for one service from its
// own merged ingest result: its operation ids, paths, method-gated aliases,
// component schema names, service.yaml concepts, and its own name.
func buildKnownRefs(name string, meta domain.ServiceMetadata, merged *mergedContracts) docs.KnownRefs {
	known := docs.KnownRefs{
		Aliases:  map[string]string{},
		Methods:  map[string][]string{},
		Concepts: meta.Concepts,
		Services: []string{name},
	}

	for _, op := range merged.Operations {
		known.Operations = append(known.Operations, op.ID)
	}
	for _, s := range merged.Schemas {
		known.Schemas = append(known.Schemas, s.Name)
	}

	seenPath := map[string]bool{}
	methodSets := map[string]map[string]bool{}
	for _, a := range merged.Aliases {
		if !seenPath[a.Path] {
			known.Paths = append(known.Paths, a.Path)
			seenPath[a.Path] = true
		}
		if methodSets[a.Path] == nil {
			methodSets[a.Path] = map[string]bool{}
		}
		methodSets[a.Path][strings.ToUpper(a.Method)] = true
		known.Aliases[strings.ToUpper(a.Method)+" "+a.Path] = a.OperationID
	}
	for p, set := range methodSets {
		methods := make([]string, 0, len(set))
		for m := range set {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		known.Methods[p] = methods
	}

	return known
}

// buildDocs re-parses the contract-embedded docs (info/tag descriptions)
// through docs.Parse so they carry Refs, then parses every docs/**/*.md file
// under pkg.DocsDir.
func buildDocs(name string, pkg *Package, merged *mergedContracts, known docs.KnownRefs) ([]domain.Doc, error) {
	out := make([]domain.Doc, 0, len(merged.Docs))

	for _, d := range merged.Docs {
		body := ""
		if len(d.Sections) > 0 {
			body = d.Sections[0].Body
		}
		out = append(out, docs.Parse(body, docs.Options{
			ServiceID: name,
			Path:      d.Path,
			Title:     d.Title,
			Source:    d.Source,
			Known:     known,
		}))
	}

	if pkg.DocsDir != "" {
		files, err := globFilesRecursive(pkg.DocsDir, ".md")
		if err != nil {
			return nil, errs.Wrap(errs.Internal, err, "scanning %s", pkg.DocsDir)
		}
		for _, f := range files {
			rel := filepath.ToSlash(relPath(pkg.Dir, f))
			doc, perr := docs.ParseFile(f, docs.Options{
				ServiceID: name,
				Path:      rel,
				Known:     known,
			})
			if perr != nil {
				return nil, errs.Wrap(errs.Internal, perr, "parsing doc %s", f)
			}
			out = append(out, doc)
		}
	}

	return out, nil
}

// rawFlowStep and rawFlow are a deliberately minimal decode target for
// *.flow.yaml files: only the fields FlowSummary needs. Every other key
// (inputs, description, a step's body/extract/assert/... in any shape) is
// silently ignored by yaml.v3, so odd or evolving flow shapes never break
// discovery.
type rawFlowStep struct {
	ID   string `yaml:"id"`
	Call string `yaml:"call"`
}

type rawFlow struct {
	ID    string        `yaml:"id"`
	Name  string        `yaml:"name"`
	Tags  []string      `yaml:"tags"`
	Steps []rawFlowStep `yaml:"steps"`
}

// scanFlows discovers pkg's service-owned flows (flows/*.flow.yaml) as
// FlowSummary records owned by the service named ownerID.
func scanFlows(ownerID string, pkg *Package) ([]domain.FlowSummary, error) {
	if pkg.FlowsDir == "" {
		return nil, nil
	}

	matches, err := filepath.Glob(filepath.Join(pkg.FlowsDir, "*"+domain.FlowFileSuffix))
	if err != nil {
		return nil, errs.Wrap(errs.Internal, err, "globbing %s", pkg.FlowsDir)
	}
	sort.Strings(matches)

	out := make([]domain.FlowSummary, 0, len(matches))
	for _, f := range matches {
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			return nil, errs.Wrap(errs.Internal, rerr, "reading %s", f)
		}

		var raw rawFlow
		_ = yaml.Unmarshal(data, &raw) // best-effort; never fail discovery on odd flow shapes

		id := raw.ID
		if id == "" {
			id = strings.TrimSuffix(filepath.Base(f), domain.FlowFileSuffix)
		}

		var ops []string
		seenOp := map[string]bool{}
		for _, s := range raw.Steps {
			if s.Call == "" || seenOp[s.Call] {
				continue
			}
			seenOp[s.Call] = true
			ops = append(ops, s.Call)
		}

		info, statErr := os.Stat(f)
		var updated time.Time
		if statErr == nil {
			updated = info.ModTime()
		}

		h, herr := hashFileHex(f)
		if herr != nil {
			return nil, errs.Wrap(errs.Internal, herr, "hashing %s", f)
		}

		out = append(out, domain.FlowSummary{
			ID:         id,
			Name:       raw.Name,
			Path:       filepath.ToSlash(relPath(pkg.Dir, f)),
			OwnerKind:  "service",
			OwnerID:    ownerID,
			Tags:       raw.Tags,
			Operations: ops,
			StepCount:  len(raw.Steps),
			Hash:       h,
			Updated:    updated,
		})
	}

	return out, nil
}

// globFilesRecursive walks dir, returning (sorted) absolute paths of every
// regular file whose extension matches ext (case-insensitively). Hidden
// directories (dotfiles) are skipped entirely. A missing dir yields (nil,
// nil), not an error.
func globFilesRecursive(dir, ext string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ext) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// relPath returns path relative to base, falling back to path itself if it
// cannot be made relative (should not happen for our own discovered paths).
func relPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

// firstLine returns the first non-blank, trimmed line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// hashFileHex returns the sha256 hex digest of the file at path.
func hashFileHex(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
