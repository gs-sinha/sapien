package config

import (
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/gs-sinha/sapien/internal/errs"
)

// SetUpdatesCheck sets `updates: check:` in the user config file
// (UserPath), preserving every other key -- most importantly `mcp:` and
// `semantic:`, which this package's own Load never declares (see the
// package doc) and would silently drop if this round-tripped through a
// typed Config -- and every comment, by editing the file's parsed
// yaml.Node tree rather than re-marshaling a struct. A config file that
// does not exist yet is created with just this one key. This is PUT
// /v1/settings/updates's write path (PLAN §34f item 4); a later PUT for
// another top-level section (e.g. `semantic:`) can reuse setUserMapping
// below rather than growing its own copy of this dance.
func SetUpdatesCheck(check bool) error {
	return setUserMapping(func(root *yaml.Node) error {
		return setScalarPath(root, []string{"updates", "check"}, "!!bool", strconv.FormatBool(check))
	})
}

// setUserMapping loads UserPath() as a YAML document node (an empty
// mapping if the file does not exist, or is empty), applies mutate to its
// root mapping, and writes the result back atomically (temp file in the
// same directory, then rename) with mode 0600 -- the same durability
// daemon.Write and selfupdate's cache file use.
func setUserMapping(mutate func(root *yaml.Node) error) error {
	path := UserPath()

	var doc yaml.Node
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) > 0:
		if uerr := yaml.Unmarshal(data, &doc); uerr != nil {
			return errs.Wrap(errs.Invalid, uerr, "config: parsing %s", path).WithDetail("file", path)
		}
	case err == nil, os.IsNotExist(err):
		// Empty or absent: start from an empty mapping rather than failing,
		// since "no config file yet" is the common case the first PUT hits.
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	default:
		return errs.Wrap(errs.Internal, err, "config: reading %s", path)
	}
	if len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errs.New(errs.Invalid, "config: %s does not contain a YAML mapping at its root", path).WithDetail("file", path)
	}

	if err := mutate(root); err != nil {
		return err
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return errs.Wrap(errs.Internal, err, "config: encoding %s", path)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errs.Wrap(errs.Internal, err, "creating %s", dir)
	}
	tmp, err := os.CreateTemp(dir, ".config.yaml.*.tmp")
	if err != nil {
		return errs.Wrap(errs.Internal, err, "creating temp file in %s", dir)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return errs.Wrap(errs.Internal, err, "writing %s", tmpName)
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, err, "closing %s", tmpName)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return errs.Wrap(errs.Internal, err, "chmod %s", tmpName)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errs.Wrap(errs.Internal, err, "renaming %s to %s", tmpName, path)
	}
	return nil
}

// setScalarPath walks keyPath (creating intermediate mapping nodes as
// needed) inside root -- itself a MappingNode -- and sets the final key to
// a scalar with the given YAML tag and value, preserving that key's own
// comments if it was already present. A non-mapping value found partway
// along (e.g. a hand-edited `updates: false`) is overwritten with a fresh
// mapping rather than rejected: this write is the source of truth for the
// key from here on.
func setScalarPath(root *yaml.Node, keyPath []string, tag, value string) error {
	node := root
	for i, key := range keyPath {
		_, valNode := findMapEntry(node, key)
		last := i == len(keyPath)-1

		if valNode == nil {
			valNode = &yaml.Node{Kind: yaml.MappingNode}
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, valNode)
		}
		if last {
			valNode.Kind = yaml.ScalarNode
			valNode.Tag = tag
			valNode.Value = value
			valNode.Content = nil
			return nil
		}
		if valNode.Kind != yaml.MappingNode {
			valNode.Kind = yaml.MappingNode
			valNode.Tag = ""
			valNode.Value = ""
			valNode.Content = nil
		}
		node = valNode
	}
	return nil
}

// findMapEntry returns the key and value nodes of key within m (a
// MappingNode), or (nil, nil) if key is not present.
func findMapEntry(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}
