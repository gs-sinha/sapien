// This file is internal/config's writer for the `semantic:` block (PLAN
// §34f item 5: Settings can turn semantic search on/off, without a
// restart, from the UI or `sapien semantic enable/disable`). It is kept
// separate from config.go -- which only ever reads -- so a second writer
// landing in its own file (the `updates:` block, PLAN §34f item 4) merges
// mechanically instead of editing the same function.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/errs"
)

// SemanticWrite is what WriteSemantic persists into one config file's
// `semantic:` block. Every field except APIKey is written unconditionally
// -- the caller (internal/engine/local's settings API) has already
// resolved defaults and decided the values this scope should hold. APIKey
// is tri-state: nil leaves the file's stored key untouched, a pointer to
// "" clears it, anything else (including a verbatim "${env.NAME}"
// reference) sets it.
type SemanticWrite struct {
	Enabled   bool
	Kind      string
	BaseURL   string
	Model     string
	BatchSize int
	APIKey    *string
	// Kinds replaces the `kinds:` list when non-nil; an empty list removes
	// the key (every kind). nil leaves the file's list alone.
	Kinds *[]string
	// QueryPrefix/DocumentPrefix: nil leaves the key alone; a pointer to a
	// nil *string removes it (back to the model's default); a pointer to a
	// string, "" included, writes it.
	QueryPrefix    **string
	DocumentPrefix **string
	// KeepAlive: nil leaves the key alone; "" removes it (Ollama's default).
	KeepAlive *string
}

// WriteSemantic writes w into path's top-level `semantic:` block, creating
// the file (and its directory, e.g. ~/.sapien) if it does not exist yet. It
// round-trips through a yaml.Node document so every other top-level key
// (`git:`, `daemon:`, `mcp:`, `updates:`, ...) and, as far as yaml.v3's
// Node re-encoding allows, comments survive untouched -- only the
// `semantic:` keys this call actually sets are replaced. The file is
// written 0600, since it may hold an api_key.
func WriteSemantic(path string, w SemanticWrite) error {
	doc, err := loadOrNewYAMLDocument(path)
	if err != nil {
		return err
	}

	root := doc.Content[0]
	sem := yamlMapValue(root, "semantic")
	if sem == nil {
		sem = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		yamlAppendMapEntry(root, "semantic", sem)
	}

	yamlSetScalar(sem, "enabled", w.Enabled)
	yamlSetScalar(sem, "kind", w.Kind)
	yamlSetScalar(sem, "base_url", w.BaseURL)
	yamlSetScalar(sem, "model", w.Model)
	yamlSetScalar(sem, "batch_size", w.BatchSize)
	if w.APIKey != nil {
		yamlSetScalar(sem, "api_key", *w.APIKey)
	}
	if w.Kinds != nil {
		if len(*w.Kinds) == 0 {
			yamlDeleteKey(sem, "kinds")
		} else {
			yamlSetScalar(sem, "kinds", *w.Kinds)
		}
	}
	for key, p := range map[string]**string{"query_prefix": w.QueryPrefix, "document_prefix": w.DocumentPrefix} {
		switch {
		case p == nil:
		case *p == nil:
			yamlDeleteKey(sem, key)
		default:
			yamlSetScalar(sem, key, **p)
		}
	}

	if w.KeepAlive != nil {
		if *w.KeepAlive == "" {
			yamlDeleteKey(sem, "keep_alive")
		} else {
			yamlSetScalar(sem, "keep_alive", *w.KeepAlive)
		}
	}

	return saveYAMLDocument(path, doc)
}

