// Wraps an async fetch to also report how long it took, so pages can show
// "123 ms" next to search/context results without every call site
// duplicating the performance.now() bookkeeping.
export interface Timed<T> {
  data: T;
  latencyMs: number;
}

export async function timed<T>(fn: () => Promise<T>): Promise<Timed<T>> {
  const start = performance.now();
  const data = await fn();
  return { data, latencyMs: Math.round(performance.now() - start) };
}
