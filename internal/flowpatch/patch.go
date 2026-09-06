package flowpatch

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// canonicalOrder is where Apply inserts a top-level key that doesn't exist
// yet (set_inputs, set_meta, or add_step creating a phase list for the
// first time): it looks for the first key already present that comes later
// in this list and inserts just before it, so a brand-new `inputs:` lands
// after `tags:` and before `setup:`/`steps:` the way a hand-written flow
// would order it, without disturbing any existing key's position.
var canonicalOrder = []string{"version", "id", "name", "description", "tags", "inputs", "setup", "steps", "teardown"}

// phaseKeys is every step-list key, in the order Apply searches them when
// looking for a step by id (set_step/merge_step/remove_step don't take a
// Phase, so a step is found wherever it actually lives).
var phaseKeys = []string{"setup", "steps", "teardown"}

// Apply parses source (a *.flow.yaml document) into its YAML node tree,
// applies ops in order, and returns the re-marshaled result. Each op is
// applied to the tree built by the previous one, so later ops see earlier
// ones' effects (e.g. add_step then merge_step on the step it just added).
// An error identifies the failing op by index and kind and leaves source
// unexamined otherwise; Apply does not partially write anything -- it only
// returns a new string for the caller to validate and save.
func Apply(source string, ops []Op) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
		return "", fmt.Errorf("flowpatch: parsing source: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return "", fmt.Errorf("flowpatch: source is not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("flowpatch: flow document must be a YAML mapping at the top level")
	}

	for i, op := range ops {
		if err := applyOne(root, op); err != nil {
			return "", fmt.Errorf("flowpatch: op %d (%s): %w", i, op.Kind, err)
		}
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("flowpatch: marshaling result: %w", err)
	}
	return string(out), nil
}

func applyOne(root *yaml.Node, op Op) error {
	switch op.Kind {
	case KindSetStep:
		return applySetStep(root, op)
	case KindMergeStep:
		return applyMergeStep(root, op)
	case KindAddStep:
		return applyAddStep(root, op)
	case KindRemoveStep:
		return applyRemoveStep(root, op)
	case KindSetInputs:
		return applySetInputs(root, op)
	case KindSetMeta:
		return applySetMeta(root, op)
	case "":
		return fmt.Errorf("op has no kind")
	default:
		return fmt.Errorf("unknown op kind %q", op.Kind)
	}
}

// --- set_step -----------------------------------------------------

func applySetStep(root *yaml.Node, op Op) error {
	if op.ID == "" {
		return fmt.Errorf("set_step requires id")
	}
	_, seq, idx, _, err := findStep(root, op.ID)
	if err != nil {
		return err
	}
	newNode, err := encodeStepNode(op.ID, op.Step)
	if err != nil {
		return err
	}
	seq.Content[idx] = newNode
	return nil
}

// --- merge_step -----------------------------------------------------

func applyMergeStep(root *yaml.Node, op Op) error {
	if op.ID == "" {
		return fmt.Errorf("merge_step requires id")
	}
	_, _, _, stepNode, err := findStep(root, op.ID)
	if err != nil {
		return err
	}
	if len(op.Fields) == 0 {
		return fmt.Errorf("merge_step requires at least one field")
	}

	keys := make([]string, 0, len(op.Fields))
	for k := range op.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output regardless of JSON/map iteration order

	for _, k := range keys {
		if !allowedMergeFields[k] {
			return fmt.Errorf("unknown field %q; want one of %s", k, strings.Join(sortedMergeFieldNames(), ", "))
		}
		valNode, err := encodeNode(op.Fields[k])
		if err != nil {
			return fmt.Errorf("field %q: %w", k, err)
		}
		mappingSet(stepNode, k, valNode)
	}
	return nil
}

