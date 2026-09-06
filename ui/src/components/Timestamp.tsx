const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 31536000],
  ['month', 2592000],
  ['week', 604800],
  ['day', 86400],
  ['hour', 3600],
  ['minute', 60],
];

const rtf = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

function relative(date: Date): string {
  const diffSec = Math.round((date.getTime() - Date.now()) / 1000);
  for (const [unit, secs] of units) {
    if (Math.abs(diffSec) >= secs) return rtf.format(Math.round(diffSec / secs), unit);
  }
  return rtf.format(diffSec, 'second');
}

// Relative time, with the absolute local timestamp available on hover via
// the native `title` tooltip (no extra JS for the hover itself).
export function Timestamp({ value }: { value?: string }) {
  if (!value) return <span className="text-slate-400">-</span>;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return <span className="text-slate-400">{value}</span>;
  return (
    <span title={date.toLocaleString()} className="whitespace-nowrap">
      {relative(date)}
    </span>
  );
}
