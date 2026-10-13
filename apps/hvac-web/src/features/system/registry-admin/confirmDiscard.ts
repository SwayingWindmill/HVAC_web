export function confirmDiscardRegistryDraft(dirty: boolean): boolean {
  return !dirty || window.confirm('当前登记表单有未保存修改。确定放弃这些修改吗？');
}
