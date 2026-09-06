// Per-operation form state: sessionStorage persistence (so navigating away
// from /ui/try/:operationId and back keeps the payload) and small helpers
// for turning a Param's schema into a sensible default/typed value for a
// plain-text form control.
import type { Param } from '../../api/types';
import type { TryItFormState } from '../../api/types-try';

function storageKey(operationId: string): string {
  return `sapien-tryit:${operationId}`;
}

export function loadFormState(operationId: string): TryItFormState | null {
  try {
    const raw = sessionStorage.getItem(storageKey(operationId));
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return null;
    return parsed as TryItFormState;
  } catch {
    return null;
  }
}

export function saveFormState(operationId: string, state: TryItFormState): void {
  try {
    sessionStorage.setItem(storageKey(operationId), JSON.stringify(state));
  } catch {
    // sessionStorage unavailable (private mode, quota); state just won't
    // survive navigation this time.
  }
}

// defaultParamValue seeds a fresh param input: booleans default false,
// numbers/integers default to an empty string (so the field starts blank
// rather than showing a misleading 0), everything else an empty string.
export function defaultParamValue(param: Param): unknown {
  if (param.example !== undefined) return param.example;
  if (param.schema?.default !== undefined) return param.schema.default;
  if (param.schema?.kind === 'boolean') return false;
  return '';
}

// paramDisplayString renders a param's current value back into a plain
// text input's string value.
export function paramDisplayString(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  return String(value);
}

// coerceParamValue converts a text input's raw string into the JS value
// CallRequest.params should carry for that param's declared type: numbers
// and integers parse to a number (left as the raw string if unparsable, so
// the daemon's own validation reports the problem rather than silently
// dropping it), everything else stays a string.
export function coerceParamValue(param: Param, raw: string): unknown {
  const kind = param.schema?.kind;
  if (kind === 'integer' || kind === 'number') {
    if (raw.trim() === '') return raw;
    const n = Number(raw);
    return Number.isNaN(n) ? raw : n;
  }
  return raw;
}

export function typeHint(param: Param): string {
  const schema = param.schema;
  if (!schema) return 'string';
  if (schema.enum && schema.enum.length > 0) return schema.enum.map(String).join(' | ');
  return schema.format ? `${schema.kind} (${schema.format})` : schema.kind;
}
