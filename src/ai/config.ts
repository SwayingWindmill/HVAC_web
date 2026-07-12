const configuredRuntimeUrl = import.meta.env.VITE_COPILOTKIT_RUNTIME_URL?.trim() ?? '';

export const COPILOTKIT_RUNTIME_CONFIGURED = configuredRuntimeUrl.length > 0;
export const COPILOTKIT_RUNTIME_URL = configuredRuntimeUrl || '/api/v1/copilotkit';
export const AI_ASSISTANT_NAME = '禾苗 AI 运维助手';
