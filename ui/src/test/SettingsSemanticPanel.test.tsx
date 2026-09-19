import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import SemanticSearchPanel from '../pages/settings/SemanticSearchPanel';
import { useEvents } from '../state/events';
import type { OllamaModelsResponse, SemanticSettings, SemanticTestResult, UpdateSemanticSettingsRequest } from '../api/types';

const baseSettings: SemanticSettings = {
  enabled: true,
  kind: 'ollama',
  base_url: 'http://localhost:11434',
  model: 'nomic-embed-text',
  api_key_set: false,
  source: 'user',
  status: { state: 'ready', model: 'nomic-embed-text', dim: 768, embedded: 1903, total: 1903 },
};

const baseOllama: OllamaModelsResponse = {
  reachable: true,
  base_url: 'http://localhost:11434',
  models: [{ name: 'nomic-embed-text' }],
};

const get = vi.fn(async (): Promise<SemanticSettings> => baseSettings);
const update = vi.fn(async (_req: UpdateSemanticSettingsRequest): Promise<SemanticSettings> => baseSettings);
const runTest = vi.fn(async (_req: UpdateSemanticSettingsRequest): Promise<SemanticTestResult> => ({ ok: true, dim: 768, latency_ms: 42 }));
const reindex = vi.fn(async (): Promise<void> => undefined);
const ollama = vi.fn(async (_baseUrl: string): Promise<OllamaModelsResponse> => baseOllama);
const ollamaPull = vi.fn(async (_req: { model: string; base_url?: string }): Promise<void> => undefined);

vi.mock('../api/client', () => ({
  semanticSettings: {
    get: () => get(),
    update: (req: UpdateSemanticSettingsRequest) => update(req),
    test: (req: UpdateSemanticSettingsRequest) => runTest(req),
    reindex: () => reindex(),
    ollama: (baseUrl: string) => ollama(baseUrl),
    ollamaPull: (req: { model: string; base_url?: string }) => ollamaPull(req),
  },
}));

beforeEach(() => {
  get.mockReset().mockResolvedValue(baseSettings);
  update.mockReset().mockResolvedValue(baseSettings);
  runTest.mockReset().mockResolvedValue({ ok: true, dim: 768, latency_ms: 42 });
  reindex.mockReset().mockResolvedValue(undefined);
  ollama.mockReset().mockResolvedValue(baseOllama);
  ollamaPull.mockReset().mockResolvedValue(undefined);
});

