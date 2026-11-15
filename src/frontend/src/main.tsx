import { Component, type ErrorInfo, type ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './app/App';
import './styles.css';

class ErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidCatch(_error: Error, _info: ErrorInfo) { /* Do not log state or credentials. */ }
  render() {
    return this.state.failed ? <main><h1>We couldn’t open your workspace.</h1><p>Your saved credentials have not been erased. Check browser storage permissions and refresh to recover.</p><button onClick={() => location.reload()}>Refresh workspace</button></main> : this.props.children;
  }
}
const root = document.getElementById('root');
if (!root) throw new Error('Application root is missing');
createRoot(root).render(<ErrorBoundary><App /></ErrorBoundary>);
