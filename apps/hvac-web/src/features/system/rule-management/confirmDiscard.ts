export function confirmDiscardRuleDraft(dirty: boolean): boolean {
  return !dirty || window.confirm('当前 Rule 草稿有未发布修改。确定放弃这些修改吗？');
}
