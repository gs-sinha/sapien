import { useEffect, useRef, useState } from 'react';
import { getTerminalTargets } from '../../api/agentExtra';
import { useAsync } from '../../lib/useAsync';
import { startSession, useAgentTerminal } from '../../state/agentTerminal';
import { useWorkspace } from '../../state/workspace';
import { StatusPill } from '../StatusPill';

// pickDefaultCommand prefers claude, then codex, then whatever else is
// available (the user's shell, per the server's allowlist) -- claude and
// codex are why this pane exists; the shell is the fallback.
function pickDefaultCommand(commands: string[]): string {
  for (const c of ['claude', 'codex']) {
    if (commands.includes(c)) return c;
  }
  return commands[0] || '';
}

// Picker is the agent pane's command/directory picker and Start/Restart
// control (PLAN §34c Phase 7b item 4), backed by GET /v1/terminal/targets.
// When initialPrompt is set (the page's ?prompt= hand-off, item 5) and no
// session is running yet, it auto-starts once targets have loaded so the
// user doesn't have to click Start themselves before seeing their prompt
// waiting in the terminal.
export function Picker({ initialPrompt }: { initialPrompt?: string }) {
  // Keyed on the selected workspace, not fetched once on mount: the
  // directories on offer are that workspace's, so a switch has to re-ask.
  // In practice WorkspacePicker reloads the page on a switch, but the
  // page must not depend on that -- state/workspace.ts also drops a
  // selection the daemon no longer serves, with no reload.
  const workspace = useWorkspace((s) => s.current);
  const { data: targets, loading, error } = useAsync(() => getTerminalTargets(), [workspace]);
  const { phase, errorMessage } = useAgentTerminal();
  const [command, setCommand] = useState('');
  const [dir, setDir] = useState('');
  const consumedPrompt = useRef(false);
  const autoStarted = useRef(false);

  useEffect(() => {
    if (!targets) return;
    // Commands are workspace-independent (the daemon resolves them on its
    // own PATH), so a chosen one survives. A directory does not: one held
    // from another workspace is not in this list and GET /v1/terminal would
    // reject it, so it falls back to this workspace's first entry.
    setCommand((c) => c || pickDefaultCommand(targets.commands));
    setDir((d) => (targets.dirs.some((t) => t.path === d) ? d : targets.dirs[0]?.path || ''));
  }, [targets]);

  const start = () => {
    if (!command || !dir) return;
    const prompt = consumedPrompt.current ? undefined : initialPrompt;
    consumedPrompt.current = true;
    startSession({ command, dir, prompt });
  };

  useEffect(() => {
    if (autoStarted.current || !initialPrompt) return;
    if (phase !== 'idle' || !command || !dir) return;
    autoStarted.current = true;
    start();
    // start() is stable enough here: this effect's only job is a one-time
    // kick once command/dir have defaults and nothing else has started a
    // session yet.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialPrompt, phase, command, dir]);

  const busy = phase === 'connecting';
  const noCommands = !loading && (targets?.commands.length ?? 0) === 0;

  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-slate-200 p-2 text-sm dark:border-slate-800">
      <select
        value={command}
        onChange={(e) => setCommand(e.target.value)}
        disabled={busy || noCommands}
        aria-label="Command"
        className="rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
      >
        {noCommands && <option value="">No allowed command found on PATH</option>}
        {(targets?.commands || []).map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>

      <select
        value={dir}
        onChange={(e) => setDir(e.target.value)}
        disabled={busy || !(targets?.dirs.length ?? 0)}
        aria-label="Directory"
        className="min-w-[14rem] rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
      >
        {(targets?.dirs || []).map((d) => (
          <option key={d.path} value={d.path} title={d.path}>
            {d.label}
          </option>
        ))}
      </select>

      <button
        type="button"
        onClick={start}
        disabled={busy || !command || !dir}
        className="rounded bg-slate-900 px-3 py-1 text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
      >
        {phase === 'idle' ? 'Start' : 'Restart'}
      </button>

      <StatusPill status={phase} />

      {loading && <span className="text-slate-400">Loading targets…</span>}
      {error && <span className="text-red-600">{error.message}</span>}
      {phase === 'error' && errorMessage && <span className="text-red-600">{errorMessage}</span>}
    </div>
  );
}
