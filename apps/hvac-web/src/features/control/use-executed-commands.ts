import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { useRegistryAssetModel } from '@/api/registry';
import { formatTime } from '@/lib/operator-format';
import { siteCommandsQuery } from './command-api';
import { commandSummary } from './command-presentation';
import { commandLabels, controlTargets } from './control-model';

export interface ExecutedCommand {
  readonly commandId: string;
  /** When and what, as the control page names it: 10/12 14:30 · CHILLER-01 冷冻水出水设定温度 → 8.0 °C */
  readonly description: string;
}

/** The Site's recent commands that took effect, described in the operator's words. */
export function useExecutedCommands(siteId: string, timeZone: string, enabled = true) {
  const commands = useQuery({ ...siteCommandsQuery(siteId), enabled });
  const model = useRegistryAssetModel(siteId, enabled);
  const executed = useMemo((): readonly ExecutedCommand[] => {
    const labels = commandLabels(model.data ? controlTargets(model.data, new Date()) : []);
    return (commands.data ?? []).filter((command) => command.status === 'SUCCEEDED').map((command) => {
      const summary = commandSummary(command, labels);
      return { commandId: command.commandId, description: `${formatTime(command.updatedAt, timeZone)} · ${summary.target} ${summary.title}` };
    });
  }, [commands.data, model.data, timeZone]);
  return { executed, isPending: commands.isPending, isError: commands.isError };
}