describe('SemanticSearchPanel', () => {
  it('renders the current settings and a Ready status line from GET', async () => {
    render(<SemanticSearchPanel />);

    await waitFor(() => expect(screen.getByText('Ready · nomic-embed-text · 768 dims · 1,903 embedded')).toBeInTheDocument());
    expect(screen.getByRole('checkbox', { name: 'Enable semantic search' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'Ollama' })).toBeChecked();
    expect(get).toHaveBeenCalled();
  });

  it('shows the one-sentence explanation when off, with no provider fields', async () => {
    get.mockResolvedValue({ ...baseSettings, enabled: false, status: { state: 'off' } });
    render(<SemanticSearchPanel />);

    await waitFor(() => expect(screen.getByText(/optional, off by default/)).toBeInTheDocument());
    expect(screen.queryByRole('radio', { name: 'Ollama' })).not.toBeInTheDocument();
    expect(screen.getByText('Off.')).toBeInTheDocument();
  });

  it('a refused Save shows the message inline and "Save anyway" resends with force: true', async () => {
    const user = userEvent.setup();
    update.mockRejectedValueOnce(new Error('changing model requires a full reindex'));
    render(<SemanticSearchPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.getByText('changing model requires a full reindex')).toBeInTheDocument());
    expect(update).toHaveBeenCalledTimes(1);
    expect(update.mock.calls[0][0].force).toBeUndefined();

    await user.click(screen.getByRole('button', { name: 'Save anyway' }));
    await waitFor(() => expect(update).toHaveBeenCalledTimes(2));
    expect(update.mock.calls[1][0].force).toBe(true);
  });

  it('Test connection shows ok/dim/latency inline', async () => {
    const user = userEvent.setup();
    render(<SemanticSearchPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Test connection' })).toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: 'Test connection' }));
    await waitFor(() => expect(screen.getByText('OK · 768 dims · 42ms')).toBeInTheDocument());
  });

  it('Reindex posts /reindex', async () => {
    const user = userEvent.setup();
    render(<SemanticSearchPanel />);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Reindex' })).toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: 'Reindex' }));
    await waitFor(() => expect(reindex).toHaveBeenCalled());
  });

  it('Ollama: shows suggestion chips for missing models with a Pull button, tracks progress from a simulated semantic.pull event, and refreshes on done', async () => {
    ollama.mockResolvedValueOnce({ reachable: true, base_url: 'http://localhost:11434', models: [] });
    render(<SemanticSearchPanel />);

    const pullButton = await screen.findByRole('button', { name: /Pull nomic-embed-text \(default\)/ });
    ollama.mockResolvedValueOnce({ reachable: true, base_url: 'http://localhost:11434', models: [{ name: 'nomic-embed-text' }] });

    act(() => {
      useEvents.getState()._append({
        type: 'semantic.pull',
        time: 't1',
        summary: '',
        ids: {},
        semanticPull: { model: 'nomic-embed-text', status: 'pulling', completed: 50, total: 100, done: false },
      });
    });

    await waitFor(() => expect(screen.getByRole('button', { name: /Pulling…/ })).toBeInTheDocument());
    expect(pullButton).toBeDisabled();

    act(() => {
      useEvents.getState()._append({
        type: 'semantic.pull',
        time: 't2',
        summary: '',
        ids: {},
        semanticPull: { model: 'nomic-embed-text', status: 'done', completed: 100, total: 100, done: true },
      });
    });

    // A completed pull refreshes the installed-models list.
    await waitFor(() => expect(ollama).toHaveBeenCalledTimes(2));
  });

  it('clicking a suggestion chip pulls that model', async () => {
    const user = userEvent.setup();
    ollama.mockResolvedValue({ reachable: true, base_url: 'http://localhost:11434', models: [] });
    render(<SemanticSearchPanel />);

    const pullButton = await screen.findByRole('button', { name: /Pull mxbai-embed-large/ });
    await user.click(pullButton);
    await waitFor(() => expect(ollamaPull).toHaveBeenCalledWith({ model: 'mxbai-embed-large', base_url: 'http://localhost:11434' }));
  });

  it('shows the Ollama-unreachable state with the install link and a Retry that re-checks', async () => {
    const user = userEvent.setup();
    ollama.mockResolvedValue({ reachable: false, base_url: 'http://localhost:11434', models: [], error: 'connection refused' });
    render(<SemanticSearchPanel />);

    await waitFor(() => expect(screen.getByText('Ollama is not running at http://localhost:11434.')).toBeInTheDocument());
    expect(screen.getByRole('link', { name: 'Install Ollama' })).toHaveAttribute('href', 'https://ollama.com/download');

    await user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(ollama).toHaveBeenCalledTimes(2));
  });

  it('OpenAI-compatible: switching provider swaps the base URL default and offers a password API key field', async () => {
    const user = userEvent.setup();
    render(<SemanticSearchPanel />);
    await waitFor(() => expect(screen.getByRole('radio', { name: 'Ollama' })).toBeInTheDocument());

    await user.click(screen.getByRole('radio', { name: 'OpenAI-compatible' }));
    expect(screen.getByDisplayValue('https://api.openai.com/v1')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'text-embedding-3-small' })).toBeInTheDocument();

    const apiKeyField = screen.getByLabelText('API key') as HTMLInputElement;
    expect(apiKeyField).toHaveAttribute('type', 'password');
  });

  it('shows "unchanged" as the API key placeholder when a key is already stored, and omits api_key from Save unless touched', async () => {
    const user = userEvent.setup();
    get.mockResolvedValue({ ...baseSettings, kind: 'openai', api_key_set: true, model: 'text-embedding-3-small' });
    render(<SemanticSearchPanel />);

    const apiKeyField = await screen.findByLabelText('API key');
    expect(apiKeyField).toHaveAttribute('placeholder', 'unchanged');

    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(update).toHaveBeenCalled());
    expect(update.mock.calls[0][0]).not.toHaveProperty('api_key');
  });
  it('turning "Doc sections" off saves kinds without docs, and examples cannot outlive operations', async () => {
    get.mockResolvedValue({
      ...baseSettings,
      kinds: ['operations', 'examples', 'memories', 'docs'],
      status: { ...baseSettings.status, by_kind: { docs: { embedded: 840, total: 840 } } },
    });
    const user = userEvent.setup();
    render(<SemanticSearchPanel />);

    const docs = await screen.findByRole('checkbox', { name: /Doc sections/ });
    expect(screen.getByText(/840 \/ 840 embedded/)).toBeInTheDocument();
    await user.click(docs);
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(update).toHaveBeenCalledWith(expect.objectContaining({ kinds: ['operations', 'examples', 'memories'] })));

    await user.click(screen.getByRole('checkbox', { name: /Operations/ }));
    expect(screen.getByRole('checkbox', { name: /Examples/ })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(update).toHaveBeenLastCalledWith(expect.objectContaining({ kinds: ['memories'] })));
  });

  it('prefixes follow the model until edited; an edit is sent, and Reset sends reset_prefixes', async () => {
    get.mockResolvedValue({
      ...baseSettings,
      query_prefix: 'search_query: ',
      document_prefix: 'search_document: ',
      default_query_prefix: 'search_query: ',
      default_document_prefix: 'search_document: ',
      prefixes_custom: false,
    });
    const user = userEvent.setup();
    render(<SemanticSearchPanel />);

    await user.click(await screen.findByRole('button', { name: /Task prefixes/ }));
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(update).toHaveBeenCalled());
    expect(update.mock.calls[0][0]).not.toHaveProperty('query_prefix');
    expect(update.mock.calls[0][0]).not.toHaveProperty('reset_prefixes');

    const doc = screen.getByRole('textbox', { name: /Document prefix/ });
    await user.clear(doc);
    await user.type(doc, 'passage: ');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(update).toHaveBeenLastCalledWith(expect.objectContaining({ query_prefix: 'search_query: ', document_prefix: 'passage: ' })),
    );

    await user.click(screen.getByRole('button', { name: /Reset to the model/ }));
    expect(screen.getByRole('textbox', { name: /Document prefix/ })).toHaveValue('search_document: ');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(update).toHaveBeenLastCalledWith(expect.objectContaining({ reset_prefixes: true })));
  });
  it('sends keep_alive for Ollama, as chosen', async () => {
    const user = userEvent.setup();
    render(<SemanticSearchPanel />);
    const select = await screen.findByRole('combobox', { name: /Keep the model in memory/ });
    await user.selectOptions(select, '0');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(update).toHaveBeenCalledWith(expect.objectContaining({ keep_alive: '0' })));
  });
});
