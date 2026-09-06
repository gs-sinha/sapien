package openapi

import (
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	"github.com/pb33f/libopenapi/orderedmap"
	yaml "go.yaml.in/yaml/v4"

	"github.com/gs-sinha/sapien/internal/domain"
)

// schemaCtx carries the state needed while normalizing a schema tree: the chain of
// component names currently being expanded (for cycle detection), and the current
// nesting depth (for the MaxDepth cap).
type schemaCtx struct {
	chain []string
	depth int
}

func (c schemaCtx) push(name string) schemaCtx {
	chain := make([]string, len(c.chain)+1)
	copy(chain, c.chain)
	chain[len(c.chain)] = name
	return schemaCtx{chain: chain, depth: c.depth + 1}
}

func (c schemaCtx) deeper() schemaCtx {
	return schemaCtx{chain: c.chain, depth: c.depth + 1}
}

func (c schemaCtx) contains(name string) bool {
	for _, n := range c.chain {
		if n == name {
			return true
		}
	}
	return false
}

// componentName extracts the trailing component name from a $ref such as
// "#/components/schemas/Pet", returning "" if it doesn't look like a local schema ref.
func componentName(ref string) string {
	if ref == "" {
		return ""
	}
	idx := strings.LastIndexByte(ref, '/')
	if idx < 0 || idx == len(ref)-1 {
		return ""
	}
	return ref[idx+1:]
}

// resolveSchemaProxy resolves a *base.SchemaProxy into a normalized *domain.Schema,
// handling component naming, cycle detection (chain of component names being
// expanded), and the MaxDepth cap. usedBy, when non-nil, is called with every
// component name reached (directly or nested) so callers can compute NamedSchema.UsedBy.
func (b *builder) resolveSchemaProxy(sp *base.SchemaProxy, ctx schemaCtx, usedBy func(name string)) *domain.Schema {
	if sp == nil {
		return nil
	}

	var name string
	if sp.IsReference() {
		name = componentName(sp.GetReference())
	}

	if name != "" {
		if usedBy != nil {
			usedBy(name)
		}
		if ctx.contains(name) {
			b.addWarning(domain.LintWarning{
				Code:    "CIRCULAR_REF",
				Message: "circular reference to component " + name,
			})
			return &domain.Schema{Kind: domain.KindRef, Ref: b.serviceID + "." + name, Name: name}
		}
	}

	if ctx.depth > b.maxDepth {
		if name != "" {
			return &domain.Schema{Kind: domain.KindRef, Ref: b.serviceID + "." + name, Name: name}
		}
		return &domain.Schema{Kind: domain.KindAny}
	}

	s, err := sp.BuildSchema()
	if err != nil || s == nil {
		ref := sp.GetReference()
		b.addWarning(domain.LintWarning{
			Code:    "UNRESOLVED_REF",
			Message: "unable to resolve reference " + ref,
		})
		return &domain.Schema{Kind: domain.KindAny}
	}

	nextCtx := ctx.deeper()
	if name != "" {
		nextCtx = ctx.push(name)
	}
	out := b.convertSchema(s, nextCtx, usedBy)
	if name != "" {
		out.Name = name
	}
	return out
}

