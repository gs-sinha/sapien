// Types for wave-2 pages (Try It, Examples, Memories, Events) that are not
// already in api/types.ts. Kept separate per the wave-2 file ownership
// split so this file never conflicts with the hand-maintained mirror of
// internal/domain.
//
// Hint/HintRef mirror internal/diagnose.Hint / internal/diagnose.Ref
// (internal/diagnose/diagnose.go), served by GET /v1/runs/{id}/hints.

export type HintKind = 'contract' | 'doc' | 'memory';

export interface HintRef {
  service?: string;
  path?: string;
  section?: string;
  memory_id?: string;
}

export interface Hint {
  kind: HintKind;
  step_id?: string;
  title: string;
  score: number;
  matched_on?: string;
  ref: HintRef;
}

// ---- Try It form state (kept per-operation in sessionStorage) ----

export interface TryItFormState {
  params: Record<string, unknown>;
  bodyText: string;
  headers: Array<{ key: string; value: string }>;
  env: string;
  allowProduction: boolean;
}
