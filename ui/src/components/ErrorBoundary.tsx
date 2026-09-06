import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // eslint-disable-next-line no-console
    console.error('ui error boundary', error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="m-6 rounded border border-red-300 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
          <div className="font-medium">Something went wrong rendering this page.</div>
          <div className="mt-1 font-mono text-xs">{this.state.error.message}</div>
          <button
            type="button"
            className="mt-3 rounded border border-red-400 px-2 py-1 text-xs"
            onClick={() => this.setState({ error: null })}
          >
            Try again
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