// convertSchema converts an already-built *base.Schema (the dereferenced value) into
// a normalized *domain.Schema.
func (b *builder) convertSchema(s *base.Schema, ctx schemaCtx, usedBy func(name string)) *domain.Schema {
	out := &domain.Schema{}

	switch {
	case len(s.OneOf) > 0:
		out.Kind = domain.KindOneOf
		out.Variants = b.convertVariants(s.OneOf, ctx, usedBy)
	case len(s.AnyOf) > 0:
		out.Kind = domain.KindAnyOf
		out.Variants = b.convertVariants(s.AnyOf, ctx, usedBy)
	case len(s.AllOf) > 0:
		out.Kind = domain.KindAllOf
		out.Variants = b.convertVariants(s.AllOf, ctx, usedBy)
	default:
		kind, nullable := kindFromTypes(s.Type)
		out.Kind = kind
		if nullable {
			out.Nullable = true
		}
	}

	if s.Nullable != nil && *s.Nullable {
		out.Nullable = true
	}

	out.Description = s.Description
	out.Format = s.Format
	if s.ReadOnly != nil {
		out.ReadOnly = *s.ReadOnly
	}
	if s.WriteOnly != nil {
		out.WriteOnly = *s.WriteOnly
	}
	if s.Deprecated != nil {
		out.Deprecated = *s.Deprecated
	}
	if s.Default != nil {
		out.Default = nodeToValue(s.Default)
	}
	if s.Example != nil {
		out.Example = nodeToValue(s.Example)
	} else if len(s.Examples) > 0 && s.Examples[0] != nil {
		out.Example = nodeToValue(s.Examples[0])
	}
	if len(s.Enum) > 0 {
		out.Enum = nodesToValues(s.Enum)
	}

	if s.Properties != nil && orderedmap.Len(s.Properties) > 0 {
		out.Properties = make(map[string]*domain.Schema, orderedmap.Len(s.Properties))
		for propName, propProxy := range s.Properties.FromOldest() {
			out.PropertyOrder = append(out.PropertyOrder, propName)
			out.Properties[propName] = b.resolveSchemaProxy(propProxy, ctx.deeper(), usedBy)
		}
		if out.Kind == "" || out.Kind == domain.KindAny {
			out.Kind = domain.KindObject
		}
	}
	if len(s.Required) > 0 {
		out.Required = append([]string{}, s.Required...)
	}

	if s.Items != nil && s.Items.IsA() && s.Items.A != nil {
		out.Items = b.resolveSchemaProxy(s.Items.A, ctx.deeper(), usedBy)
		if out.Kind == "" {
			out.Kind = domain.KindArray
		}
	}

	if s.AdditionalProperties != nil && s.AdditionalProperties.IsA() && s.AdditionalProperties.A != nil {
		out.AdditionalProperties = b.resolveSchemaProxy(s.AdditionalProperties.A, ctx.deeper(), usedBy)
	}

	if out.Kind == "" {
		switch {
		case out.Properties != nil:
			out.Kind = domain.KindObject
		case out.Items != nil:
			out.Kind = domain.KindArray
		default:
			out.Kind = domain.KindAny
		}
	}

	return out
}

func (b *builder) convertVariants(proxies []*base.SchemaProxy, ctx schemaCtx, usedBy func(name string)) []*domain.Schema {
	out := make([]*domain.Schema, 0, len(proxies))
	for _, p := range proxies {
		out = append(out, b.resolveSchemaProxy(p, ctx.deeper(), usedBy))
	}
	return out
}

// kindFromTypes maps an OpenAPI `type` (a single 3.0 string, or a 3.1 list that may
// include "null") to a domain.SchemaKind plus whether "null" was present.
func kindFromTypes(types []string) (domain.SchemaKind, bool) {
	if len(types) == 0 {
		return "", false
	}
	nullable := false
	var nonNull []string
	for _, t := range types {
		if t == "null" {
			nullable = true
			continue
		}
		nonNull = append(nonNull, t)
	}
	if len(nonNull) == 0 {
		return domain.KindNull, true
	}
	return domainKindFor(nonNull[0]), nullable
}

func domainKindFor(t string) domain.SchemaKind {
	switch t {
	case "object":
		return domain.KindObject
	case "array":
		return domain.KindArray
	case "string":
		return domain.KindString
	case "number":
		return domain.KindNumber
	case "integer":
		return domain.KindInteger
	case "boolean":
		return domain.KindBoolean
	case "null":
		return domain.KindNull
	default:
		return domain.KindAny
	}
}

// nodeToValue decodes a *yaml.Node into a generic Go value.
func nodeToValue(n *yaml.Node) any {
	if n == nil {
		return nil
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return n.Value
	}
	return v
}

func nodesToValues(nodes []*yaml.Node) []any {
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeToValue(n))
	}
	return out
}

// schemaTypeString renders the compact type string used by Field.Type, per PLAN §5:
// "string", "integer", ..., unions like "string|null", "oneOf(Bike|Van)".
func schemaTypeString(s *domain.Schema) string {
	if s == nil {
		return string(domain.KindAny)
	}
	switch s.Kind {
	case domain.KindOneOf, domain.KindAnyOf, domain.KindAllOf:
		names := make([]string, 0, len(s.Variants))
		for _, v := range s.Variants {
			names = append(names, variantLabel(v))
		}
		return string(s.Kind) + "(" + strings.Join(names, "|") + ")"
	case domain.KindRef:
		if s.Name != "" {
			return s.Name
		}
		return string(domain.KindObject)
	default:
		k := string(s.Kind)
		if s.Nullable && s.Kind != domain.KindNull {
			return k + "|null"
		}
		return k
	}
}

func variantLabel(s *domain.Schema) string {
	if s == nil {
		return string(domain.KindAny)
	}
	if s.Name != "" {
		return s.Name
	}
	return string(s.Kind)
}
