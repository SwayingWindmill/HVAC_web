import { useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ChevronDown, Upload } from 'lucide-react';
import { toast } from 'sonner';
import type { OTAPackage } from '@/api/generated/platformGateway.gen';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Field, FieldDescription, FieldError, FieldLabel, FieldSet, FieldLegend } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { isOneOf } from '@/lib/typed';
import {
  OTA_ARCH_LABELS,
  OTA_COMPONENT_LABELS,
  OTA_MAX_PACKAGE_BYTES,
  describeOtaError,
  formatByteSize,
  parseHardwareModelsInput,
  validateHardwareModelsInput,
  validateOptionalSemver,
  validateSemver,
  validateSha256Hex,
  validateSignaturePair,
} from '../ota-model';
import { otaApi, otaKeys } from '../ota-query';

const COMPONENTS = ['EDGE_GATEWAY', 'FIRMWARE'] as const satisfies readonly OTAPackage['component'][];
const ARCHES = ['armv7l', 'arm64', 'amd64'] as const satisfies readonly OTAPackage['targetArch'][];

interface FieldErrors {
  file?: string;
  version?: string;
  hardwareModels?: string;
  minCompatibleVersion?: string;
  expectedSha256?: string;
  signaturePair?: string;
  releaseNotes?: string;
}

