import { lazy, Suspense } from 'react';
import { API_MODE } from '@/api/config';
import { LoadingState } from '@/components/PageState';
import RealRegistrySitePanel from './RealRegistrySitePanel';

const MockRegistrySitePanel = lazy(() => import('./MockRegistrySitePanel'));

export default function RegistrySitePanel() {
  if (API_MODE === 'real') return <RealRegistrySitePanel />;
  return (
    <Suspense fallback={<LoadingState tip="加载演示资产结构" />}>
      <MockRegistrySitePanel />
    </Suspense>
  );
}
