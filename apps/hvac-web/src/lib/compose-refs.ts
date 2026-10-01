import * as React from 'react';

type PossibleRef<T> = React.Ref<T> | undefined;

function setRef<T>(ref: PossibleRef<T>, value: T | null) {
  if (typeof ref === 'function') return ref(value);
  if (ref !== null && ref !== undefined) ref.current = value;
}

function composeRefs<T>(...refs: PossibleRef<T>[]): React.RefCallback<T> {
  return (node) => {
    let hasCleanup = false;
    const cleanups = refs.map((ref) => {
      const cleanup = setRef(ref, node);
      if (!hasCleanup && typeof cleanup === 'function') hasCleanup = true;
      return cleanup;
    });

    if (hasCleanup) {
      return () => {
        for (let index = 0; index < cleanups.length; index += 1) {
          const cleanup = cleanups[index];
          if (typeof cleanup === 'function') cleanup();
          else setRef(refs[index], null);
        }
      };
    }
  };
}

function useComposedRefs<T>(...refs: PossibleRef<T>[]): React.RefCallback<T> {
  // Intentional: ref identity should track all composed refs.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return React.useCallback(composeRefs(...refs), refs);
}

export { composeRefs, useComposedRefs };
