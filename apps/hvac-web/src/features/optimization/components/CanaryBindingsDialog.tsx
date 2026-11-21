import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { GitBranch, RotateCcw } from 'lucide-react';
import { useState, type KeyboardEvent } from 'react';
import { toast } from 'sonner';
import { FactTile } from '@/blocks/fact-list';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type {
  OptimizationCanaryBindingRecord,
  OptimizationCanaryBindingStatus,
} from '@/api/generated/platformGateway.gen';
import { formatTime } from '@/lib/operator-format';
import {
  formatLoopKey,
  formatSolverRef,
  formatSolverSource,
} from '../optimization-model';
import {
  deleteSiteOptimizationCanaryBinding,
  optimizationKeys,
  siteOptimizationCanaryBindingsQuery,
  upsertSiteOptimizationCanaryBinding,
} from '../query';

type LoopKeyOption = 'PLANT' | 'CHW_PUMP' | 'CW_PUMP' | 'CT_FAN';
type SolverMode = 'builtin://hvac-recommendation-v1' | 'builtin://chiller-cop-model' | 'external';

const LOOP_OPTIONS: readonly { readonly value: LoopKeyOption; readonly label: string }[] = [
  { value: 'PLANT', label: '全冷站主回路' },
  { value: 'CHW_PUMP', label: '冷冻泵变频回路' },
  { value: 'CW_PUMP', label: '冷却泵变频回路' },
  { value: 'CT_FAN', label: '冷却塔风机回路' },
];

const SOLVER_MODES: readonly { readonly value: SolverMode; readonly label: string }[] = [
  { value: 'builtin://hvac-recommendation-v1', label: '内置稳态优化策略' },
  { value: 'builtin://chiller-cop-model', label: '冷机 COP 拟合策略' },
  { value: 'external', label: '外部 Python 优化服务' },
];

function isLoopOption(value: string): value is LoopKeyOption {
  return value === 'PLANT' || value === 'CHW_PUMP' || value === 'CW_PUMP' || value === 'CT_FAN';
}

function isSolverMode(value: string): value is SolverMode {
  return value === 'builtin://hvac-recommendation-v1'
    || value === 'builtin://chiller-cop-model'
    || value === 'external';
}

function isBindingStatus(value: string): value is OptimizationCanaryBindingStatus {
  return value === 'ACTIVE' || value === 'DISABLED';
}

