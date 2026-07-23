import { lazy, Suspense } from 'react';
import { API_MODE } from '@/api/config';
import { LoadingState } from '@/components/PageState';
import RealAssets from './RealAssets';

const MockAssets = lazy(() => import('./MockAssets'));

export default function Assets() {
  if (API_MODE === 'real') return <RealAssets />;
  return (
    <Suspense fallback={<LoadingState tip="加载演示设备台账" />}>
      <MockAssets />
    </Suspense>
  );
}
