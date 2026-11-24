import { toast, type ExternalToast } from 'sonner';

const DEFAULT_SUCCESS_DURATION = 4000;

export function notifySuccess(message: string, options?: ExternalToast) {
  return toast.success(message, { duration: DEFAULT_SUCCESS_DURATION, ...options });
}

export function notifyInfo(message: string, options?: ExternalToast) {
  return toast.info(message, { duration: DEFAULT_SUCCESS_DURATION, ...options });
}

export function notifyWarning(message: string, options?: ExternalToast) {
  return toast.warning(message, { duration: 6000, ...options });
}

/** Error toasts remain visible until dismissed or retried (DESIGN.md §4.1). */
export function notifyError(
  message: string,
  options?: ExternalToast & { readonly onRetry?: () => void },
) {
  const { onRetry, action, ...rest } = options ?? {};
  return toast.error(message, {
    duration: Infinity,
    action: action ?? (onRetry ? { label: '重试', onClick: onRetry } : undefined),
    ...rest,
  });
}
