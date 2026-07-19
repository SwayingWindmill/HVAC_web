/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_COPILOTKIT_RUNTIME_URL?: string;
  readonly VITE_AI_AGENT_PROFILE?: 'hvac' | 'energyagent';
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
