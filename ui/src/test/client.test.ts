import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiClientError, callOperation, environments, flows, getWorkspace, normalizeNulls, operations, services } from '../api/client';

// jsdom (and Node 16) has no native global `fetch`, so there is nothing to
// vi.spyOn -- stub a fresh mock function as the global instead.
const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function mockResponse(status: number, body: unknown, ok = status >= 200 && status < 300): Response {
  return {
    ok,
    status,
    statusText: 'status',
    text: async () => (body === undefined ? '' : JSON.stringify(body)),
  } as Response;
}

describe('api client', () => {
  it('parses a successful GET response', async () => {
    fetchMock.mockResolvedValueOnce(mockResponse(200, { version: 1, name: 'demo', dir: '/tmp/demo', file: '/tmp/demo/sapien.workspace.yaml' }));
    const ws = await getWorkspace();
    expect(ws.name).toBe('demo');
    expect(fetchMock).toHaveBeenCalledWith('/v1/workspace', expect.objectContaining({ method: 'GET', credentials: 'include' }));
  });

  it('sends a JSON body and Content-Type on POST', async () => {
    fetchMock.mockResolvedValueOnce(mockResponse(200, { id: 'run_1', status: 'passed', environment: 'stage', summary: {}, steps: [] }));
    await callOperation({ operation: 'svc.op', env: 'stage' });
    const [, init] = fetchMock.mock.calls[0];
    expect(init.method).toBe('POST');
    expect(init.headers['Content-Type']).toBe('application/json');
    expect(JSON.parse(init.body)).toEqual({ operation: 'svc.op', env: 'stage' });
  });

  it('maps a daemon error body to ApiClientError', async () => {
    fetchMock.mockResolvedValueOnce(
      mockResponse(404, { code: 'E_SERVICE_NOT_FOUND', message: 'service "x" not found', hint: 'run sapien service add' }, false),
    );
    await expect(services.get('x')).rejects.toMatchObject({
      code: 'E_SERVICE_NOT_FOUND',
      message: 'service "x" not found',
      hint: 'run sapien service add',
    });
  });

  it('maps a network failure to E_NETWORK', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
    await expect(getWorkspace()).rejects.toBeInstanceOf(ApiClientError);
    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
    await expect(getWorkspace()).rejects.toMatchObject({ code: 'E_NETWORK' });
  });

  it('handles a 204 No Content response', async () => {
    fetchMock.mockResolvedValueOnce(mockResponse(204, undefined));
    await expect(services.remove('x')).resolves.toBeUndefined();
  });
});

describe('normalizeNulls', () => {
  it('rewrites a null value to [] for a known nullable-array key', () => {
    expect(normalizeNulls({ steps: null })).toEqual({ steps: [] });
  });

  it('recurses into nested objects and arrays', () => {
    const input = {
      flows: [{ id: 'f1', steps: null }],
      docs: null,
      nested: { deeper: { tags: null } },
    };
    expect(normalizeNulls(input)).toEqual({
      flows: [{ id: 'f1', steps: [] }],
      docs: [],
      nested: { deeper: { tags: [] } },
    });
  });

  it('leaves a null value alone for a key that is map/scalar-shaped elsewhere', () => {
    // "params" is Operation.params (Param[]) in one shape but Step.params
    // (an ExplicitParams object) in another, so it's deliberately excluded
    // from NULLABLE_ARRAY_KEYS -- converting a null Step.params to [] would
    // corrupt that shape.
    expect(normalizeNulls({ params: null, headers: null, body: null, inputs: null })).toEqual({
      params: null,
      headers: null,
      body: null,
      inputs: null,
    });
  });

  it('leaves non-null values, including empty arrays and objects, untouched', () => {
    expect(normalizeNulls({ steps: [], owners: ['a'], name: 'x', count: 0, ok: false })).toEqual({
      steps: [],
      owners: ['a'],
      name: 'x',
      count: 0,
      ok: false,
    });
  });

  it('is applied to every parsed response body inside request()', async () => {
    fetchMock.mockResolvedValueOnce(
      mockResponse(200, {
        version: 1,
        id: 'f1',
        steps: null, // Flow.Steps has no `omitempty` -- Go emits a literal null
        tags: null,
      }),
    );
    const flow = await flows.get('f1');
    expect(flow.steps).toEqual([]);
    expect(flow.tags).toEqual([]);
  });

  it('does not rewrite a null map-typed field even when normalizing the response', async () => {
    fetchMock.mockResolvedValueOnce(
      mockResponse(200, {
        id: 'orders.createOrder',
        service_id: 'orders',
        protocol: 'http',
        tags: null,
        params: null, // Operation.params (Param[]) -- excluded key, so left null, not []
        source: { file: 'contract.yaml' },
        hash: 'h1',
      }),
    );
    const op = await operations.get('orders.createOrder');
    expect(op.tags).toEqual([]);
    expect(op.params).toBeNull();
  });

  it('falls back to [] for a bare top-level null array response (environments.list)', async () => {
    fetchMock.mockResolvedValueOnce(mockResponse(200, null));
    await expect(environments.list()).resolves.toEqual([]);
  });
});
