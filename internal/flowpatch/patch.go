package flowpatch

import (
	"bytes"
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

// conventionalStepKeyOrder is the key order Apply uses whenever it writes a
// step's keys itself: add_step's brand-new step, set_step's whole
// replacement, and any key merge_step adds that the step didn't already
// have. A step decoded from JSON arrives as map[string]any, which has no
// order, and yaml.v3 alphabetizes a Go map on encode -- so without this,
// every step Apply writes comes out as assert/body/call/id/input, not the
// order a person would hand-write it in. Mirrors the field order of a
// hand-written flow step (id, when, call, ...), ending with the loop-block
// fields (a step is either a call step or a block, never both).
var conventionalStepKeyOrder = []string{
	"id", "when", "call", "example", "input", "params", "body", "headers",
	"timeout", "until", "poll", "extract", "assert",
	"foreach", "repeat", "max", "break_when", "on_error", "steps",
}

// phaseKeys is every step-list key, in the order Apply searches them when
// looking for a step by id (a step id is unique across the whole flow, so
// every op that names a step by id -- not just add_step's anchors --
// searches all three, recursing into loop blocks along the way).
var phaseKeys = []string{"setup", "steps", "teardown"}

// Result is what Apply returns: the patched document, ready to validate and
// save, plus any Notes about a comment Apply could not carry over
// unambiguously. See the package doc for exactly what yaml.v3 does and does
// not preserve.
type Result struct {
	// YAML is the patched document.
	YAML string
	// Notes has one entry per step whose own comment (its HeadComment,
	// LineComment, or FootComment, on the step itself or its first key) was
	// affected by the patch: dropped along with a step set_step replaced or
	// remove_step removed, or left in place on a step merge_step changed
	// underneath it. Empty when no patched step had such a comment.
	Notes []string
}

// Apply parses source (a *.flow.yaml document) into its YAML node tree,
// applies ops in order, and returns the re-marshaled result. Each op is
// applied to the tree built by the previous one, so later ops see earlier
// ones' effects (e.g. add_step then merge_step on the step it just added).
// An error identifies the failing op by index and kind and leaves source
// unexamined otherwise; Apply does not partially write anything -- it only
// returns a new string for the caller to validate and save.
func Apply(source string, ops []Op) (Result, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
		return Result{}, fmt.Errorf("flowpatch: parsing source: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return Result{}, fmt.Errorf("flowpatch: source is not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return Result{}, fmt.Errorf("flowpatch: flow document must be a YAML mapping at the top level")
	}

	var notes []string
	for i, op := range ops {
		if err := applyOne(root, op, &notes); err != nil {
			return Result{}, fmt.Errorf("flowpatch: op %d (%s): %w", i, op.Kind, err)
		}
	}

	out, err := marshalWithIndent(&doc, detectIndentWidth(source))
	if err != nil {
		return Result{}, fmt.Errorf("flowpatch: marshaling result: %w", err)
	}
	return Result{YAML: out, Notes: notes}, nil
}

// marshalWithIndent re-serializes doc using indent spaces per nesting level
// (see detectIndentWidth) instead of yaml.v3's own default of 4, which
// would otherwise silently reflow a 2-space-indented file -- see the
// package doc for what else yaml.v3 normalizes regardless of this setting.
func marshalWithIndent(doc *yaml.Node, indent int) (string, error) {
	if indent < 2 {
		indent = 2
	}
	if indent > 9 {
		indent = 9
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// detectIndentWidth guesses source's indent width (spaces per nesting
// level) from its own text, before any parsing: the first line that is
// nothing but "key:" (opening a nested block -- a mapping or a sequence),
// compared against the indent of its first non-blank, non-comment child
// line. Comment lines between the two are skipped, so a comment directly
// above a flow's first step (the common case) doesn't hide the sequence
// item that actually carries the indent. Falls back to 2 -- this
// codebase's own convention -- when no such pair exists (e.g. a flow with
// only flat top-level scalar keys). Does not handle a sequence written
// flush with its key (`steps:\n- id: a`, zero extra indent for the dash);
// that style falls back to 2 as well.
func detectIndentWidth(source string) int {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasSuffix(trimmed, ":") {
			continue // not a bare block opener (has an inline value, or is a sequence item itself)
		}
		indent := len(line) - len(trimmed)
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			nt := strings.TrimLeft(next, " ")
			if nt == "" || strings.HasPrefix(nt, "#") {
				continue
			}
			nIndent := len(next) - len(nt)
			if nIndent > indent {
				return nIndent - indent
			}
			break // sibling or dedent immediately follows; this key had no block child
		}
	}
	return 2
}

func applyOne(root *yaml.Node, op Op, notes *[]string) error {
	switch op.Kind {
	case KindSetStep:
		return applySetStep(root, op, notes)
	case KindMergeStep:
		return applyMergeStep(root, op, notes)
	case KindAddStep:
		return applyAddStep(root, op)
	case KindRemoveStep:
		return applyRemoveStep(root, op, notes)
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

// appendCommentNote records one Notes entry: "step `id` <msg>".
func appendCommentNote(notes *[]string, id, msg string) {
	*notes = append(*notes, fmt.Sprintf("step `%s` %s", id, msg))
}

// hasStepComment reports whether node -- a step's own sequence-item mapping
// node -- carries a HeadComment, LineComment, or FootComment, either on
// itself or on its first key: the two places yaml.v3 attaches a comment
// written directly above a step in the source.
func hasStepComment(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.HeadComment != "" || node.LineComment != "" || node.FootComment != "" {
		return true
	}
	if len(node.Content) > 0 {
		k := node.Content[0]
		if k.HeadComment != "" || k.LineComment != "" || k.FootComment != "" {
			return true
		}
	}
	return false
}

// --- set_step -----------------------------------------------------

func applySetStep(root *yaml.Node, op Op, notes *[]string) error {
	if op.ID == "" {
		return fmt.Errorf("set_step requires id")
	}
	loc, err := locateStep(root, op.ID)
	if err != nil {
		return err
	}
	if hasStepComment(loc.node) {
		appendCommentNote(notes, op.ID, "had a comment above it; it was removed with the step")
	}
	newNode, err := encodeStepNode(op.ID, op.Step)
	if err != nil {
		return err
	}
	loc.holder.Content[loc.idx] = newNode
	return nil
}

// --- merge_step -----------------------------------------------------

func applyMergeStep(root *yaml.Node, op Op, notes *[]string) error {
	if op.ID == "" {
		return fmt.Errorf("merge_step requires id")
	}
	loc, err := locateStep(root, op.ID)
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
	sort.Strings(keys) // deterministic processing regardless of JSON/map iteration order

	for _, k := range keys {
		if !allowedMergeFields[k] {
			return fmt.Errorf("unknown field %q; want one of %s", k, strings.Join(sortedMergeFieldNames(), ", "))
		}
		valNode, err := encodeNode(op.Fields[k])
		if err != nil {
			return fmt.Errorf("field %q: %w", k, err)
		}
		// A new key lands at its conventional position (e.g. `input` before
		// `body`, `headers` after) regardless of processing order -- each
		// insertion looks at the step's current keys fresh, so the order
		// keys are merged in doesn't matter.
		mappingSetStepOrdered(loc.node, k, valNode)
	}
	if hasStepComment(loc.node) {
		appendCommentNote(notes, op.ID, "kept its comment; check it still describes the step")
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

// applyAddStep inserts op.Step into a step list. When op.Before/op.After
// names an anchor step, the anchor is resolved flow-wide (a step id is
// unique across setup/steps/teardown and every loop block) and the target
// list, and the new step's phase/block, are INFERRED from wherever that
// anchor actually lives -- not from op.Phase/op.Into, which is only
// checked, never trusted, once an anchor is given: an explicit Phase or
// Into that disagrees with the anchor's real location is rejected with an
// error naming exactly where the anchor is, rather than silently using the
// (wrong) explicit value or claiming the anchor wasn't found. With no
// anchor, Phase/Into pick the list directly, same as before (append to a
// phase list, or into a block's nested steps).
func applyAddStep(root *yaml.Node, op Op) error {
	if op.After != "" && op.Before != "" {
		return fmt.Errorf("add_step: set after or before, not both")
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
	if _, err := locateStep(root, newID); err == nil {
		return fmt.Errorf("add_step: step id %q already exists", newID)
	}

	anchor, anchorWord := op.Before, "before"
	if op.After != "" {
		anchor, anchorWord = op.After, "after"
	}

	var seq *yaml.Node
	var insertIdx int

	switch {
	case anchor != "":
		loc, ferr := locateStep(root, anchor)
		if ferr != nil {
			return fmt.Errorf("add_step: %s step %q not found anywhere in the flow; ids present: %s",
				anchorWord, anchor, strings.Join(allStepIDs(root), ", "))
		}
		if op.Phase != "" {
			wantPhase, perr := phaseKeyFor(op.Phase)
			if perr != nil {
				return perr
			}
			if wantPhase != loc.phase {
				return fmt.Errorf("add_step: %s step %q is in %s; phase %q does not match",
					anchorWord, anchor, loc.label(), op.Phase)
			}
		}
		if op.Into != "" && op.Into != loc.block {
			return fmt.Errorf("add_step: %s step %q is in %s; into %q does not match",
				anchorWord, anchor, loc.label(), op.Into)
		}
		seq = loc.holder
		insertIdx = loc.idx
		if anchorWord == "after" {
			insertIdx = loc.idx + 1
		}

	case op.Into != "":
		// PLAN §34f.8: add inside a loop block's own nested `steps:`
		// instead of a top-level phase list.
		blockLoc, ferr := locateStep(root, op.Into)
		if ferr != nil {
			return fmt.Errorf("add_step: into block %q not found; ids present: %s",
				op.Into, strings.Join(allStepIDs(root), ", "))
		}
		s, err := getOrCreateNestedSeq(blockLoc.node)
		if err != nil {
			return err
		}
		seq = s
		insertIdx = len(seq.Content)

	default:
		phaseKey, err := phaseKeyFor(op.Phase)
		if err != nil {
			return err
		}
		s, err := getOrCreateSeq(root, phaseKey)
		if err != nil {
			return err
		}
		seq = s
		insertIdx = len(seq.Content)
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

func applyRemoveStep(root *yaml.Node, op Op, notes *[]string) error {
	if op.ID == "" {
		return fmt.Errorf("remove_step requires id")
	}
	loc, err := locateStep(root, op.ID)
	if err != nil {
		return err
	}
	if hasStepComment(loc.node) {
		appendCommentNote(notes, op.ID, "had a comment above it; it was removed with the step")
	}
	loc.holder.Content = append(loc.holder.Content[:loc.idx], loc.holder.Content[loc.idx+1:]...)
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

// mappingSetInOrder replaces key's value in m if present (moving nothing);
// otherwise it inserts key: val as a new pair, positioned just before the
// first existing key that comes later than key in order -- so a brand-new
// key lands where order says it conventionally belongs, not wherever it
// happened to be processed relative to other new keys, and not at the end.
// A key not present in order at all is appended at the end.
func mappingSetInOrder(m *yaml.Node, key string, val *yaml.Node, order []string) {
	if i, _ := mappingGet(m, key); i >= 0 {
		m.Content[i+1] = val
		return
	}
	orderIdx := indexOf(order, key)
	insertAt := len(m.Content)
	if orderIdx >= 0 {
		for i := 0; i+1 < len(m.Content); i += 2 {
			existingIdx := indexOf(order, m.Content[i].Value)
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

// mappingSetOrdered is mappingSetInOrder against canonicalOrder (a flow's
// own top-level keys): set_inputs' `inputs:` and set_meta's `name:`,
// `description:`, `tags:`.
func mappingSetOrdered(m *yaml.Node, key string, val *yaml.Node) {
	mappingSetInOrder(m, key, val, canonicalOrder)
}

// mappingSetStepOrdered is mappingSetInOrder against
// conventionalStepKeyOrder (a step's own keys): merge_step's fields.
func mappingSetStepOrdered(m *yaml.Node, key string, val *yaml.Node) {
	mappingSetInOrder(m, key, val, conventionalStepKeyOrder)
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
// MappingNode whose keys are written in conventionalStepKeyOrder,
// regardless of stepVal's own (map[string]any: unordered) iteration order
// -- letting yaml.v3's Encode decide would alphabetize them instead. Any
// key stepVal has that isn't a real step field is still written (sorted,
// at the end) rather than silently dropped; flow validation, not Apply, is
// what rejects it. If the encoded step has no `id` key, defaultID (when
// non-empty) is inserted as its first key; if it does have one and
// defaultID is also set, they must match (set_step: the step you send back
// must be the step you asked for).
func encodeStepNode(defaultID string, stepVal any) (*yaml.Node, error) {
	if stepVal == nil {
		return nil, fmt.Errorf("step is required")
	}
	m, ok := stepVal.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("step must be a mapping")
	}

	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	seen := make(map[string]bool, len(m))
	for _, key := range conventionalStepKeyOrder {
		v, ok := m[key]
		if !ok {
			continue
		}
		seen[key] = true
		valNode, err := encodeNode(v)
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		n.Content = append(n.Content, keyNode(key), valNode)
	}
	var extra []string
	for k := range m {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		valNode, err := encodeNode(m[key])
		if err != nil {
			return nil, fmt.Errorf("encoding %q: %w", key, err)
		}
		n.Content = append(n.Content, keyNode(key), valNode)
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

// stepLocation describes exactly where one step lives in the flow: the
// top-level phase list it (or its enclosing block) is under, the id of
// that enclosing loop block when the step is nested one level in (PLAN
// §34f.8; blocks cannot nest, so one level is always enough, "" when the
// step is a direct child of the phase list), the exact SequenceNode
// holding the step, and its index within that sequence.
type stepLocation struct {
	phase  string // "setup" | "steps" | "teardown"
	block  string // enclosing block's step id, or ""
	holder *yaml.Node
	idx    int
	node   *yaml.Node
}

// label describes loc for an error message, e.g. `phase "steps"` or
// `block "each" (phase "steps")` -- so "not found" and "does not match"
// errors can say exactly where a listed id actually is, instead of a bare
// id an agent has already seen and been told (wrongly) is missing.
func (loc stepLocation) label() string {
	if loc.block == "" {
		return fmt.Sprintf("phase %q", phaseLabel(loc.phase))
	}
	return fmt.Sprintf("block %q (phase %q)", loc.block, phaseLabel(loc.phase))
}

// locateStep finds the step named id anywhere in the flow -- setup, steps,
// or teardown, recursing one level into any loop block's own nested
// `steps:` along the way -- so set_step, merge_step, remove_step, and
// add_step's anchor/into/duplicate-id checks all address a nested step
// exactly like a top-level one. If no step has that id anywhere, the error
// names every id actually present, each labeled with where it lives (see
// stepLocation.label), so "present but refused" cannot happen again.
func locateStep(root *yaml.Node, id string) (stepLocation, error) {
	for _, pk := range phaseKeys {
		_, s := mappingGet(root, pk)
		if s == nil || s.Kind != yaml.SequenceNode {
			continue
		}
		if loc, ok := locateStepInSeq(s, pk, "", id); ok {
			return loc, nil
		}
	}
	return stepLocation{}, fmt.Errorf("unknown step id %q; ids present: %s", id, strings.Join(allStepIDs(root), ", "))
}

// locateStepInSeq searches seq (a step-list SequenceNode under phase, and
// under block if seq is itself a loop block's nested steps) for the step
// named id, recursing one level into any item's own nested `steps:`.
func locateStepInSeq(seq *yaml.Node, phase, block, id string) (stepLocation, bool) {
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if _, idNode := mappingGet(item, "id"); idNode != nil && idNode.Value == id {
			return stepLocation{phase: phase, block: block, holder: seq, idx: i, node: item}, true
		}
		if _, nested := mappingGet(item, "steps"); nested != nil && nested.Kind == yaml.SequenceNode {
			blockID := ""
			if _, bn := mappingGet(item, "id"); bn != nil {
				blockID = bn.Value
			}
			if loc, ok := locateStepInSeq(nested, phase, blockID, id); ok {
				return loc, true
			}
		}
	}
	return stepLocation{}, false
}

// allStepIDs lists every step id across setup, steps, and teardown, each
// labeled with where it lives (stepLocation.label), in that order,
// recursing into each loop block's own nested steps right after the block
// itself, for error messages.
func allStepIDs(root *yaml.Node) []string {
	var out []string
	for _, pk := range phaseKeys {
		_, s := mappingGet(root, pk)
		if s == nil || s.Kind != yaml.SequenceNode {
			continue
		}
		out = append(out, seqStepIDs(s, pk, "")...)
	}
	return out
}

// seqStepIDs lists every step id in seq (under phase/block, see
// locateStepInSeq), recursing one level into any item's own nested
// `steps:`, each formatted as `id (location)`.
func seqStepIDs(seq *yaml.Node, phase, block string) []string {
	var out []string
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if _, idNode := mappingGet(item, "id"); idNode != nil {
			loc := stepLocation{phase: phase, block: block}
			out = append(out, fmt.Sprintf("%s (%s)", idNode.Value, loc.label()))
		}
		if _, nested := mappingGet(item, "steps"); nested != nil && nested.Kind == yaml.SequenceNode {
			blockID := ""
			if _, bn := mappingGet(item, "id"); bn != nil {
				blockID = bn.Value
			}
			out = append(out, seqStepIDs(nested, phase, blockID)...)
		}
	}
	return out
}

// phaseKeyFor maps an Op.Phase to its YAML key: "" or "steps" (the main
// steps) -> "steps", "setup" -> "setup", "teardown" -> "teardown"; anything
// else is an error. "" is add_step's default (append to the main steps)
// and is also what an anchor-driven add_step treats as "no explicit phase
// given" (never a contradiction); "steps" is accepted too so an explicit
// phase can be written for every list, including the main one.
func phaseKeyFor(phase string) (string, error) {
	switch phase {
	case "", "steps":
		return "steps", nil
	case "setup", "teardown":
		return phase, nil
	default:
		return "", fmt.Errorf("unknown phase %q; want \"\" or \"steps\" (the main list), \"setup\", or \"teardown\"", phase)
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

// getOrCreateNestedSeq returns blockNode's own `steps:` SequenceNode,
// creating an empty one if absent (PLAN §34f.8: a well-formed block always
// has at least one nested step already, since `steps: []` fails the
// schema's minItems: 1, but add_step's `into` is defensive about a
// malformed document anyway).
func getOrCreateNestedSeq(blockNode *yaml.Node) (*yaml.Node, error) {
	_, s := mappingGet(blockNode, "steps")
	if s != nil {
		if s.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("block's steps is not a list")
		}
		return s, nil
	}
	newSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	blockNode.Content = append(blockNode.Content, keyNode("steps"), newSeq)
	return newSeq, nil
}