// SemanticSource reports which file contributed the effective semantic
// configuration config.Load(ws) would return: "workspace" when
// <ws>/.sapien/config.yaml sets any `semantic:` key, "user" when only the
// user-level file (UserPath) does, "default" when neither does. ws may be
// nil (no workspace-level file to consider), matching Load's own contract.
func SemanticSource(ws *domain.Workspace) (string, error) {
	if ws != nil {
		set, err := fileSetsSemantic(WorkspacePath(ws))
		if err != nil {
			return "", err
		}
		if set {
			return "workspace", nil
		}
	}
	set, err := fileSetsSemantic(UserPath())
	if err != nil {
		return "", err
	}
	if set {
		return "user", nil
	}
	return "default", nil
}

// fileSetsSemantic reports whether path's `semantic:` block sets at least
// one key. A missing file is not an error: it simply sets nothing.
func fileSetsSemantic(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, errs.Wrap(errs.Internal, err, "config: reading %s", path)
	}
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false, errs.Wrap(errs.Invalid, err, "config: parsing %s", path).WithDetail("file", path)
	}
	r := raw.Semantic
	return r.Enabled != nil || r.Kind != nil || r.BaseURL != nil || r.Model != nil || r.APIKey != nil || r.BatchSize != nil ||
		r.Kinds != nil || r.QueryPrefix != nil || r.DocumentPrefix != nil || r.KeepAlive != nil, nil
}

// loadOrNewYAMLDocument reads path as a yaml.Node document, or -- when it
// does not exist, or is empty -- returns a fresh document with an empty
// top-level mapping, ready for yamlMapValue/yamlAppendMapEntry to build on.
func loadOrNewYAMLDocument(path string) (*yaml.Node, error) {
	emptyDoc := func() *yaml.Node {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyDoc(), nil
		}
		return nil, errs.Wrap(errs.Internal, err, "config: reading %s", path)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, errs.Wrap(errs.Invalid, err, "config: parsing %s", path).WithDetail("file", path)
	}
	// An empty (or all-comments) file decodes to the zero Node.
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return emptyDoc(), nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errs.New(errs.Invalid, "config: %s does not contain a YAML mapping at the top level", path).
			WithDetail("file", path)
	}
	return &doc, nil
}

// yamlMapValue returns the value node for key in mapping node mapNode, or
// nil when key is absent.
func yamlMapValue(mapNode *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapNode.Content); i += 2 {
		if mapNode.Content[i].Value == key {
			return mapNode.Content[i+1]
		}
	}
	return nil
}

// yamlAppendMapEntry appends a new key/value pair to mapping node mapNode.
// Callers already know key is absent (yamlMapValue returned nil); this
// never checks itself, to keep it a plain append.
func yamlAppendMapEntry(mapNode *yaml.Node, key string, value *yaml.Node) {
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapNode.Content = append(mapNode.Content, keyNode, value)
}

// yamlSetScalar sets key's scalar value within mapping node mapNode,
// appending a new entry when key does not exist yet, or re-encoding just
// the existing value node in place (keeping the key node -- and any
// HeadComment/FootComment already attached to it -- untouched) when it
// does.
func yamlSetScalar(mapNode *yaml.Node, key string, value any) {
	v := yamlMapValue(mapNode, key)
	if v == nil {
		v = &yaml.Node{}
		yamlAppendMapEntry(mapNode, key, v)
	}
	_ = v.Encode(value)
}

// yamlDeleteKey removes key (and its value) from mapping node mapNode; a
// key that is not there is left not there.
func yamlDeleteKey(mapNode *yaml.Node, key string) {
	for i := 0; i+1 < len(mapNode.Content); i += 2 {
		if mapNode.Content[i].Value == key {
			mapNode.Content = append(mapNode.Content[:i], mapNode.Content[i+2:]...)
			return
		}
	}
}

// saveYAMLDocument creates path's parent directory if needed and writes doc
// to path, 0600.
func saveYAMLDocument(path string, doc *yaml.Node) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errs.Wrap(errs.Internal, err, "config: creating %s", filepath.Dir(path))
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "config: encoding %s", path)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return errs.Wrap(errs.Internal, err, "config: writing %s", path)
	}
	return nil
}
