import { API_MODE } from '@/api/config';
import RealAssets from './RealAssets';
import MockAssets from './MockAssets';

export default function Assets() {
  if (API_MODE === 'real') return <RealAssets />;
  return <MockAssets />;
}
