import { CopyButton } from '../../components/run/CopyButton';
import { YamlView } from '../../components/YamlView';

export function YamlSourcePanel({ source }: { source: string }) {
  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <h2 className="text-sm font-semibold">YAML</h2>
        <CopyButton text={source} />
      </div>
      {source ? <YamlView source={source} /> : <div className="text-xs text-slate-400">No source available for this flow.</div>}
    </div>
  );
}
