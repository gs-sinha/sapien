// State and YAML-patching helpers for FlowDetailPage's editable step
// payloads (build brief item 1: "flows's payload should be editable on ui
// and not just some parameter"). Kept in its own module so FlowDetailPage,
// FlowStepCard, and RunPanel can all share one shape without a circular
// import between the page and its own subcomponents.
//
// js-yaml is imported lazily (dynamic import) wherever it's actually used
// (applyStepEditsToYaml) so the dependency ships only in the flow detail
// page's own lazy-loaded chunk, never the app shell.
import type { Step } from '../../api/types';

// One step's pending edits. A field's presence here (even an empty object)
// means "the user changed this field"; absence means "still whatever the
// flow's own step declares". This is what drives both the per-step
// "modified" marker and Reset.
export interface StepEdit {
  input?: Record<string, unknown>;
  body?: unknown;
  headers?: Record<string, string>;
}

export type FlowStepEdits = Record<string, StepEdit>;

export function isStepModified(edit: StepEdit | undefined): boolean {
  return !!edit && (edit.input !== undefined || edit.body !== undefined || edit.headers !== undefined);
}

function storageKey(flowId: string): string {
  return `sapien:flow-step-edits:${flowId}`;
}

// loadStepEdits/saveStepEdits persist edits in sessionStorage (per flow id)
// so navigating away to a run and back keeps them -- but only for the tab's
// session, never across a browser restart, matching the rest of this app's
// "no cross-navigation cache in memory, nothing durable client-side" stance
// for anything that isn't explicitly saved server-side.
export function loadStepEdits(flowId: string): FlowStepEdits {
  try {
    const raw = sessionStorage.getItem(storageKey(flowId));
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? (parsed as FlowStepEdits) : {};
  } catch {
    return {};
  }
}

export function saveStepEdits(flowId: string, edits: FlowStepEdits): void {
  try {
    if (Object.keys(edits).length === 0) {
      sessionStorage.removeItem(storageKey(flowId));
    } else {
      sessionStorage.setItem(storageKey(flowId), JSON.stringify(edits));
    }
  } catch {
    // sessionStorage can throw (private browsing, quota); edits still work
    // in-memory for the rest of this page visit, they just won't survive
    // navigating away.
  }
}

// applyEditsToSteps walks a step list depth-first, applying `edits` by step
// id, and recurses into a loop block's own `steps` (PLAN §34f.8: step ids
// stay unique across the whole flow, blocks included, so an edit keyed by
// a nested step's id must reach it there too).
function applyEditsToSteps(steps: Array<Record<string, unknown>>, edits: FlowStepEdits): void {
  for (const step of steps) {
    const id = typeof step.id === 'string' ? step.id : undefined;
    const edit = id ? edits[id] : undefined;
    if (edit) {
      if (edit.input !== undefined) step.input = edit.input;
      if (edit.body !== undefined) step.body = edit.body;
      if (edit.headers !== undefined) step.headers = edit.headers;
    }
    if (Array.isArray(step.steps)) applyEditsToSteps(step.steps as Array<Record<string, unknown>>, edits);
  }
}

// applyStepEditsToYaml parses the flow's saved YAML `source`, overwrites
// input/body/headers on every step named in `edits` (leaving everything
// else -- other steps, extract/assert/until/poll, key order -- exactly as
// parsed), and dumps it back to YAML text for POST /v1/runs/source or
// PUT /v1/flows/{id}. js-yaml's dumper does not preserve comments or the
// source file's original formatting; callers that persist this (Save to
// flow) must warn about that separately.
export async function applyStepEditsToYaml(source: string, edits: FlowStepEdits): Promise<string> {
  const yaml = await import('js-yaml');
  const doc = yaml.load(source);
  if (!doc || typeof doc !== 'object' || !Array.isArray((doc as { steps?: unknown }).steps)) {
    // Not a shape we recognize (unexpected top-level YAML, or a flow with
    // no steps array) -- return the source unchanged rather than risk
    // dumping something that doesn't round-trip.
    return source;
  }
  const steps = (doc as { steps: Array<Record<string, unknown>> }).steps;
  applyEditsToSteps(steps, edits);
  return yaml.dump(doc, { lineWidth: -1, noRefs: true });
}

// hasAnyEdits reports whether any step in `edits` is actually modified,
// for enabling/disabling "Save to flow" and deciding whether the run panel
// needs to patch YAML at all instead of just calling POST /v1/flows/{id}/run.
export function hasAnyEdits(edits: FlowStepEdits): boolean {
  return Object.values(edits).some(isStepModified);
}

// mergedStepValue returns what a step's field currently is: the edit if the
// user touched it, otherwise the original step's own value.
export function mergedInput(step: Step, edit: StepEdit | undefined): Record<string, unknown> {
  return edit?.input ?? step.input ?? {};
}
export function mergedBody(step: Step, edit: StepEdit | undefined): unknown {
  return edit?.body !== undefined ? edit.body : step.body;
}
export function mergedHeaders(step: Step, edit: StepEdit | undefined): Record<string, string> {
  return edit?.headers ?? step.headers ?? {};
}

// A step's whole body may itself be a `${...}` template (substituted with
// whatever value -- object, array, scalar -- the expression evaluates to at
// run time), not JSON. bodyToText/textToBody keep that one case as a plain
// string instead of routing it through JSON.stringify/parse (which would
// otherwise still round-trip correctly since JSON quotes a string, but would
// force the user to type quotes around a template to edit it back).
const templateRe = /^\$\{[^}]*\}$/;

export function bodyToText(body: unknown): string {
  if (body === undefined) return '';
  if (typeof body === 'string' && templateRe.test(body.trim())) return body;
  try {
    return JSON.stringify(body, null, 2) ?? '';
  } catch {
    return String(body);
  }
}

// textToBody parses a body textarea's current text back into a value fit
// for StepEdit.body. Invalid JSON is still returned (as the raw text) so a
// keystroke never gets silently dropped -- `valid: false` is what the editor
// uses to show a live "invalid JSON" note and disable Format.
export function textToBody(text: string): { value: unknown; valid: boolean } {
  const trimmed = text.trim();
  if (trimmed === '') return { value: undefined, valid: true };
  if (templateRe.test(trimmed)) return { value: trimmed, valid: true };
  try {
    return { value: JSON.parse(text), valid: true };
  } catch {
    return { value: text, valid: false };
  }
}
