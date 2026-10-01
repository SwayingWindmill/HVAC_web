import { useCallback, useMemo } from 'react';
import { useLocation, useNavigate } from '@tanstack/react-router';

export type ScopeType = 'portfolio' | 'group' | 'site';

export interface ScopeItem {
  readonly id: string;
  readonly type: ScopeType;
  readonly name: string;
  readonly description?: string;
  readonly badge?: string;
}

export const AVAILABLE_SCOPES: readonly ScopeItem[] = [
  {
    id: 'portfolio',
    type: 'portfolio',
    name: '全集团资产总览',
    description: '涵盖华东、华南、华北共 12 个重点用能站点',
    badge: '集团',
  },
  {
    id: 'group:east',
    type: 'group',
    name: '华东区域商业综合体',
    description: '上海、杭州、南京共 5 个商业建筑',
    badge: '大区',
  },
  {
    id: 'group:south',
    type: 'group',
    name: '华南区域高新园区',
    description: '深圳、广州共 4 个工业及科技园区',
    badge: '大区',
  },
  {
    id: 'site:site-01',
    type: 'site',
    name: '上海恒隆广场',
    description: '建筑面积 26.8 万 m² · 暖通冷站一级能效示范站',
    badge: '示范站',
  },
  {
    id: 'site:site-02',
    type: 'site',
    name: '深圳科技园大厦',
    description: '建筑面积 18.5 万 m² · 双工质蓄冰与光伏储能系统',
    badge: '多能互补',
  },
  {
    id: 'site:site-03',
    type: 'site',
    name: '北京研发创新中心',
    description: '建筑面积 14.2 万 m² · 超低能耗绿色建筑',
    badge: '绿色三星',
  },
];

export function useScope() {
  const location = useLocation();
  const navigate = useNavigate();

  const searchParams = useMemo(() => {
    return new URLSearchParams(location.searchStr);
  }, [location.searchStr]);

  const rawScopeId = searchParams.get('scope') || 'site:site-01';

  const currentScope = useMemo(() => {
    const match = AVAILABLE_SCOPES.find((s) => s.id === rawScopeId);
    return match || AVAILABLE_SCOPES[3]; // default to 上海恒隆广场
  }, [rawScopeId]);

  const setScope = useCallback((scopeId: string) => {
    const params = new URLSearchParams(location.searchStr);
    params.set('scope', scopeId);
    const searchString = params.toString() ? `?${params.toString()}` : '';
    navigate({
      to: `${location.pathname}${searchString}${location.hash}`,
      replace: false,
    });
  }, [location.pathname, location.searchStr, location.hash, navigate]);

  return {
    currentScope,
    availableScopes: AVAILABLE_SCOPES,
    setScope,
    isPortfolio: currentScope.type === 'portfolio',
    isGroup: currentScope.type === 'group',
    isSite: currentScope.type === 'site',
  };
}
