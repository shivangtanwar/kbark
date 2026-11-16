import { Component, type ReactNode } from 'react';
import { Button, Card, Empty } from './ui';

/**
 * Keeps one page's rendering error from blanking the whole console.
 * The navigation stays usable, and the message says what to do.
 */
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <Card>
        <Empty title="This page could not be shown">
          <p>The data is unaffected and nothing was changed in any cluster. Reload to try again.</p>
          <p className="mt-2 font-mono text-xs text-fg-3">{this.state.error.message}</p>
          <Button className="mt-4" onClick={() => window.location.reload()}>Reload</Button>
        </Empty>
      </Card>
    );
  }
}
