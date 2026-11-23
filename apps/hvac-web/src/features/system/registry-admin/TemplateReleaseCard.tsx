import { useId } from 'react';
import { LayoutTemplate, Plus, Trash2 } from 'lucide-react';
import type { TemplateKind } from '@/api/generated/platformGateway.gen';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { isOneOf } from '@/lib/typed';
import type { StructuredDeviceTemplateDraft, TemplateReferenceEntry } from './template-model';
import { TemplateStructuredDeviceEditor } from './TemplateStructuredDeviceEditor';
import {
  kindLabel,
  TEMPLATE_KINDS,
  type PayloadEditorMode,
  type ReleaseDraft,
} from './template-workbench-types';

export interface TemplateReleaseCardProps {
  readonly allowed: boolean;
  readonly working: boolean;
  readonly payloadMode: PayloadEditorMode;
  readonly releaseDraft: ReleaseDraft;
  readonly structuredDraft: StructuredDeviceTemplateDraft;
  readonly referenceEntries: readonly TemplateReferenceEntry[];
  readonly onTemplateKeyChange: (value: string) => void;
  readonly onTemplateKindChange: (value: TemplateKind) => void;
  readonly onSwitchPayloadMode: (mode: PayloadEditorMode) => void;
  readonly onStructuredDraftChange: (next: StructuredDeviceTemplateDraft) => void;
  readonly onPayloadJsonChange: (value: string) => void;
  readonly onReferenceEntriesChange: (next: TemplateReferenceEntry[]) => void;
  readonly onReasonChange: (value: string) => void;
  readonly onRelease: () => void;
}

export function TemplateReleaseCard({
  allowed,
  working,
  payloadMode,
  releaseDraft,
  structuredDraft,
  referenceEntries,
  onTemplateKeyChange,
  onTemplateKindChange,
  onSwitchPayloadMode,
  onStructuredDraftChange,
  onPayloadJsonChange,
  onReferenceEntriesChange,
  onReasonChange,
  onRelease,
}: TemplateReleaseCardProps) {
  const structuredGroupId = useId();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <LayoutTemplate className="size-4" />
          发布模板版本
        </CardTitle>
        <CardDescription>
          设备模板支持结构化配置标准点位、质量检查与控制能力，也可切换为高级 JSON 编辑。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="grid gap-1.5 text-sm">
            <span>模板名称</span>
            <Input
              disabled={!allowed}
              value={releaseDraft.templateKey}
              placeholder="chiller-standard"
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              onChange={(event) => onTemplateKeyChange(event.currentTarget.value)}
            />
          </label>
          <label className="grid gap-1.5 text-sm">
            <span>适用对象</span>
            <Select
              disabled={!allowed}
              value={releaseDraft.templateKind}
              onValueChange={(value) => {
                if (isOneOf(TEMPLATE_KINDS, value)) onTemplateKindChange(value);
              }}
            >
              <SelectTrigger aria-label="适用对象">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {TEMPLATE_KINDS.map((kind) => (
                    <SelectItem key={kind} value={kind}>
                      {kindLabel(kind)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </label>
        </div>

        {releaseDraft.templateKind === 'DEVICE' ? (
          <div className="flex items-center justify-between gap-2 border-y border-divider py-2">
            <span id={structuredGroupId} className="text-xs font-medium text-muted-foreground">
              模板内容格式
            </span>
            <ToggleGroup
              type="single"
              variant="outline"
              size="sm"
              value={payloadMode}
              onValueChange={(val) => {
                if (val === 'structured' || val === 'json') onSwitchPayloadMode(val);
              }}
              aria-labelledby={structuredGroupId}
            >
              <ToggleGroupItem value="structured" className="text-xs">
                结构化配置
              </ToggleGroupItem>
              <ToggleGroupItem value="json" className="text-xs">
                高级 JSON
              </ToggleGroupItem>
            </ToggleGroup>
          </div>
        ) : null}

        {releaseDraft.templateKind === 'DEVICE' && payloadMode === 'structured' ? (
          <TemplateStructuredDeviceEditor
            draft={structuredDraft}
            allowed={allowed}
            onChange={onStructuredDraftChange}
          />
        ) : (
          <label className="grid gap-1.5 text-sm">
            <span>模板内容 JSON</span>
            <Textarea
              disabled={!allowed}
              rows={8}
              className="font-mono text-xs"
              value={releaseDraft.payloadJson}
              onChange={(event) => onPayloadJsonChange(event.currentTarget.value)}
            />
          </label>
        )}

        <div className="space-y-2 border-t border-divider pt-3">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold text-foreground">发布引用（Key-Value）</span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!allowed}
              onClick={() => onReferenceEntriesChange([...referenceEntries, { key: '', value: '' }])}
            >
              <Plus data-icon="inline-start" />
              添加引用
            </Button>
          </div>
          <div className="space-y-1.5">
            {referenceEntries.map((entry, index) => (
              <div key={index} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] gap-2">
                <Input
                  disabled={!allowed}
                  aria-label={`发布引用键 ${index + 1}`}
                  placeholder="registrySchema"
                  value={entry.key}
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  className="h-8 font-mono text-xs"
                  onChange={(event) => {
                    const val = event.currentTarget.value;
                    onReferenceEntriesChange(
                      referenceEntries.map((item, i) => (i === index ? { ...item, key: val } : item)),
                    );
                  }}
                />
                <Input
                  disabled={!allowed}
                  aria-label={`发布引用值 ${index + 1}`}
                  placeholder="v1"
                  value={entry.value}
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  className="h-8 font-mono text-xs"
                  onChange={(event) => {
                    const val = event.currentTarget.value;
                    onReferenceEntriesChange(
                      referenceEntries.map((item, i) => (i === index ? { ...item, value: val } : item)),
                    );
                  }}
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  disabled={!allowed || referenceEntries.length <= 1}
                  aria-label={`删除发布引用 ${index + 1}`}
                  onClick={() =>
                    onReferenceEntriesChange(referenceEntries.filter((_, i) => i !== index))
                  }
                >
                  <Trash2 className="size-3.5" />
                </Button>
              </div>
            ))}
          </div>
        </div>

        <label className="grid gap-1.5 text-sm">
          <span>发布原因</span>
          <Textarea
            disabled={!allowed}
            rows={2}
            value={releaseDraft.reason}
            onChange={(event) => onReasonChange(event.currentTarget.value)}
          />
        </label>
        <Button
          disabled={!allowed || working || !releaseDraft.templateKey.trim() || !releaseDraft.reason.trim()}
          onClick={onRelease}
        >
          {working ? '正在处理…' : '发布新版本'}
        </Button>
      </CardContent>
    </Card>
  );
}
