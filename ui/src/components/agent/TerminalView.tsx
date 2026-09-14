import { useEffect, useRef } from 'react';
import { attachTerminal, fitTerminal, sendResize, startSession, useAgentTerminal } from '../../state/agentTerminal';

// TerminalView mounts the persistent, module-level xterm.js terminal
// (state/agentTerminal.ts) into a wrapper it owns, keeps it fit to the
// wrapper's size (fit addon + ResizeObserver, pushing a resize control
// frame whenever the size actually changes), and shows an exit/error
// notice with a Restart button once the session ends -- PLAN §34c Phase
// 7b item 4. ResizeObserver is guarded because jsdom (this project's test
// environment) doesn't implement it.
export function TerminalView() {
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const { phase, command, dir, exitCode, errorMessage, endedNote } = useAgentTerminal();

  useEffect(() => {
    const el = wrapperRef.current;
    if (!el) return;

    attachTerminal(el);
    const dims = fitTerminal();
    if (dims) sendResize(dims.cols, dims.rows);

    if (typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver(() => {
      const d = fitTerminal();
      if (d) sendResize(d.cols, d.rows);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const ended = phase === 'exited' || phase === 'error';

  return (
    <div className="relative flex h-full min-h-0 w-full flex-col bg-[#1b1a17]">
      <div ref={wrapperRef} className="min-h-0 flex-1 overflow-hidden p-1" />
      {ended && (
        <div className="absolute inset-x-0 bottom-0 flex items-center justify-between gap-3 border-t border-slate-700 bg-slate-900/95 px-3 py-2 text-sm text-slate-100">
          <span>
            {/* endedNote is set when something other than the process itself
                ended the session (today: a workspace switch), and replaces
                the generic notice, which would otherwise read as if the
                agent had exited on its own. */}
            {phase === 'error'
              ? `Could not start: ${errorMessage || 'unknown error'}`
              : endedNote || `Session ended${exitCode !== null ? ` (exit code ${exitCode})` : ''}.`}
          </span>
          <button
            type="button"
            onClick={() => command && dir && startSession({ command, dir })}
            disabled={!command || !dir}
            className="shrink-0 rounded border border-slate-500 px-3 py-1 text-xs hover:bg-slate-800 disabled:opacity-50"
          >
            Restart
          </button>
        </div>
      )}
    </div>
  );
}
