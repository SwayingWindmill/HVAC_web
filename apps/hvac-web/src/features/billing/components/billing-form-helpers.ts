import type { KeyboardEvent } from 'react';

/** Prevents Enter from submitting the enclosing form while an IME composition session is active. */
export function preventImeSubmit(event: KeyboardEvent<HTMLInputElement>): void {
  if (event.key === 'Enter' && (event.nativeEvent.isComposing || event.keyCode === 229)) {
    event.preventDefault();
  }
}

/** Combines non-empty element IDs for `aria-describedby`, omitting the attribute when empty. */
export function describedBy(
  ...ids: ReadonlyArray<string | false | null | undefined>
): string | undefined {
  const joined = ids
    .filter((id): id is string => typeof id === 'string' && id.length > 0)
    .join(' ');
  return joined.length > 0 ? joined : undefined;
}
