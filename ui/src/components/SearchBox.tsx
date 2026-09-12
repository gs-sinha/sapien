import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

export function SearchBox() {
  const [value, setValue] = useState('');
  const navigate = useNavigate();

  const go = () => {
    const q = value.trim();
    if (!q) return;
    navigate(`/ui/operations?q=${encodeURIComponent(q)}`);
  };

  return (
    <form
      className="flex-1"
      onSubmit={(e) => {
        e.preventDefault();
        go();
      }}
    >
      <input
        type="search"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="Search intent, e.g. allocate rider"
        className="w-full max-w-md rounded border border-slate-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-slate-500 dark:border-slate-700 dark:bg-slate-900"
      />
    </form>
  );
}
