import { BrowserRouter, Route, Routes } from 'react-router'
import { AuthProvider } from './auth'
import { ConfirmHost } from './components/confirm'
import Api from './pages/Api'
import Clients from './pages/Clients'
import ExternalNodes from './pages/ExternalNodes'
import Cascades from './pages/Cascades'
import Servers from './pages/Servers'
import Inbounds from './pages/Inbounds'
import Login from './pages/Login'
import Overview from './pages/Overview'
import Settings from './pages/Settings'
import Shell from './pages/Shell'
import SubPage from './pages/SubPage'
import Subscription from './pages/Subscription'

export default function App() {
  return (
    <AuthProvider>
      <ConfirmHost />
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/sub/:subId" element={<SubPage />} />
          <Route element={<Shell />}>
            <Route index element={<Overview />} />
            <Route path="inbounds" element={<Inbounds />} />
            <Route path="clients" element={<Clients />} />
            <Route path="servers" element={<Servers />} />
            <Route path="cascades" element={<Cascades />} />
            <Route path="nodes" element={<ExternalNodes />} />
            <Route path="subscription" element={<Subscription />} />
            <Route path="api" element={<Api />} />
            <Route path="settings" element={<Settings />} />
          </Route>
          {/* The subscription prefix is configurable, so any other path is treated as <prefix><id> for the user page. */}
          <Route path="*" element={<SubPage />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