export function CanaryBindingsDialog({
  siteId,
  timeZone,
  open,
  onOpenChange,
  canManage,
  csrfToken,
}: {
  readonly siteId: string;
  readonly timeZone: string;
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly canManage: boolean;
  readonly csrfToken: string;
}) {
  const queryClient = useQueryClient();
  const bindingsQuery = useQuery(siteOptimizationCanaryBindingsQuery(siteId, open));

  const [loopKey, setLoopKey] = useState<LoopKeyOption>('PLANT');
  const [solverMode, setSolverMode] = useState<SolverMode>('builtin://chiller-cop-model');
  const [externalUrl, setExternalUrl] = useState('');
  const [status, setStatus] = useState<OptimizationCanaryBindingStatus>('ACTIVE');
  const [formError, setFormError] = useState<string | null>(null);

  const saveMutation = useMutation({
    mutationFn: ({ targetLoop, servingRef, nextStatus }: {
      readonly targetLoop: string;
      readonly servingRef: string;
      readonly nextStatus: OptimizationCanaryBindingStatus;
    }) => upsertSiteOptimizationCanaryBinding(siteId, targetLoop, { servingRef, status: nextStatus }, csrfToken),
    onSuccess: (saved) => {
      setFormError(null);
      toast.success(`已更新「${formatLoopKey(saved.loopKey)}」灰度策略绑定`);
      void queryClient.invalidateQueries({ queryKey: optimizationKeys.canaryBindings(siteId) });
      void queryClient.invalidateQueries({ queryKey: optimizationKeys.governanceSummary(siteId) });
    },
    onError: (err) => {
      setFormError(err instanceof Error ? err.message : '保存灰度路由失败，请稍后重试');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (targetLoop: string) => deleteSiteOptimizationCanaryBinding(siteId, targetLoop, csrfToken),
    onSuccess: (deleted) => {
      setFormError(null);
      toast.success(`已将「${formatLoopKey(deleted.loopKey)}」切回默认策略`);
      void queryClient.invalidateQueries({ queryKey: optimizationKeys.canaryBindings(siteId) });
      void queryClient.invalidateQueries({ queryKey: optimizationKeys.governanceSummary(siteId) });
    },
    onError: (err) => {
      setFormError(err instanceof Error ? err.message : '重置灰度路由失败，请稍后重试');
    },
  });

  function populateFromBinding(item: OptimizationCanaryBindingRecord) {
    setFormError(null);
    if (isLoopOption(item.loopKey)) {
      setLoopKey(item.loopKey);
    }
    setStatus(item.status);
    if (item.servingRef === 'builtin://hvac-recommendation-v1' || item.servingRef === 'builtin://chiller-cop-model') {
      setSolverMode(item.servingRef);
      setExternalUrl('');
      return;
    }
    setSolverMode('external');
    setExternalUrl(item.servingRef);
  }

  function handleSubmit() {
    setFormError(null);
    let servingRef: string = solverMode;
    if (solverMode === 'external') {
      const trimmed = externalUrl.trim();
      if (!trimmed) {
        setFormError('请填写外部 Python 优化服务地址');
        return;
      }
      servingRef = trimmed;
    }
    saveMutation.mutate({ targetLoop: loopKey, servingRef, nextStatus: status });
  }

  function handleUrlKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (event.key === 'Enter') {
      event.preventDefault();
      handleSubmit();
    }
  }

  const data = bindingsQuery.data;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <GitBranch className="size-4 text-primary" aria-hidden="true" />
            站点控制回路灰度路由管理
          </DialogTitle>
          <DialogDescription>
            按冷站主回路或辅机变频回路动态切换优化求解策略；外部服务地址受服务端白名单严格校验，一键删除后立即切回部署绑定或内置默认策略。
          </DialogDescription>
        </DialogHeader>

        {bindingsQuery.isPending ? (
          <Skeleton className="h-48 rounded-lg" />
        ) : bindingsQuery.isError ? (
          <Alert variant="destructive">
            <AlertTitle>灰度路由配置暂时读不到</AlertTitle>
            <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
              <span>{bindingsQuery.error instanceof Error ? bindingsQuery.error.message : '读取站点灰度绑定失败'}</span>
              <Button variant="outline" size="sm" onClick={() => void bindingsQuery.refetch()}>
                重试
              </Button>
            </AlertDescription>
          </Alert>
        ) : data ? (
          <div className="flex flex-col gap-4">
            <dl className="grid grid-cols-1 gap-4 rounded-lg border border-divider bg-muted/25 px-4 py-3 sm:grid-cols-2">
              <FactTile label="主回路当前生效策略" valueClassName="text-sm font-semibold text-foreground">
                <span>{formatSolverRef(data.activeSolver.servingRef)}</span>
                <span className="mt-0.5 block text-xs font-normal text-muted-foreground">
                  {formatSolverSource(data.activeSolver.source)} · {formatLoopKey(data.activeSolver.loopKey)}
                </span>
              </FactTile>
              <FactTile label="允许的外部优化主机" valueClassName="text-sm font-semibold text-foreground">
                {data.allowedSolverHosts.length === 0 ? (
                  <span className="text-xs font-normal text-muted-foreground">服务端未配置外部主机白名单</span>
                ) : (
                  <div className="mt-0.5 flex flex-wrap gap-1.5">
                    {data.allowedSolverHosts.map((host) => (
                      <Button
                        key={host}
                        type="button"
                        variant="outline"
                        size="sm"
                        className="h-6 px-2 font-mono text-xs"
                        disabled={!canManage}
                        onClick={() => {
                          setSolverMode('external');
                          setExternalUrl(`http://${host}`);
                          setFormError(null);
                        }}
                      >
                        {host}
                      </Button>
                    ))}
                  </div>
                )}
              </FactTile>
            </dl>

            <div className="overflow-hidden rounded-lg border border-divider">
              <Table aria-label="站点灰度路由绑定列表">
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">控制回路</TableHead>
                    <TableHead>目标策略</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>更新时间</TableHead>
                    {canManage ? <TableHead className="pr-4 text-right">操作</TableHead> : null}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.items.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={canManage ? 5 : 4} className="py-6 text-center text-xs text-muted-foreground">
                        当前站点暂无自定义灰度路由绑定，全部回路按部署版本或默认基线策略运行。
                      </TableCell>
                    </TableRow>
                  ) : (
                    data.items.map((item) => (
                      <TableRow key={item.id}>
                        <TableCell className="pl-4 font-medium">{formatLoopKey(item.loopKey)}</TableCell>
                        <TableCell>{formatSolverRef(item.servingRef)}</TableCell>
                        <TableCell>
                          <Badge variant={item.status === 'ACTIVE' ? 'success' : 'secondary'}>
                            {item.status === 'ACTIVE' ? '生效中' : '已停用'}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground tabular-nums">
                          {formatTime(item.updatedAt, timeZone)}
                        </TableCell>
                        {canManage ? (
                          <TableCell className="pr-4 text-right">
                            <div className="inline-flex items-center gap-1.5">
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onClick={() => populateFromBinding(item)}
                              >
                                编辑
                              </Button>
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                disabled={deleteMutation.isPending}
                                onClick={() => deleteMutation.mutate(item.loopKey)}
                              >
                                <RotateCcw className="mr-1 size-3.5" aria-hidden="true" />
                                切回默认
                              </Button>
                            </div>
                          </TableCell>
                        ) : null}
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </div>

            {!canManage ? (
              <p className="text-xs text-muted-foreground">
                当前账号仅可查看灰度路由绑定；修改或切回默认策略需要排班与控制权限。
              </p>
            ) : (
              <div className="flex flex-col gap-3 rounded-lg border border-divider bg-muted/15 p-4">
                <p className="text-xs font-semibold text-foreground">新增或更新回路灰度绑定</p>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <Field>
                    <FieldLabel htmlFor="canary-loop-select">控制回路</FieldLabel>
                    <Select
                      value={loopKey}
                      onValueChange={(value) => {
                        if (isLoopOption(value)) setLoopKey(value);
                      }}
                    >
                      <SelectTrigger id="canary-loop-select" aria-label="控制回路">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {LOOP_OPTIONS.map((option) => (
                          <SelectItem key={option.value} value={option.value}>
                            {option.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="canary-solver-select">目标优化策略</FieldLabel>
                    <Select
                      value={solverMode}
                      onValueChange={(value) => {
                        if (isSolverMode(value)) {
                          setSolverMode(value);
                          setFormError(null);
                        }
                      }}
                    >
                      <SelectTrigger id="canary-solver-select" aria-label="目标优化策略">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {SOLVER_MODES.map((option) => (
                          <SelectItem key={option.value} value={option.value}>
                            {option.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>

                  <Field>
                    <FieldLabel htmlFor="canary-status-select">绑定状态</FieldLabel>
                    <Select
                      value={status}
                      onValueChange={(value) => {
                        if (isBindingStatus(value)) setStatus(value);
                      }}
                    >
                      <SelectTrigger id="canary-status-select" aria-label="绑定状态">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="ACTIVE">生效中</SelectItem>
                        <SelectItem value="DISABLED">已停用</SelectItem>
                      </SelectContent>
                    </Select>
                  </Field>
                </div>

                {solverMode === 'external' ? (
                  <Field>
                    <FieldLabel htmlFor="canary-external-url">外部服务地址</FieldLabel>
                    <Input
                      id="canary-external-url"
                      aria-label="外部服务地址"
                      value={externalUrl}
                      onChange={(e) => {
                        setExternalUrl(e.target.value);
                        setFormError(null);
                      }}
                      onKeyDown={handleUrlKeyDown}
                      placeholder="例如 http://ai-solver-service:8090"
                    />
                    <FieldDescription>
                      仅允许提交服务端已配置的 HTTP/HTTPS 内部白名单主机，不支持携带查询参数或账号凭证。
                    </FieldDescription>
                  </Field>
                ) : null}

                {formError ? (
                  <Alert variant="destructive">
                    <AlertDescription>{formError}</AlertDescription>
                  </Alert>
                ) : null}

                <div className="flex justify-end">
                  <Button
                    type="button"
                    disabled={saveMutation.isPending}
                    onClick={handleSubmit}
                  >
                    {saveMutation.isPending ? '保存中...' : '保存灰度绑定'}
                  </Button>
                </div>
              </div>
            )}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
