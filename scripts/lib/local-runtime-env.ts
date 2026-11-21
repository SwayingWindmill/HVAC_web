const PLACEHOLDER_SECRET = '[REDACTED_SECRET]';

export function hasConfiguredDeepSeekApiKey(
  envText: string,
  processEnv: Readonly<Record<string, string | undefined>> = process.env,
): boolean {
  const envOverride = processEnv.DEEPSEEK_API_KEY;
  if (envOverride !== undefined) {
    const trimmed = envOverride.trim();
    return trimmed.length > 0 && trimmed !== PLACEHOLDER_SECRET;
  }

  const match = /^\s*(?:export\s+)?DEEPSEEK_API_KEY\s*=\s*(.+?)\s*$/m.exec(envText);
  if (!match) return false;
  const raw = (match[1] ?? '').trim();
  const unquoted =
    (raw.startsWith('"') && raw.endsWith('"')) || (raw.startsWith("'") && raw.endsWith("'"))
      ? raw.slice(1, -1).trim()
      : raw;
  return unquoted.length > 0 && unquoted !== PLACEHOLDER_SECRET;
}
