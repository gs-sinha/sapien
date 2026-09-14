package mcp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// allowMutationsTemplate is the file AllowMutations writes when a workspace
// has no .sapien/mcp.yaml yet.
const allowMutationsTemplate = `# MCP permissions for agents using this workspace on this machine. This file
# lives in .sapien/, which git ignores. Reference: docs/mcp.md "Permissions".
default:
  # POST, PUT, PATCH and DELETE calls from agents (execute_api, run_flow).
  # Production environments stay blocked unless allow_production is set.
  execute_mutation: true
`

// AllowMutations sets default.execute_mutation: true in the MCP permission
// file at path (a workspace's .sapien/mcp.yaml), so every client may make
// POST/PUT/PATCH/DELETE calls on the environments it is already allowed.
// It creates the file when it does not exist, and otherwise edits it in
// place, keeping every other key and comment. A document that nests its
// settings under `mcp:` (the shape ~/.sapien/config.yaml uses) is edited
// there. It reports whether the file changed.
//
// It does not touch allow_production or environments: production stays
// behind its own explicit grant.
func AllowMutations(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("mcp: reading config %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, fmt.Errorf("mcp: creating %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(allowMutationsTemplate), 0o644); err != nil {
			return false, fmt.Errorf("mcp: writing config %s: %w", path, err)
		}
		return true, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf("mcp: parsing config %s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, fmt.Errorf("mcp: config %s is not a YAML mapping", path)
	}
	root := doc.Content[0]
	if nested := mappingValue(root, "mcp"); nested != nil && nested.Kind == yaml.MappingNode {
		root = nested
	}

	def, err := ensureMapping(root, "default", path)
	if err != nil {
		return false, err
	}
	if val := mappingValue(def, "execute_mutation"); val != nil {
		var granted bool
		if val.Kind == yaml.ScalarNode && val.Decode(&granted) == nil && granted {
			return false, nil
		}
		*val = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true", LineComment: val.LineComment}
	} else {
		def.Content = append(def.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "execute_mutation"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
		)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return false, fmt.Errorf("mcp: encoding config %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return false, fmt.Errorf("mcp: encoding config %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("mcp: writing config %s: %w", path, err)
	}
	return true, nil
}

// mappingValue returns the value node for key in mapping m, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// ensureMapping returns m's mapping under key, adding it when absent and
// turning an empty value (`default:` with nothing after it) into one.
func ensureMapping(m *yaml.Node, key, path string) (*yaml.Node, error) {
	val := mappingValue(m, key)
	if val == nil {
		val = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
		return val, nil
	}
	switch {
	case val.Kind == yaml.MappingNode:
		return val, nil
	case val.Kind == yaml.ScalarNode && val.Tag == "!!null":
		*val = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", LineComment: val.LineComment}
		return val, nil
	default:
		return nil, fmt.Errorf("mcp: %s in config %s is not a mapping", key, path)
	}
}
