import { useMemo } from 'react';
import { useRegistryAssetModel } from '@/api/registry';
import { commandLabels, controlTargets, type CommandLabel } from './control-model';

const NO_LABELS: ReadonlyMap<string, CommandLabel> = new Map();

/** The Site's command points named as the control page names them, read from the Registry. */
export function useCommandLabels(siteId: string): ReadonlyMap<string, CommandLabel> {
  const model = useRegistryAssetModel(siteId);
  return useMemo(() => (model.data ? commandLabels(controlTargets(model.data, new Date())) : NO_LABELS), [model.data]);
}
