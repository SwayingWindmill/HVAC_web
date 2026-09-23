export const numberZh = (value: number) => value.toLocaleString('zh-CN');

export const currencyCny = (value: number, options?: { round?: boolean }) => {
  const amount = options?.round === false ? value : Math.round(value);
  return `¥${amount.toLocaleString('zh-CN')}`;
};

export const percentText = (value: number, options?: { signed?: boolean; digits?: number }) => {
  const digits = options?.digits ?? 0;
  const fixed = value.toFixed(digits);
  return `${options?.signed && value > 0 ? '+' : ''}${fixed}%`;
};
