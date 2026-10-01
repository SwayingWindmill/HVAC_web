import { useEffect } from 'react';

export function useRegistryDirtyGuard(dirty: boolean) {
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

export function confirmDiscardRegistryDraft(dirty: boolean): boolean {
  return !dirty || window.confirm('当前 Registry 表单有未保存修改。确定放弃这些修改吗？');
}
