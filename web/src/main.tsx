import { Component, StrictMode, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import './app.css'
import './prefs'
import { App } from './App'

// Shows a reload prompt instead of a blank page if a screen crashes.
class Boundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) {
    return { error }
  }
  render() {
    if (!this.state.error) return this.props.children
    return (
      <div className="auth">
        <div className="card stack" style={{ padding: 32, maxWidth: 420 }}>
          <h1>Something broke</h1>
          <code className="muted">{this.state.error.message}</code>
          <div className="row">
            <button className="primary" onClick={() => location.reload()}>
              Reload
            </button>
            <a className="button" href="#/" onClick={() => this.setState({ error: null })}>
              Back to canvas
            </a>
          </div>
        </div>
      </div>
    )
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Boundary>
      <App />
    </Boundary>
  </StrictMode>,
)
