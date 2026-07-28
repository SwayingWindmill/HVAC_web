import { useEffect, useRef, type ComponentPropsWithoutRef } from 'react';

export function FocusHeading({ children, ...props }: ComponentPropsWithoutRef<'h1'>) {
  const headingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    headingRef.current?.focus({ preventScroll: true });
  }, []);

  return (
    <h1 {...props} ref={headingRef} tabIndex={-1}>
      {children}
    </h1>
  );
}
