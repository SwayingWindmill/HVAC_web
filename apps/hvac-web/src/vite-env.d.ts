/// <reference types="vite/client" />

declare const __HVAC_WEB_BUILD_TARGET__: 'demo' | 'real';
declare const __HVAC_WEB_BUILD_ID__: string;
declare const __HVAC_WEB_GATEWAY_BASE_PATH__: string;
declare const __HVAC_WEB_REALTIME_PROTOCOL__: string;

interface ImportMetaEnv {
  readonly VITE_COPILOTKIT_RUNTIME_URL?: string;
  readonly VITE_AI_AGENT_PROFILE?: 'hvac' | 'energyagent';
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
