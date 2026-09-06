// Builds a JSON skeleton from a request body's schema for the "from
// schema" button: required fields only, filled with placeholder values by
// type. Schema.Properties are already resolved at ingest (see
// internal/domain/schema.go), so this only needs to walk the tree; `ref`
// only appears for a genuine cycle, which is rendered as `{}`.
import type { Schema } from '../../api/types';

const MAX_DEPTH = 8;

function placeholderLeaf(schema: Schema, name: string): unknown {
  if (schema.enum && schema.enum.length > 0) return schema.enum[0];
  if (schema.example !== undefined) return schema.example;
  if (schema.default !== undefined) return schema.default;

  switch (schema.kind) {
    case 'string':
      switch (schema.format) {
        case 'date-time':
          return new Date().toISOString();
        case 'date':
          return new Date().toISOString().slice(0, 10);
        case 'email':
          return 'user@example.com';
        case 'uuid':
          return '00000000-0000-0000-0000-000000000000';
        default:
          return name || 'string';
      }
    case 'integer':
    case 'number':
      return 0;
    case 'boolean':
      return false;
    case 'null':
      return null;
    default:
      return null;
  }
}

function valueFor(schema: Schema | undefined, name: string, depth: number): unknown {
  if (!schema || depth > MAX_DEPTH) return null;
  switch (schema.kind) {
    case 'object':
    case 'array':
    case 'oneOf':
    case 'anyOf':
    case 'allOf':
    case 'ref':
      return buildSkeleton(schema, depth);
    default:
      return placeholderLeaf(schema, name);
  }
}

// buildSkeleton returns a JS value (to be JSON.stringify'd) with every
// required field of an object schema filled in; optional fields are
// omitted so the result stays small and obviously edit-in-place.
export function buildSkeleton(schema: Schema | undefined, depth = 0): unknown {
  if (!schema || depth > MAX_DEPTH) return null;

  switch (schema.kind) {
    case 'object': {
      const out: Record<string, unknown> = {};
      const props = schema.properties || {};
      const required = schema.required || [];
      const order = schema.property_order && schema.property_order.length > 0 ? schema.property_order : Object.keys(props);
      for (const key of order) {
        if (!required.includes(key)) continue;
        out[key] = valueFor(props[key], key, depth + 1);
      }
      return out;
    }
    case 'array':
      return [valueFor(schema.items, 'item', depth + 1)];
    case 'oneOf':
    case 'anyOf':
      return schema.variants && schema.variants.length > 0 ? valueFor(schema.variants[0], 'value', depth + 1) : null;
    case 'allOf': {
      let out: Record<string, unknown> = {};
      for (const variant of schema.variants || []) {
        const built = buildSkeleton(variant, depth + 1);
        if (built && typeof built === 'object' && !Array.isArray(built)) out = { ...out, ...(built as Record<string, unknown>) };
      }
      return out;
    }
    case 'ref':
      // Unresolved / cyclic reference: nothing more to expand server-side.
      return {};
    default:
      return placeholderLeaf(schema, '');
  }
}
