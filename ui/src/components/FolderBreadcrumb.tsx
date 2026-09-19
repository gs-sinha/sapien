// Breadcrumb above the Flows/Memories/Examples list, tracking the folder
// sidebar's selection (PLAN §34f item 6). Only rendered by the caller
// alongside the sidebar, so it never shows in a workspace with no folders.
export function FolderBreadcrumb({ folder, onNavigate }: { folder: string | null; onNavigate: (folder: string | null) => void }) {
  const parts = folder ? folder.split('/') : [];
  return (
    <nav aria-label="Folder" className="mb-2 flex flex-wrap items-center gap-1 text-xs text-slate-500">
      <button type="button" onClick={() => onNavigate(null)} className="hover:underline">
        All
      </button>
      {parts.map((p, i) => {
        const path = parts.slice(0, i + 1).join('/');
        return (
          <span key={path} className="flex items-center gap-1">
            <span className="text-slate-300 dark:text-slate-700">/</span>
            <button type="button" onClick={() => onNavigate(path)} className="hover:underline">
              {p}
            </button>
          </span>
        );
      })}
    </nav>
  );
}
