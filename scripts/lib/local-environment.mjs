// The one local environment: its Compose project and the runtime directory holding its
// generated keys, certificates, credentials and configuration (Git-ignored).
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
export const phase1Dir = path.join(repoRoot, 'deploy', 'platform', 'phase1');
export const localProject = process.env.HVAC_LOCAL_PROJECT?.trim() || 'hvac-local';
export const runtimeDir = path.resolve(process.env.PHASE1_RUNTIME_DIR?.trim() || path.join(phase1Dir, 'runtime', 'local'));
export const runtimePath = (...segments) => path.join(runtimeDir, ...segments);
export const localContainer = (service) => `${localProject}-${service}-1`;
export const localEnvFile = path.resolve(process.env.PHASE1_ENV_FILE?.trim() || path.join(phase1Dir, 'environments', 'development.runtime.env'));

// The development tier per deployment-tiers.v1.json; it fits integration and intelligence.
process.env.PHASE1_DEPLOYMENT_TIER ??= 'single-lite';

// Developer secrets kept in the Git-ignored repository .env (for example DEEPSEEK_API_KEY)
// reach Compose interpolation without being written to any other file.
const dotenv = path.join(repoRoot, '.env');
if (existsSync(dotenv)) {
  for (const line of readFileSync(dotenv, 'utf8').replace(/^﻿/, '').split(/\r?\n/)) {
    const match = /^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$/.exec(line);
    if (match && process.env[match[1]] === undefined) process.env[match[1]] = match[2].replace(/^(['"])(.*)\1$/, '$2');
  }
}
