import { Routes, Route, Navigate } from 'react-router-dom';
import AppShell from '@/layout/AppShell';
import Dashboard from '@/pages/Dashboard';
import Optimize from '@/pages/Optimize';
import Fdd from '@/pages/Fdd';
import Alarms from '@/pages/Alarms';
import BigScreen from '@/pages/BigScreen';
import Assets from '@/pages/Assets';
import Energy from '@/pages/Energy';
import Cost from '@/pages/Cost';
import Ai from '@/pages/Ai';
import System from '@/pages/System';
import NotFound from '@/pages/NotFound';
import { MODULES } from '@/store/ui';

// Real pages built so far; the rest remain placeholder until their tickets land.
const REAL_PAGES: Record<string, () => JSX.Element> = {
  '/assets': Assets,
  '/energy': Energy,
  '/cost': Cost,
  '/ai': Ai,
  '/system': System,
  '/optimize': Optimize,
  '/fdd': Fdd,
  '/alarms': Alarms,
};

function moduleRoutes() {
  // /bigscreen is a full-screen takeover rendered outside AppShell (see route below).
  return MODULES.filter((m) => m.path !== '/dashboard' && m.path !== '/bigscreen').map((m) => {
    const Real = REAL_PAGES[m.path];
    return <Route key={m.path} path={m.path} element={Real ? <Real /> : <NotFound />} />;
  });
}

export default function App() {
  return (
    <Routes>
      {/* Full-screen demo big screen — independent of the app shell (#9 / #12) */}
      <Route path="/bigscreen" element={<BigScreen />} />
      <Route element={<AppShell />}>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={<Dashboard />} />
        {moduleRoutes()}
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
