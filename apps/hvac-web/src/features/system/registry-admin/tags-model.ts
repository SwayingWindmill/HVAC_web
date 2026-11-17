import type { Space } from '@/api/generated/platformGateway.gen';

export const TAG_KEY_PATTERN = /^[a-z0-9][a-z0-9._/-]{0,63}$/;
export const MAX_TAG_COUNT = 64;
export const MAX_TAG_VALUE_LENGTH = 256;

export interface TagEntryDraft {
  readonly key: string;
  readonly value: string;
}

export function tagMapToEntries(
  tags?: Readonly<Record<string, string>>,
): TagEntryDraft[] {
  if (!tags) return [];
  return Object.entries(tags)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => ({ key, value }));
}

export function formatTagEntries(
  tags?: Readonly<Record<string, string>>,
): string[] {
  if (!tags) return [];
  return Object.entries(tags)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}:${value}`);
}

export function validateAndBuildTagMap(
  entries: readonly TagEntryDraft[],
): { readonly ok: true; readonly tags: Record<string, string> } | { readonly ok: false; readonly error: string } {
  const tags: Record<string, string> = {};
  const seenKeys = new Set<string>();
  let count = 0;

  for (let index = 0; index < entries.length; index += 1) {
    const raw = entries[index]!;
    const key = raw.key.trim();
    const value = raw.value.trim();

    if (!key && !value) continue;

    if (!key) {
      return { ok: false, error: `标签第 ${index + 1} 行的键名不能为空` };
    }
    if (!TAG_KEY_PATTERN.test(key)) {
      return {
        ok: false,
        error: `标签键「${key}」格式无效：须以小写字母或数字开头，仅含小写字母、数字、点、下划线、斜杠或连字符`,
      };
    }
    if (!value || value.length > MAX_TAG_VALUE_LENGTH) {
      return {
        ok: false,
        error: `标签「${key}」的值须为 1 至 ${MAX_TAG_VALUE_LENGTH} 个非空字符`,
      };
    }
    if (seenKeys.has(key)) {
      return { ok: false, error: `标签键「${key}」重复` };
    }
    seenKeys.add(key);

    count += 1;
    if (count > MAX_TAG_COUNT) {
      return { ok: false, error: `单个对象最多登记 ${MAX_TAG_COUNT} 个标签` };
    }
    tags[key] = value;
  }

  return { ok: true, tags };
}

export function parseTagFilterInput(
  raw: string,
): { readonly ok: true; readonly tag?: string } | { readonly ok: false; readonly error: string } {
  const trimmed = raw.trim();
  if (!trimmed) return { ok: true, tag: undefined };

  const colonIdx = trimmed.indexOf(':');
  const eqIdx = trimmed.indexOf('=');
  const sepIdx = colonIdx >= 0 ? colonIdx : eqIdx;

  if (sepIdx === -1) {
    if (!TAG_KEY_PATTERN.test(trimmed)) {
      return {
        ok: false,
        error: '标签筛选格式须为 key 或 key:value（键名仅含小写字母、数字、._/-）',
      };
    }
    return { ok: true, tag: trimmed };
  }

  const key = trimmed.slice(0, sepIdx).trim();
  const value = trimmed.slice(sepIdx + 1).trim();
  if (!TAG_KEY_PATTERN.test(key)) {
    return {
      ok: false,
      error: '标签筛选键名无效，须以小写字母或数字开头（._/-）',
    };
  }
  if (!value || value.length > MAX_TAG_VALUE_LENGTH) {
    return {
      ok: false,
      error: `标签「${key}」的筛选值须为 1 至 ${MAX_TAG_VALUE_LENGTH} 个非空字符`,
    };
  }
  return { ok: true, tag: `${key}:${value}` };
}

export function collectSpaceSubtreeIds(
  spaces: readonly Pick<Space, 'id' | 'parentSpaceId'>[],
  rootSpaceId: string,
): Set<string> {
  const ids = new Set<string>([rootSpaceId]);
  let expanded = true;
  while (expanded) {
    expanded = false;
    for (const space of spaces) {
      if (space.parentSpaceId && ids.has(space.parentSpaceId) && !ids.has(space.id)) {
        ids.add(space.id);
        expanded = true;
      }
    }
  }
  return ids;
}
