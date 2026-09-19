// /ui/settings (PLAN §34f, "a Settings page (/ui/settings) holds semantic
// search, daemon and updates"). Each panel owns its own GET/PUT calls; this
// page is just the shell so the panels can be lazy-loaded together as one
// chunk without pulling their weight into every other route.
import DaemonPanel from './settings/DaemonPanel';
import SemanticSearchPanel from './settings/SemanticSearchPanel';
import UpdatesPanel from './settings/UpdatesPanel';

export default function SettingsPage() {
  return (
    <div className="p-4">
      <h1 className="mb-4 text-lg font-semibold">Settings</h1>
      <SemanticSearchPanel />
      <DaemonPanel />
      <UpdatesPanel />
    </div>
  );
}
