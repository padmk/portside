import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/fonts.css'
import './styles/themes.css'
import './styles/base.css'
import './styles/controls.css'
import './ui/lamp.css'
import { App } from './app/App'
import { startLive } from './state/live'

startLive()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
