export const COPILOTKIT_RUNTIME_URL = import.meta.env.VITE_COPILOTKIT_RUNTIME_URL?.trim() ?? '';
export const COPILOTKIT_ENABLED = COPILOTKIT_RUNTIME_URL.length > 0;

export const AI_ASSISTANT_NAME = 'HVAC AI 运维助手';
