import { useEffect } from 'react';

export function useRuleDirtyGuard(dirty: boolean) {
  useEffect(() => {
    if (!dirty) return undefined;
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handleBeforeUnload);
    return () => window.removeEventListener('beforeunload', handleBeforeUnload);
  }, [dirty]);
}

export function confirmDiscardRuleDraft(dirty: boolean): boolean {
  return !dirty || window.confirm('当前 Rule 草稿有未发布修改。确定放弃这些修改吗？');
}
