import { useState } from 'react';

// Flow descriptions are usually written by an agent and routinely run to
// several paragraphs, which used to push the step list below the fold on the
// page whose main job is "run this flow and watch its steps". Anything longer
// than a couple of lines is clamped to two lines with a toggle.
//
// The "is it long?" test is a character/newline count rather than a measured
// scrollHeight: it gives the same answer on every render (and in jsdom, where
// nothing has a height), so the toggle never flickers in or out as fonts
// load or the column resizes.
const collapseOverChars = 180;

export function FlowDescription({ text }: { text?: string }) {
  const [expanded, setExpanded] = useState(false);
  if (!text) return null;

  const long = text.length > collapseOverChars || text.split('\n').length > 2;
  const clamped = long && !expanded;

  return (
    <div className="mb-3">
      <p className={`whitespace-pre-wrap text-sm text-slate-500 ${clamped ? 'line-clamp-2' : ''}`}>{text}</p>
      {long && (
        <button
          type="button"
          onClick={() => setExpanded((e) => !e)}
          className="mt-0.5 text-xs text-sky-700 hover:underline dark:text-sky-400"
        >
          {expanded ? 'Show less' : 'Show more'}
        </button>
      )}
    </div>
  );
}
