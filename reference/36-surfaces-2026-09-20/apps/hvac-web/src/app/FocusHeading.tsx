import { useEffect, useRef, type ComponentPropsWithoutRef } from 'react';

export function FocusHeading({ children, className, style, ...props }: ComponentPropsWithoutRef<'h1'>) {
  const headingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    headingRef.current?.focus({ preventScroll: true });
  }, []);

  return (
    <h1
      {...props}
      ref={headingRef}
      tabIndex={-1}
      className={['real-focus-heading', className].filter(Boolean).join(' ')}
      style={{ ...style, outline: 'none', boxShadow: 'none' }}
    >
      {children}
    </h1>
  );
}
