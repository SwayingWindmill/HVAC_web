/**
 * Smart Energy Unit Formatter (Section 74)
 * Standardizes engineering units and display representation:
 * Energy (kWh / MWh), Power (kW / MW), Currency (¥), Carbon (tCO2), Temperature (°C), Percentage (%).
 */

export interface FormattedMetric {
  readonly value: string;
  readonly unit: string;
  readonly formatted: string;
}

export function formatEnergy(
  kwh: number,
  options?: { decimals?: number; forceUnit?: 'kWh' | 'MWh' | 'GWh' }
): FormattedMetric {
  const decimals = options?.decimals ?? 1;

  if (options?.forceUnit === 'GWh' || (!options?.forceUnit && Math.abs(kwh) >= 1_000_000)) {
    const val = (kwh / 1_000_000).toFixed(decimals);
    return { value: val, unit: 'GWh', formatted: `${Number(val).toLocaleString()} GWh` };
  }

  if (options?.forceUnit === 'MWh' || (!options?.forceUnit && Math.abs(kwh) >= 10_000)) {
    const val = (kwh / 1_000).toFixed(decimals);
    return { value: val, unit: 'MWh', formatted: `${Number(val).toLocaleString()} MWh` };
  }

  const val = kwh.toFixed(decimals > 0 ? decimals : 0);
  return { value: val, unit: 'kWh', formatted: `${Number(val).toLocaleString()} kWh` };
}

export function formatPower(
  kw: number,
  options?: { decimals?: number; forceUnit?: 'kW' | 'MW' }
): FormattedMetric {
  const decimals = options?.decimals ?? 1;

  if (options?.forceUnit === 'MW' || (!options?.forceUnit && Math.abs(kw) >= 10_000)) {
    const val = (kw / 1_000).toFixed(decimals);
    return { value: val, unit: 'MW', formatted: `${Number(val).toLocaleString()} MW` };
  }

  const val = kw.toFixed(decimals);
  return { value: val, unit: 'kW', formatted: `${Number(val).toLocaleString()} kW` };
}

export function formatCurrency(
  amount: number,
  options?: { decimals?: number; symbol?: string; largeWan?: boolean }
): FormattedMetric {
  const decimals = options?.decimals ?? 1;
  const symbol = options?.symbol ?? '¥';
  const useWan = options?.largeWan ?? true;

  if (useWan && Math.abs(amount) >= 10_000) {
    const val = (amount / 10_000).toFixed(decimals);
    return {
      value: val,
      unit: '万元',
      formatted: `${symbol}${Number(val).toLocaleString()} 万`,
    };
  }

  const val = Math.round(amount).toString();
  return {
    value: val,
    unit: '元',
    formatted: `${symbol}${Number(val).toLocaleString()}`,
  };
}

export function formatCarbon(
  kg: number,
  options?: { decimals?: number }
): FormattedMetric {
  const decimals = options?.decimals ?? 1;
  if (Math.abs(kg) >= 1_000) {
    const val = (kg / 1_000).toFixed(decimals);
    return { value: val, unit: 'tCO₂', formatted: `${Number(val).toLocaleString()} tCO₂` };
  }
  const val = kg.toFixed(decimals);
  return { value: val, unit: 'kgCO₂', formatted: `${Number(val).toLocaleString()} kgCO₂` };
}

export function formatPercent(
  ratio: number,
  options?: { decimals?: number; showSign?: boolean }
): string {
  const decimals = options?.decimals ?? 1;
  const pct = (ratio * 100).toFixed(decimals);
  const sign = options?.showSign && ratio > 0 ? '+' : '';
  return `${sign}${pct}%`;
}

export function formatTemperature(
  deg: number,
  options?: { decimals?: number }
): string {
  const decimals = options?.decimals ?? 1;
  return `${deg.toFixed(decimals)}°C`;
}
