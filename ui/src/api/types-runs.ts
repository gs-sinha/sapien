// New types for wave-2 flows/runs pages, mirroring internal/diagnose.Hint
// (internal/diagnose/diagnose.go). Kept separate from api/types.ts per the
// wave-2 file ownership split -- see ui/src/api/client.ts and types.ts for
// the hand-mirroring convention this follows.
import type { Flow } from './types';

// domain.Flow (internal/domain/flow.go) grew a `Source` field (raw YAML,
// json:"source,omitempty") so GET /v1/flows/{id} can carry the file's exact
// text, but api/types.ts -- off limits to this wave's edits -- has not been
// updated to mirror it yet. FlowWithSource is the same shape plus that one
// field; a plain Flow value from api/client.ts's `flows` functions already
// satisfies it structurally (the field is optional) whenever the server
// actually sends it.
export interface FlowWithSource extends Flow {
  source?: string;
}

export type HintKind = 'contract' | 'doc' | 'memory';

export interface HintRef {
  service?: string;
  path?: string;
  section?: string;
  memory_id?: string;
}

// One candidate explanation for a failed/errored step, as returned by
// GET /v1/runs/{id}/hints -- a route that may not exist yet (see
// getRunHints in api/flowsExtra.ts, which treats any failure as "no hints").
export interface Hint {
  kind: HintKind;
  step_id?: string;
  title: string;
  score: number;
  matched_on?: string;
  ref: HintRef;
}

export interface HintsResponse {
  hints: Hint[];
}
