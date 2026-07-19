import axios from 'axios';
import { REST_BASE } from './config';
import { getSiteId, getToken } from './auth';

export const http = axios.create({ baseURL: REST_BASE, timeout: 10_000 });

http.interceptors.request.use((cfg) => {
  const siteId = getSiteId();
  if (siteId) cfg.headers.set('X-Site-Id', siteId);
  const token = getToken();
  if (token) cfg.headers.set('Authorization', `Bearer ${token}`);
  return cfg;
});

/**
 * Tolerant unwrap: the backend response envelope is inconsistent (#4 gap 3).
 * We accept { code:0, data } / { code:200, data } / bare data and always return `data`.
 */
export async function unwrap<T>(call: Promise<{ data: unknown }>): Promise<T> {
  const res = await call;
  const body = res.data as { data?: unknown } | unknown;
  if (body && typeof body === 'object' && 'data' in body) return (body as { data: T }).data;
  return body as T;
}
