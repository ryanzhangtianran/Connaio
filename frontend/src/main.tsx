import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import Monitor from './Monitor'
import { platform } from './format'

// e.g. Linux windows have the system's title bar and no blur behind them (see .linux in style.css)
document.documentElement.classList.add(platform)
// the floating process monitor loads the same page as /?view=monitor (app.go)
const monitor = new URLSearchParams(location.search).get('view') === 'monitor'
if (monitor) document.documentElement.classList.add('monitor-view')

// a failed Go call is already listed in the events (MarshalError in app.go): no page catches those
window.addEventListener('unhandledrejection', (e) => e.reason?.name === 'RuntimeError' && e.preventDefault())

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    {monitor ? <Monitor /> : <App />}
  </React.StrictMode>,
)
