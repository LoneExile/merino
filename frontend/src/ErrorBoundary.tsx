import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * Catches render exceptions so a bug cannot blank the panel.
 *
 * React 19 unmounts the entire tree on an uncaught render error. In a menubar
 * app that is indistinguishable from a paint failure or a dead backend: the
 * window is simply empty, with nothing to act on. This turns that silent void
 * into a readable message.
 *
 * Learned the hard way — mis-unwrapping an event payload turned the agent list
 * into a single object, `.filter` threw, and the panel went permanently blank
 * with no clue as to why.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("panel render failed", error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    return (
      <div className="crash">
        <strong>The panel hit an error.</strong>
        <pre>{error.message}</pre>
        <button className="btn" onClick={() => this.setState({ error: null })}>
          Retry
        </button>
      </div>
    );
  }
}
