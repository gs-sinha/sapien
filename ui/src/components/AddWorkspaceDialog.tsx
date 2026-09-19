// "Add workspace" dialog, reached from the workspace picker: create a new
// folder (POST /v1/workspaces/create), clone a git repository (POST
// /v1/workspaces/clone), or register an existing folder (POST
// /v1/workspaces). Parent/checkout paths are chosen through the daemon's
// folder browser (components/FolderPicker) rather than a browser file dialog.
import { useState } from 'react';
import { workspacesApi } from '../api/client';
import { pushToast } from '../state/toast';
import { FolderPicker } from './FolderPicker';
import { Modal } from './run/Modal';
import { buttonCls } from '../pages/services/BindingPanel';

type Tab = 'new' | 'clone' | 'existing';

// join composes a child onto a parent path, tolerating trailing/leading
// slashes on either side (the daemon's paths have no double separators).
function join(a: string, b: string): string {
  if (!a) return b;
  if (!b) return a;
  return `${a.replace(/\/+$/, '')}/${b.replace(/^\/+/, '')}`;
}

// deriveName is the workspace name for a clone URL left blank: its last path
// segment with a trailing ".git" stripped. Handles both https:// and the
// scp-like git@host:path syntax.
function deriveName(url: string): string {
  const seg = url.trim().replace(/\/+$/, '').split(/[/:]/).pop() || '';
  return seg.replace(/\.git$/, '');
}

export function AddWorkspaceDialog({ onClose, onAdded }: { onClose: () => void; onAdded: (dir: string) => void }) {
  const [tab, setTab] = useState<Tab>('new');
  const [parent, setParent] = useState('');
  const [name, setName] = useState('');
  const [gitInit, setGitInit] = useState(false);
  const [url, setUrl] = useState('');
  const [existingDir, setExistingDir] = useState('');
  // Which field a FolderPicker is currently choosing for; null = closed.
  const [browseFor, setBrowseFor] = useState<'parent' | 'existing' | null>(null);
  const [busy, setBusy] = useState(false);

  const resolvedName = name.trim() || deriveName(url);
  const canSubmit =
    tab === 'new' ? !!parent.trim() && !!name.trim() : tab === 'clone' ? !!url.trim() && !!parent.trim() : !!existingDir.trim();

  const submit = async () => {
    setBusy(true);
    try {
      let dir: string;
      if (tab === 'new') {
        dir = join(parent.trim(), name.trim());
        await workspacesApi.create(dir, name.trim(), gitInit);
        pushToast('success', `Created workspace ${name.trim()}.`);
      } else if (tab === 'clone') {
        dir = join(parent.trim(), resolvedName);
        await workspacesApi.clone(url.trim(), dir, name.trim());
        pushToast('success', `Cloned ${resolvedName}.`);
      } else {
        dir = existingDir.trim();
        await workspacesApi.register(dir);
        pushToast('success', `Registered ${dir}.`);
      }
      onAdded(dir);
    } catch (e) {
      pushToast('error', e instanceof Error ? e.message : 'Failed to add workspace.');
    } finally {
      setBusy(false);
    }
  };

  const tabs: Array<{ id: Tab; label: string }> = [
    { id: 'new', label: 'New' },
    { id: 'clone', label: 'Clone' },
    { id: 'existing', label: 'Existing' },
  ];

  return (
    <Modal title="Add workspace" onClose={onClose}>
      <div className="mb-3 flex gap-1 border-b border-slate-200 dark:border-slate-800" role="tablist">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={tab === t.id}
            onClick={() => setTab(t.id)}
            className={`-mb-px border-b-2 px-2 py-1 text-xs ${
              tab === t.id
                ? 'border-slate-900 font-medium text-slate-900 dark:border-slate-100 dark:text-slate-100'
                : 'border-transparent text-slate-500 hover:text-slate-700 dark:hover:text-slate-300'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div className="space-y-3 text-sm">
        {tab === 'new' && (
          <>
            <BrowseField label="Parent folder" value={parent} onBrowse={() => setBrowseFor('parent')} />
            <label className="block">
              <span className="mb-1 block text-xs text-slate-500">Name</span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-workspace"
                className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
              />
            </label>
            <label className="flex items-center gap-2 text-xs text-slate-500">
              <input type="checkbox" checked={gitInit} onChange={(e) => setGitInit(e.target.checked)} />
              initialize a git repository
            </label>
          </>
        )}

        {tab === 'clone' && (
          <>
            <label className="block">
              <span className="mb-1 block text-xs text-slate-500">Repository URL</span>
              <input
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://github.com/acme/orders.git"
                className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
              />
            </label>
            <BrowseField label="Parent folder" value={parent} onBrowse={() => setBrowseFor('parent')} />
            <label className="block">
              <span className="mb-1 block text-xs text-slate-500">Name (optional)</span>
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={deriveName(url) || 'my-workspace'}
                className="w-full rounded border border-slate-300 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900"
              />
            </label>
          </>
        )}

        {tab === 'existing' && (
          <BrowseField label="Folder" value={existingDir} onBrowse={() => setBrowseFor('existing')} />
        )}

        <div className="flex justify-end gap-2 pt-1">
          <button type="button" onClick={onClose} className={buttonCls}>
            Cancel
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={busy || !canSubmit}
            className="rounded bg-slate-900 px-3 py-1 text-xs text-white disabled:opacity-50 dark:bg-slate-100 dark:text-slate-900"
          >
            {busy ? (tab === 'clone' ? 'Cloning…' : 'Adding…') : 'Add'}
          </button>
        </div>
      </div>

      {browseFor && (
        <FolderPicker
          title={browseFor === 'existing' ? 'Choose an existing folder' : 'Choose a parent folder'}
          onSelect={(dir) => {
            if (browseFor === 'existing') setExistingDir(dir);
            else setParent(dir);
            setBrowseFor(null);
          }}
          onClose={() => setBrowseFor(null)}
        />
      )}
    </Modal>
  );
}

// A read-only path field with the "Browse…" button that opens the daemon's
// folder picker; the value is only ever set by selecting a folder.
function BrowseField({ label, value, onBrowse }: { label: string; value: string; onBrowse: () => void }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs text-slate-500">{label}</span>
      <div className="flex gap-2">
        <input
          value={value}
          readOnly
          placeholder="Choose a folder…"
          className="min-w-0 flex-1 rounded border border-slate-300 bg-white px-2 py-1 font-mono text-xs dark:border-slate-700 dark:bg-slate-900"
        />
        <button type="button" onClick={onBrowse} className={buttonCls}>
          Browse…
        </button>
      </div>
    </label>
  );
}
