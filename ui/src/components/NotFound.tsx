import { Link } from 'react-router-dom';

export function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center gap-3 p-16 text-center">
      <div className="text-lg font-medium">Page not found</div>
      <p className="text-sm text-slate-500">There is nothing here.</p>
      <Link to="/ui/flows" className="text-sm text-sky-700 underline dark:text-sky-400">
        Go to Flows
      </Link>
    </div>
  );
}