export function OtaPackageUploadDialog({
  open,
  csrfToken,
  onOpenChange,
  onUploaded,
}: {
  readonly open: boolean;
  readonly csrfToken: string;
  readonly onOpenChange: (open: boolean) => void;
  readonly onUploaded?: (pkg: OTAPackage) => void;
}) {
  const queryClient = useQueryClient();

  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [component, setComponent] = useState<OTAPackage['component']>('EDGE_GATEWAY');
  const [version, setVersion] = useState('');
  const [targetArch, setTargetArch] = useState<OTAPackage['targetArch']>('armv7l');
  const [hardwareModels, setHardwareModels] = useState('EG8200');
  const [releaseNotes, setReleaseNotes] = useState('');
  const [minCompatibleVersion, setMinCompatibleVersion] = useState('');
  const [expectedSha256, setExpectedSha256] = useState('');
  const [signingKeyId, setSigningKeyId] = useState('');
  const [signatureB64, setSignatureB64] = useState('');
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [errors, setErrors] = useState<FieldErrors>({});

  const resetForm = () => {
    setSelectedFile(null);
    setComponent('EDGE_GATEWAY');
    setVersion('');
    setTargetArch('armv7l');
    setHardwareModels('EG8200');
    setReleaseNotes('');
    setMinCompatibleVersion('');
    setExpectedSha256('');
    setSigningKeyId('');
    setSignatureB64('');
    setAdvancedOpen(false);
    setErrors({});
  };

  const uploadMutation = useMutation({
    mutationFn: async (formData: FormData) => otaApi.uploadPackage(formData, csrfToken),
    onSuccess: (pkg) => {
      void queryClient.invalidateQueries({ queryKey: otaKeys.packages() });
      toast.success(`升级包 v${pkg.version} 已上传并签名`);
      resetForm();
      onOpenChange(false);
      onUploaded?.(pkg);
    },
  });

  const validateFile = (file: File | null): string | undefined => {
    if (!file) return '请选择要上传的升级包文件';
    if (file.size <= 0) return '升级包文件不能为空';
    if (file.size > OTA_MAX_PACKAGE_BYTES) {
      return `文件大小 ${formatByteSize(file.size)} 超过 64 MiB 上限`;
    }
    return undefined;
  };

  const validateAll = (): FieldErrors => {
    const nextErrors: FieldErrors = {};
    const fileErr = validateFile(selectedFile);
    if (fileErr) nextErrors.file = fileErr;

    const verErr = validateSemver(version);
    if (verErr) nextErrors.version = verErr;

    const hwErr = validateHardwareModelsInput(hardwareModels);
    if (hwErr) nextErrors.hardwareModels = hwErr;

    const minErr = validateOptionalSemver(minCompatibleVersion);
    if (minErr) nextErrors.minCompatibleVersion = minErr;

    const shaErr = validateSha256Hex(expectedSha256);
    if (shaErr) nextErrors.expectedSha256 = shaErr;

    const sigErr = validateSignaturePair(signingKeyId, signatureB64);
    if (sigErr) nextErrors.signaturePair = sigErr;

    if (releaseNotes.trim().length > 2000) {
      nextErrors.releaseNotes = '版本说明不能超过 2000 个字符';
    }
    return nextErrors;
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextErrors = validateAll();
    if (Object.keys(nextErrors).length > 0) {
      setErrors(nextErrors);
      if (nextErrors.minCompatibleVersion || nextErrors.expectedSha256 || nextErrors.signaturePair) {
        setAdvancedOpen(true);
      }
      return;
    }

    const formData = new FormData();
    formData.set('file', selectedFile!);
    formData.set('component', component);
    formData.set('version', version.trim());
    formData.set('targetOs', 'linux');
    formData.set('targetArch', targetArch);

    const parsedModels = parseHardwareModelsInput(hardwareModels);
    if (parsedModels.length > 0) {
      formData.set('hardwareModels', parsedModels.join(','));
    }
    if (minCompatibleVersion.trim()) {
      formData.set('minCompatibleVersion', minCompatibleVersion.trim());
    }
    if (expectedSha256.trim()) {
      formData.set('expectedSha256', expectedSha256.trim().toLowerCase());
    }
    if (signingKeyId.trim() && signatureB64.trim()) {
      formData.set('signingKeyId', signingKeyId.trim());
      formData.set('signatureB64', signatureB64.trim());
    }
    if (releaseNotes.trim()) {
      formData.set('releaseNotes', releaseNotes.trim());
    }

    uploadMutation.mutate(formData);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (uploadMutation.isPending) return;
        if (!nextOpen) {
          resetForm();
          uploadMutation.reset();
        }
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>上传 OTA 升级包？</DialogTitle>
          <DialogDescription>
            上传 Linux 边缘守护进程或设备固件安装包（最大 64 MiB）。未提供外部签名时由平台自动进行 Ed25519 签名。
          </DialogDescription>
        </DialogHeader>

        <form noValidate className="space-y-4" onSubmit={handleSubmit}>
          <Field data-invalid={Boolean(errors.file)}>
            <FieldLabel htmlFor="ota-pkg-file">安装包文件（最大 64 MiB）</FieldLabel>
            <Input
              id="ota-pkg-file"
              type="file"
              accept=".tar.gz,.tgz,.bin,.img,.deb,.zip,application/octet-stream,application/gzip"
              disabled={uploadMutation.isPending}
              aria-invalid={Boolean(errors.file)}
              aria-describedby={errors.file ? 'ota-pkg-file-error' : 'ota-pkg-file-hint'}
              onChange={(event) => {
                const file = event.target.files?.[0] ?? null;
                setSelectedFile(file);
                setErrors((prev) => ({ ...prev, file: validateFile(file) }));
              }}
            />
            {selectedFile ? (
              <div
                id="ota-pkg-file-hint"
                className="flex min-w-0 items-center justify-between gap-2 rounded-md border border-divider bg-muted/40 px-2.5 py-1.5 text-xs"
              >
                <span className="min-w-0 truncate font-mono" title={selectedFile.name}>
                  {selectedFile.name}
                </span>
                <span className="shrink-0 font-mono text-muted-foreground tabular-nums">
                  {formatByteSize(selectedFile.size)}
                </span>
              </div>
            ) : (
              <FieldDescription id="ota-pkg-file-hint">
                支持 .tar.gz、.bin 或签名镜像包，上传后立即计算 SHA-256 摘要。
              </FieldDescription>
            )}
            <FieldError id="ota-pkg-file-error">{errors.file}</FieldError>
          </Field>

          <FieldSet className="gap-3 rounded-lg border border-divider p-3.5">
            <FieldLegend variant="label" className="px-1">
              版本与目标平台
            </FieldLegend>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="ota-pkg-component">程序组件</FieldLabel>
                <Select
                  value={component}
                  disabled={uploadMutation.isPending}
                  onValueChange={(val) => {
                    if (isOneOf(COMPONENTS, val)) setComponent(val);
                  }}
                >
                  <SelectTrigger id="ota-pkg-component" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {COMPONENTS.map((item) => (
                      <SelectItem key={item} value={item}>
                        {OTA_COMPONENT_LABELS[item]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              <Field>
                <FieldLabel htmlFor="ota-pkg-arch">目标芯片架构</FieldLabel>
                <Select
                  value={targetArch}
                  disabled={uploadMutation.isPending}
                  onValueChange={(val) => {
                    if (isOneOf(ARCHES, val)) setTargetArch(val);
                  }}
                >
                  <SelectTrigger id="ota-pkg-arch" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ARCHES.map((arch) => (
                      <SelectItem key={arch} value={arch}>
                        {OTA_ARCH_LABELS[arch]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              <Field data-invalid={Boolean(errors.version)}>
                <FieldLabel htmlFor="ota-pkg-version">版本号（SemVer）</FieldLabel>
                <Input
                  id="ota-pkg-version"
                  value={version}
                  maxLength={64}
                  placeholder="例如：1.5.0"
                  disabled={uploadMutation.isPending}
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-invalid={Boolean(errors.version)}
                  aria-describedby={errors.version ? 'ota-pkg-version-error' : undefined}
                  onChange={(event) => {
                    setVersion(event.target.value);
                    if (errors.version) {
                      setErrors((prev) => ({ ...prev, version: undefined }));
                    }
                  }}
                  onBlur={() => {
                    const err = validateSemver(version);
                    setErrors((prev) => ({ ...prev, version: err ?? undefined }));
                  }}
                />
                <FieldError id="ota-pkg-version-error">{errors.version}</FieldError>
              </Field>

              <Field data-invalid={Boolean(errors.hardwareModels)}>
                <FieldLabel htmlFor="ota-pkg-hardware-models">适配硬件型号</FieldLabel>
                <Input
                  id="ota-pkg-hardware-models"
                  value={hardwareModels}
                  placeholder="例如：EG8200"
                  disabled={uploadMutation.isPending}
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-invalid={Boolean(errors.hardwareModels)}
                  aria-describedby={errors.hardwareModels ? 'ota-pkg-hw-error' : 'ota-pkg-hw-hint'}
                  onChange={(event) => {
                    setHardwareModels(event.target.value);
                    if (errors.hardwareModels) {
                      setErrors((prev) => ({ ...prev, hardwareModels: undefined }));
                    }
                  }}
                  onBlur={() => {
                    const err = validateHardwareModelsInput(hardwareModels);
                    setErrors((prev) => ({ ...prev, hardwareModels: err ?? undefined }));
                  }}
                />
                <FieldDescription id="ota-pkg-hw-hint">多个型号可用逗号分隔，留空表示不限制型号。</FieldDescription>
                <FieldError id="ota-pkg-hw-error">{errors.hardwareModels}</FieldError>
              </Field>
            </div>
          </FieldSet>

          <Field data-invalid={Boolean(errors.releaseNotes)}>
            <FieldLabel htmlFor="ota-pkg-notes">更新说明（可选）</FieldLabel>
            <Textarea
              id="ota-pkg-notes"
              value={releaseNotes}
              maxLength={2000}
              rows={3}
              placeholder="例如：修复 Modbus RTU 断线重连延迟并增加健康检查超时保护"
              disabled={uploadMutation.isPending}
              aria-invalid={Boolean(errors.releaseNotes)}
              aria-describedby={errors.releaseNotes ? 'ota-pkg-notes-error' : undefined}
              onChange={(event) => {
                setReleaseNotes(event.target.value);
                if (errors.releaseNotes) {
                  setErrors((prev) => ({ ...prev, releaseNotes: undefined }));
                }
              }}
            />
            <FieldError id="ota-pkg-notes-error">{errors.releaseNotes}</FieldError>
          </Field>

          <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen}>
            <CollapsibleTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="w-full justify-between"
                aria-expanded={advancedOpen}
              >
                <span>高级校验与外部 Ed25519 签名（可选）</span>
                <ChevronDown
                  className={advancedOpen ? 'size-4 rotate-180 transition-transform' : 'size-4 transition-transform'}
                  aria-hidden="true"
                />
              </Button>
            </CollapsibleTrigger>
            <CollapsibleContent className="mt-3 space-y-3 rounded-lg border border-divider bg-muted/30 p-3.5">
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Field data-invalid={Boolean(errors.minCompatibleVersion)}>
                  <FieldLabel htmlFor="ota-pkg-min-compat">最低兼容版本</FieldLabel>
                  <Input
                    id="ota-pkg-min-compat"
                    value={minCompatibleVersion}
                    maxLength={64}
                    placeholder="例如：1.2.0"
                    disabled={uploadMutation.isPending}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    aria-invalid={Boolean(errors.minCompatibleVersion)}
                    aria-describedby={errors.minCompatibleVersion ? 'ota-pkg-min-compat-error' : undefined}
                    onChange={(event) => {
                      setMinCompatibleVersion(event.target.value);
                      if (errors.minCompatibleVersion) {
                        setErrors((prev) => ({ ...prev, minCompatibleVersion: undefined }));
                      }
                    }}
                    onBlur={() => {
                      const err = validateOptionalSemver(minCompatibleVersion);
                      setErrors((prev) => ({ ...prev, minCompatibleVersion: err ?? undefined }));
                    }}
                  />
                  <FieldError id="ota-pkg-min-compat-error">{errors.minCompatibleVersion}</FieldError>
                </Field>

                <Field data-invalid={Boolean(errors.expectedSha256)}>
                  <FieldLabel htmlFor="ota-pkg-expected-sha">预期 SHA-256（64 位十六进制）</FieldLabel>
                  <Input
                    id="ota-pkg-expected-sha"
                    value={expectedSha256}
                    maxLength={64}
                    placeholder="留空则仅以服务端计算值为准"
                    disabled={uploadMutation.isPending}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    className="font-mono text-xs"
                    aria-invalid={Boolean(errors.expectedSha256)}
                    aria-describedby={errors.expectedSha256 ? 'ota-pkg-sha-error' : undefined}
                    onChange={(event) => {
                      setExpectedSha256(event.target.value);
                      if (errors.expectedSha256) {
                        setErrors((prev) => ({ ...prev, expectedSha256: undefined }));
                      }
                    }}
                    onBlur={() => {
                      const err = validateSha256Hex(expectedSha256);
                      setErrors((prev) => ({ ...prev, expectedSha256: err ?? undefined }));
                    }}
                  />
                  <FieldError id="ota-pkg-sha-error">{errors.expectedSha256}</FieldError>
                </Field>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Field data-invalid={Boolean(errors.signaturePair)}>
                  <FieldLabel htmlFor="ota-pkg-signing-key">外部签名密钥 ID</FieldLabel>
                  <Input
                    id="ota-pkg-signing-key"
                    value={signingKeyId}
                    maxLength={128}
                    placeholder="例如：release-key-2026"
                    disabled={uploadMutation.isPending}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    aria-invalid={Boolean(errors.signaturePair)}
                    onChange={(event) => {
                      setSigningKeyId(event.target.value);
                      if (errors.signaturePair) {
                        setErrors((prev) => ({ ...prev, signaturePair: undefined }));
                      }
                    }}
                  />
                </Field>

                <Field data-invalid={Boolean(errors.signaturePair)}>
                  <FieldLabel htmlFor="ota-pkg-signature-b64">Ed25519 Base64 签名</FieldLabel>
                  <Input
                    id="ota-pkg-signature-b64"
                    value={signatureB64}
                    maxLength={512}
                    placeholder="标准 Base64 签名串"
                    disabled={uploadMutation.isPending}
                    autoCorrect="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    className="font-mono text-xs"
                    aria-invalid={Boolean(errors.signaturePair)}
                    aria-describedby={errors.signaturePair ? 'ota-pkg-sig-error' : undefined}
                    onChange={(event) => {
                      setSignatureB64(event.target.value);
                      if (errors.signaturePair) {
                        setErrors((prev) => ({ ...prev, signaturePair: undefined }));
                      }
                    }}
                  />
                </Field>
              </div>
              <FieldError id="ota-pkg-sig-error">{errors.signaturePair}</FieldError>
            </CollapsibleContent>
          </Collapsible>

          {uploadMutation.isError ? (
            <Alert variant="destructive">
              <AlertDescription>
                {describeOtaError(uploadMutation.error, '升级包上传未完成，请核对文件与参数后重试。')}
              </AlertDescription>
            </Alert>
          ) : null}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={uploadMutation.isPending}
              onClick={() => {
                resetForm();
                uploadMutation.reset();
                onOpenChange(false);
              }}
            >
              取消
            </Button>
            <Button type="submit" disabled={uploadMutation.isPending}>
              <Upload data-icon="inline-start" />
              {uploadMutation.isPending ? '正在上传…' : '上传升级包'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
