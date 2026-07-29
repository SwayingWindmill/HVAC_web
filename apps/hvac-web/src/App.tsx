import { lazy, Suspense, type ComponentType, type ReactElement } from 'react';
import { Routes, Route, Navigate } from 'react-router';
import AppShell from '@/layout/AppShell';
import NotFound from '@/pages/NotFound';
import { MODULES } from '@/store/ui';
import RequirePermission from '@/auth/RequirePermission';
import { LoadingState } from '@/components/PageState';
import { routeSubjectFromModuleKey } from '@/auth/permissions';

const Dashboard = lazy(() => import('@/pages/Dashboard'));
const Optimize = lazy(() => import('@/pages/Optimize'));
const Fdd = lazy(() => import('@/pages/Fdd'));
const Alarms = lazy(() => import('@/pages/Alarms'));
const BigScreen = lazy(() => import('@/pages/BigScreen'));
const Assets = lazy(() => import('@/pages/Assets'));
const Commands = lazy(() => import('@/pages/Commands'));
const Energy = lazy(() => import('@/pages/Energy'));
const EnergyYear = lazy(() => import('@/pages/Energy/Year'));
const EnergyMonth = lazy(() => import('@/pages/Energy/Month'));
const EnergyWeek = lazy(() => import('@/pages/Energy/Week'));
const EnergyDay = lazy(() => import('@/pages/Energy/Day'));
const Cost = lazy(() => import('@/pages/Cost'));
const Ai = lazy(() => import('@/pages/Ai'));
const System = lazy(() => import('@/pages/System'));

type LazyPage = ComponentType<Record<string, never>>;

// Real pages built so far; each entry is route-level lazy-loaded.
const REAL_PAGES: Record<string, LazyPage> = {
  '/assets': Assets,
  '/commands': Commands,
  '/cost': Cost,
  '/ai': Ai,
  '/system': System,
  '/optimize': Optimize,
  '/fdd': Fdd,
  '/alarms': Alarms,
};

function withSuspense(element: ReactElement) {
  return <Suspense fallback={<LoadingState tip="加载页面" />}>{element}</Suspense>;
}

function moduleRoutes() {
  // /bigscreen is a full-screen takeover rendered outside AppShell (see route below).
  return MODULES.filter((m) => m.path !== '/dashboard' && m.path !== '/bigscreen' && m.path !== '/energy').map((m) => {
    const Real = REAL_PAGES[m.path];
    const subject = routeSubjectFromModuleKey(m.key);
    const element = Real ? withSuspense(<Real />) : <NotFound />;
    return (
      <Route
        key={m.path}
        path={m.path}
        element={subject ? <RequirePermission subject={subject}>{element}</RequirePermission> : element}
      />
    );
  });
}

export default function App() {
  const energySubject = routeSubjectFromModuleKey('energy');
  const energyElement = withSuspense(<Energy />);
  const commandSubject = routeSubjectFromModuleKey('commands');
  const commandElement = withSuspense(<Commands />);

  return (
    <Routes>
      {/* Full-screen demo big screen — independent of the app shell (#9 / #12) */}
      <Route path="/bigscreen" element={withSuspense(<BigScreen />)} />
      <Route element={<AppShell />}>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={withSuspense(<Dashboard />)} />
        <Route
          path="/energy"
          element={energySubject ? <RequirePermission subject={energySubject}>{energyElement}</RequirePermission> : energyElement}
        >
          <Route index element={<Navigate to="month" replace />} />
          <Route path="year" element={withSuspense(<EnergyYear />)} />
          <Route path="month" element={withSuspense(<EnergyMonth />)} />
          <Route path="week" element={withSuspense(<EnergyWeek />)} />
          <Route path="day" element={withSuspense(<EnergyDay />)} />
        </Route>
        <Route
          path="/commands/:commandId"
          element={commandSubject ? <RequirePermission subject={commandSubject}>{commandElement}</RequirePermission> : commandElement}
        />
        {moduleRoutes()}
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
