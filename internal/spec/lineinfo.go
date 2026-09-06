package spec

import "gopkg.in/yaml.v3"

// lineForTokens walks a parsed yaml.Node document tree following the
// instance-location tokens jsonschema/v6 reports (raw property names and
// array indices, in order) and returns the 1-based source line of the
// deepest node it can resolve.
//
// This is best effort: yaml.Node line numbers point at scalar/mapping/
// sequence starts, not at "the exact character of the violation", and a
// keyword like additionalProperties reports the *containing* object's
// location rather than the specific extra key -- so the line returned is
// the start of that object, which is close enough to point a human or
// agent at the right place. If any token along the path cannot be resolved
// (e.g. the document doesn't actually have that shape), the line of the
// deepest node reached so far is returned rather than giving up entirely.
// Returns 0 only if root itself is not a usable document.
func lineForTokens(root *yaml.Node, tokens []string) int {
	node := documentContent(root)
	if node == nil {
		return 0
	}
	line := node.Line
	for _, tok := range tokens {
		next := descend(node, tok)
		if next == nil {
			break
		}
		node = next
		line = node.Line
	}
	return line
}

// documentContent unwraps a yaml.Node produced by yaml.Unmarshal(src, &root)
// (a DocumentNode) down to its actual top-level content node.
func documentContent(root *yaml.Node) *yaml.Node {
	if root == nil || root.Kind == 0 {
		return nil
	}
	n := root
	for n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind == yaml.DocumentNode {
		return nil
	}
	return n
}

// descend moves one instance-location token into node: a mapping key lookup
// for object properties, or a sequence index for arrays.
func descend(node *yaml.Node, tok string) *yaml.Node {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Value == tok {
				return node.Content[i+1]
			}
		}
		return nil
	case yaml.SequenceNode:
		idx, ok := parseIndex(tok)
		if !ok || idx < 0 || idx >= len(node.Content) {
			return nil
		}
		return node.Content[idx]
	default:
		return nil
	}
}

func parseIndex(tok string) (int, bool) {
	if tok == "" {
		return 0, false
	}
	n := 0
	for _, r := range tok {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
