import { useSearchParams } from 'react-router-dom';
import { Picker } from '../components/agent/Picker';
import { TerminalView } from '../components/agent/TerminalView';

// Phase 7b agent pane (PLAN §34c): runs the user's own coding agent
// (claude or codex, via GET /v1/terminal's allowlist, or a plain shell) in
// a real PTY rendered with xterm.js. Terminal only: the runs section is the
// place to look at a run, so this page does not duplicate it. The terminal
// (state/agentTerminal.ts) is a module-level singleton that survives
// navigating elsewhere in the SPA and back; see ui/README.md "Agent pane"
// for what still ends the session (closing the tab, or an explicit Restart).
//
// ?prompt=<text> (a hand-off from elsewhere, agentExtra.ts's
// agentHandoffURL) is read here and handed to Picker, which auto-starts a
// session and pastes it into the PTY once the agent's own output settles.
export default function AgentPage() {
  const [params] = useSearchParams();
  const initialPrompt = params.get('prompt') || undefined;

  return (
    <div className="flex h-full min-w-0 flex-col overflow-hidden">
      <Picker initialPrompt={initialPrompt} />
      <div className="min-h-0 flex-1">
        <TerminalView />
      </div>
    </div>
  );
}