func sortedMergeFieldNames() []string {
	names := make([]string, 0, len(allowedMergeFields))
	for k := range allowedMergeFields {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// --- add_step -----------------------------------------------------

func applyAddStep(root *yaml.Node, op Op) error {
	if op.After != "" && op.Before != "" {
		return fmt.Errorf("add_step: set after or before, not both")
	}
	phaseKey, err := phaseKeyFor(op.Phase)
	if err != nil {
		return err
	}
	newNode, err := encodeStepNode("", op.Step)
	if err != nil {
		return err
	}
	_, idNode := mappingGet(newNode, "id")
	if idNode == nil || idNode.Value == "" {
		return fmt.Errorf("add_step: step must include an id")
	}
	newID := idNode.Value
	if _, _, _, _, err := findStep(root, newID); err == nil {
		return fmt.Errorf("add_step: step id %q already exists", newID)
	}

	seq, err := getOrCreateSeq(root, phaseKey)
	if err != nil {
		return err
	}

	insertIdx := len(seq.Content)
	switch {
	case op.After != "":
		i, ok := seqIndexOf(seq, op.After)
		if !ok {
			return fmt.Errorf("add_step: after step %q not found in phase %q; ids present: %s",
				op.After, phaseLabel(op.Phase), strings.Join(allStepIDs(root), ", "))
		}
		insertIdx = i + 1
	case op.Before != "":
		i, ok := seqIndexOf(seq, op.Before)
		if !ok {
			return fmt.Errorf("add_step: before step %q not found in phase %q; ids present: %s",
				op.Before, phaseLabel(op.Phase), strings.Join(allStepIDs(root), ", "))
		}
		insertIdx = i
	}

	seq.Content = append(seq.Content, nil)
	copy(seq.Content[insertIdx+1:], seq.Content[insertIdx:])
	seq.Content[insertIdx] = newNode
	return nil
}

func phaseLabel(phase string) string {
	if phase == "" {
		return "steps"
	}
	return phase
}

// --- remove_step -----------------------------------------------------

func applyRemoveStep(root *yaml.Node, op Op) error {
	if op.ID == "" {
		return fmt.Errorf("remove_step requires id")
	}
	_, seq, idx, _, err := findStep(root, op.ID)
	if err != nil {
		return err
	}
	seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
	return nil
}

// --- set_inputs -----------------------------------------------------

func applySetInputs(root *yaml.Node, op Op) error {
	var valNode *yaml.Node
	if op.Inputs == nil {
		valNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	} else {
		n, err := encodeNode(op.Inputs)
		if err != nil {
			return err
		}
		valNode = n
	}
	mappingSetOrdered(root, "inputs", valNode)
	return nil
}

// --- set_meta -----------------------------------------------------

func applySetMeta(root *yaml.Node, op Op) error {
	if op.Meta == nil {
		return fmt.Errorf("set_meta requires meta")
	}
	if op.Meta.Name != nil {
		mappingSetOrdered(root, "name", scalarNode(*op.Meta.Name))
	}
	if op.Meta.Description != nil {
		mappingSetOrdered(root, "description", scalarNode(*op.Meta.Description))
	}
	if op.Meta.Tags != nil {
		node, err := encodeNode(*op.Meta.Tags)
		if err != nil {
			return err
		}
		mappingSetOrdered(root, "tags", node)
	}
	return nil
}

// --- node helpers -----------------------------------------------------

// mappingGet returns the index (in m.Content) of key's key-node, and its
// value node, or (-1, nil) if m has no such key. m must be a MappingNode.
func mappingGet(m *yaml.Node, key string) (int, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i, m.Content[i+1]
		}
	}
	return -1, nil
}

// mappingSet replaces key's value in m if present, else appends key: val at
// the end.
func mappingSet(m *yaml.Node, key string, val *yaml.Node) {
	if i, _ := mappingGet(m, key); i >= 0 {
		m.Content[i+1] = val
		return
	}
	m.Content = append(m.Content, keyNode(key), val)
}

