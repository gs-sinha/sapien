import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiClientError } from '../api/client';

interface AsyncState<T> {
  data: T | null;
  error: ApiClientError | Error | null;
  loading: boolean;
  reload: () => void;
}

// Fetch-on-mount, no cross-page cache: each page owns its own data and
// drops it on unmount (the point being run detail pages, whose data can
// include full request/response bodies, don't linger in memory after the
// user navigates away).
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): AsyncState<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<ApiClientError | Error | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    fnRef
      .current()
      .then((res) => {
        if (!cancelled) {
          setData(res);
          setLoading(false);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err : new Error(String(err)));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);

  return { data, error, loading, reload };
}