// mappingSetOrdered is mappingSet, except a brand-new key is inserted at
// its canonicalOrder position (see canonicalOrder) instead of at the end.
func mappingSetOrdered(m *yaml.Node, key string, val *yaml.Node) {
	if i, _ := mappingGet(m, key); i >= 0 {
		m.Content[i+1] = val
		return
	}
	orderIdx := indexOf(canonicalOrder, key)
	insertAt := len(m.Content)
	if orderIdx >= 0 {
		for i := 0; i+1 < len(m.Content); i += 2 {
			existingIdx := indexOf(canonicalOrder, m.Content[i].Value)
			if existingIdx >= 0 && existingIdx > orderIdx {
				insertAt = i
				break
			}
		}
	}
	m.Content = append(m.Content, nil, nil)
	copy(m.Content[insertAt+2:], m.Content[insertAt:])
	m.Content[insertAt] = keyNode(key)
	m.Content[insertAt+1] = val
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func keyNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func scalarNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// encodeNode marshals v (typically decoded JSON: map[string]any,
// []any, string, float64, bool, or nil) into a fresh yaml.Node.
func encodeNode(v any) (*yaml.Node, error) {
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return n, nil
}

// encodeStepNode encodes stepVal (a step's JSON/YAML mapping) into a
// MappingNode. If the encoded step has no `id` key, defaultID (when
// non-empty) is inserted as its first key; if it does have one and
// defaultID is also set, they must match (set_step: the step you send back
// must be the step you asked for).
func encodeStepNode(defaultID string, stepVal any) (*yaml.Node, error) {
	if stepVal == nil {
		return nil, fmt.Errorf("step is required")
	}
	n, err := encodeNode(stepVal)
	if err != nil {
		return nil, fmt.Errorf("encoding step: %w", err)
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("step must be a mapping")
	}
	_, idNode := mappingGet(n, "id")
	switch {
	case idNode == nil && defaultID == "":
		return nil, fmt.Errorf("step must include an id")
	case idNode == nil:
		n.Content = append([]*yaml.Node{keyNode("id"), scalarNode(defaultID)}, n.Content...)
	case defaultID != "" && idNode.Value != defaultID:
		return nil, fmt.Errorf("step id %q does not match %q", idNode.Value, defaultID)
	}
	return n, nil
}

// findStep locates the step named id across setup, steps, and teardown (in
// that order), returning the phase key it was found under, the sequence
// node holding it, its index within that sequence, and the step's own
// mapping node. If no step has that id anywhere, the error names every id
// actually present.
func findStep(root *yaml.Node, id string) (phaseKey string, seq *yaml.Node, idx int, node *yaml.Node, err error) {
	for _, pk := range phaseKeys {
		_, s := mappingGet(root, pk)
		if s == nil || s.Kind != yaml.SequenceNode {
			continue
		}
		if i, ok := seqIndexOf(s, id); ok {
			return pk, s, i, s.Content[i], nil
		}
	}
	return "", nil, -1, nil, fmt.Errorf("unknown step id %q; ids present: %s", id, strings.Join(allStepIDs(root), ", "))
}

// seqIndexOf returns the index within seq (a step-list SequenceNode) of the
// step mapping whose `id` equals id.
func seqIndexOf(seq *yaml.Node, id string) (int, bool) {
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if _, idNode := mappingGet(item, "id"); idNode != nil && idNode.Value == id {
			return i, true
		}
	}
	return -1, false
}

// allStepIDs lists every step id across setup, steps, and teardown, in that
// order, for error messages.
func allStepIDs(root *yaml.Node) []string {
	var out []string
	for _, pk := range phaseKeys {
		_, s := mappingGet(root, pk)
		if s == nil || s.Kind != yaml.SequenceNode {
			continue
		}
		for _, item := range s.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			if _, idNode := mappingGet(item, "id"); idNode != nil {
				out = append(out, idNode.Value)
			}
		}
	}
	return out
}

// phaseKeyFor maps an Op.Phase to its YAML key: "" (the main steps) ->
// "steps", "setup" -> "setup", "teardown" -> "teardown"; anything else is
// an error.
func phaseKeyFor(phase string) (string, error) {
	switch phase {
	case "":
		return "steps", nil
	case "setup", "teardown":
		return phase, nil
	default:
		return "", fmt.Errorf("unknown phase %q; want \"\" (steps), \"setup\", or \"teardown\"", phase)
	}
}

// getOrCreateSeq returns the SequenceNode at root's phaseKey, creating an
// empty one (inserted via mappingSetOrdered) if absent.
func getOrCreateSeq(root *yaml.Node, phaseKey string) (*yaml.Node, error) {
	_, s := mappingGet(root, phaseKey)
	if s != nil {
		if s.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%q is not a list", phaseKey)
		}
		return s, nil
	}
	newSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	mappingSetOrdered(root, phaseKey, newSeq)
	return newSeq, nil
}
