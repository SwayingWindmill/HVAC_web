import * as React from 'react';
import * as ReactDOM from 'react-dom';
import { Direction as DirectionPrimitive, Slot as SlotPrimitive } from 'radix-ui';

import { Button } from '@/components/ui/button';
import { useAsRef } from '@/hooks/use-as-ref';
import { useIsomorphicLayoutEffect } from '@/hooks/use-isomorphic-layout-effect';
import { useComposedRefs } from '@/lib/compose-refs';
import { cn } from '@/lib/utils';

type Direction = 'ltr' | 'rtl';
type Orientation = 'horizontal' | 'vertical';

interface DivProps extends React.ComponentProps<'div'> {
  asChild?: boolean;
}

interface ActionBarContextValue {
  onOpenChange?: (open: boolean) => void;
  dir: Direction;
  orientation: Orientation;
  loop: boolean;
}

interface ItemData {
  id: string;
  ref: React.RefObject<HTMLButtonElement | null>;
  disabled: boolean;
}

interface FocusContextValue {
  tabStopId: string | null;
  onItemFocus: (id: string) => void;
  onItemShiftTab: () => void;
  onFocusableItemAdd: () => void;
  onFocusableItemRemove: () => void;
  onItemRegister: (item: ItemData) => void;
  onItemUnregister: (id: string) => void;
  getItems: () => ItemData[];
}

const ActionBarContext = React.createContext<ActionBarContextValue | null>(null);
const FocusContext = React.createContext<FocusContextValue | null>(null);

function useActionBarContext(name: string) {
  const context = React.useContext(ActionBarContext);
  if (!context) throw new Error(`${name} must be used within ActionBar`);
  return context;
}

function useFocusContext(name: string) {
  const context = React.useContext(FocusContext);
  if (!context) throw new Error(`${name} must be used within ActionBarGroup`);
  return context;
}

function focusFirst(candidates: React.RefObject<HTMLElement | null>[]) {
  const previous = document.activeElement;
  for (const ref of candidates) {
    const candidate = ref.current;
    if (!candidate) continue;
    if (candidate === previous) return;
    candidate.focus();
    if (document.activeElement !== previous) return;
  }
}

function wrapArray<T>(items: T[], startIndex: number): T[] {
  return items.map((_, index) => items[(startIndex + index) % items.length] as T);
}

function directionAwareKey(key: string, dir: Direction) {
  if (dir !== 'rtl') return key;
  if (key === 'ArrowLeft') return 'ArrowRight';
  if (key === 'ArrowRight') return 'ArrowLeft';
  return key;
}

export interface ActionBarProps extends DivProps {
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onEscapeKeyDown?: (event: KeyboardEvent) => void;
  align?: 'start' | 'center' | 'end';
  alignOffset?: number;
  side?: 'top' | 'bottom';
  sideOffset?: number;
  portalContainer?: Element | DocumentFragment | null;
  dir?: Direction;
  orientation?: Orientation;
  loop?: boolean;
}

function ActionBar({
  open = false,
  onOpenChange,
  onEscapeKeyDown,
  align = 'center',
  alignOffset = 0,
  side = 'bottom',
  sideOffset = 16,
  portalContainer: portalContainerProp,
  dir: dirProp,
  orientation = 'horizontal',
  loop = true,
  className,
  style,
  ref,
  asChild,
  ...props
}: ActionBarProps) {
  const [mounted, setMounted] = React.useState(false);
  const rootRef = React.useRef<HTMLDivElement>(null);
  const composedRef = useComposedRefs(ref, rootRef);
  const callbacksRef = useAsRef({ onEscapeKeyDown, onOpenChange });
  const dir = DirectionPrimitive.useDirection(dirProp);

  React.useLayoutEffect(() => setMounted(true), []);

  React.useEffect(() => {
    if (!open) return;
    const ownerDocument = rootRef.current?.ownerDocument ?? document;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      callbacksRef.current.onEscapeKeyDown?.(event);
      if (!event.defaultPrevented) callbacksRef.current.onOpenChange?.(false);
    };
    ownerDocument.addEventListener('keydown', onKeyDown);
    return () => ownerDocument.removeEventListener('keydown', onKeyDown);
  }, [callbacksRef, open]);

  const portalContainer = portalContainerProp ?? (mounted ? globalThis.document?.body : null);
  if (!portalContainer || !open) return null;

  const RootPrimitive = asChild ? SlotPrimitive.Slot : 'div';
  return (
    <ActionBarContext.Provider value={{ onOpenChange, dir, orientation, loop }}>
      {ReactDOM.createPortal(
        <RootPrimitive
          role="toolbar"
          aria-orientation={orientation}
          data-slot="action-bar"
          data-side={side}
          data-align={align}
          data-orientation={orientation}
          dir={dir}
          ref={composedRef}
          className={cn(
            'fixed z-50 rounded-lg border bg-card text-card-foreground shadow-lg outline-none',
            'animate-in fade-in-0 zoom-in-95 duration-200',
            'data-[side=bottom]:slide-in-from-bottom-4 data-[side=top]:slide-in-from-top-4',
            'motion-reduce:animate-none motion-reduce:transition-none',
            orientation === 'horizontal'
              ? 'flex flex-row items-center gap-2 px-2 py-1.5'
              : 'flex flex-col items-start gap-2 px-1.5 py-2',
            className,
          )}
          style={{
            [side]: `${sideOffset}px`,
            ...(align === 'center' ? { left: '50%', translate: '-50% 0' } : {}),
            ...(align === 'start' ? { left: `${alignOffset}px` } : {}),
            ...(align === 'end' ? { right: `${alignOffset}px` } : {}),
            ...style,
          }}
          {...props}
        />,
        portalContainer,
      )}
    </ActionBarContext.Provider>
  );
}

function ActionBarSelection({ className, asChild, ...props }: DivProps) {
  const Primitive = asChild ? SlotPrimitive.Slot : 'div';
  return (
    <Primitive
      data-slot="action-bar-selection"
      className={cn('flex items-center gap-1 rounded-sm border px-2 py-1 text-sm font-medium tabular-nums', className)}
      {...props}
    />
  );
}

function ActionBarGroup({
  onBlur: onBlurProp,
  onFocus: onFocusProp,
  onMouseDown: onMouseDownProp,
  className,
  asChild,
  ref,
  ...props
}: DivProps) {
  const [tabStopId, setTabStopId] = React.useState<string | null>(null);
  const [tabbingBackOut, setTabbingBackOut] = React.useState(false);
  const [focusableCount, setFocusableCount] = React.useState(0);
  const groupRef = React.useRef<HTMLDivElement>(null);
  const composedRef = useComposedRefs(ref, groupRef);
  const clickFocusRef = React.useRef(false);
  const itemsRef = React.useRef<Map<string, ItemData>>(new Map());
  const { dir, orientation } = useActionBarContext('ActionBarGroup');

  const getItems = React.useCallback(() => (
    Array.from(itemsRef.current.values())
      .filter((item) => item.ref.current)
      .sort((left, right) => {
        const a = left.ref.current;
        const b = right.ref.current;
        if (!a || !b) return 0;
        const position = a.compareDocumentPosition(b);
        if (position & Node.DOCUMENT_POSITION_FOLLOWING) return -1;
        if (position & Node.DOCUMENT_POSITION_PRECEDING) return 1;
        return 0;
      })
  ), []);

  const focusContext = React.useMemo<FocusContextValue>(() => ({
    tabStopId,
    onItemFocus: setTabStopId,
    onItemShiftTab: () => setTabbingBackOut(true),
    onFocusableItemAdd: () => setFocusableCount((count) => count + 1),
    onFocusableItemRemove: () => setFocusableCount((count) => count - 1),
    onItemRegister: (item) => itemsRef.current.set(item.id, item),
    onItemUnregister: (id) => itemsRef.current.delete(id),
    getItems,
  }), [getItems, tabStopId]);

  const Primitive = asChild ? SlotPrimitive.Slot : 'div';

  return (
    <FocusContext.Provider value={focusContext}>
      <Primitive
        role="group"
        data-slot="action-bar-group"
        data-orientation={orientation}
        dir={dir}
        tabIndex={tabbingBackOut || focusableCount === 0 ? -1 : 0}
        ref={composedRef}
        className={cn(
          'flex gap-2 outline-none',
          orientation === 'horizontal' ? 'items-center' : 'w-full flex-col items-start',
          className,
        )}
        onBlur={(event) => {
          onBlurProp?.(event);
          if (!event.defaultPrevented) setTabbingBackOut(false);
        }}
        onFocus={(event) => {
          onFocusProp?.(event);
          if (event.defaultPrevented) return;
          if (event.target === event.currentTarget && !clickFocusRef.current && !tabbingBackOut) {
            const items = getItems().filter((item) => !item.disabled);
            const current = items.find((item) => item.id === tabStopId);
            const candidates = current ? [current, ...items] : items;
            focusFirst(candidates.map((item) => item.ref));
          }
          clickFocusRef.current = false;
        }}
        onMouseDown={(event) => {
          onMouseDownProp?.(event);
          if (!event.defaultPrevented) clickFocusRef.current = true;
        }}
        {...props}
      />
    </FocusContext.Provider>
  );
}

interface ActionBarItemProps extends Omit<React.ComponentProps<typeof Button>, 'onSelect'> {
  onSelect?: (event: Event) => void;
}

function ActionBarItem({
  onSelect,
  onClick: onClickProp,
  onFocus: onFocusProp,
  onKeyDown: onKeyDownProp,
  onMouseDown: onMouseDownProp,
  disabled,
  className,
  ref,
  ...props
}: ActionBarItemProps) {
  const itemRef = React.useRef<HTMLButtonElement>(null);
  const composedRef = useComposedRefs(ref, itemRef);
  const { onOpenChange, dir, orientation, loop } = useActionBarContext('ActionBarItem');
  const focusContext = useFocusContext('ActionBarItem');
  const itemId = React.useId();
  const isTabStop = focusContext.tabStopId === itemId;

  useIsomorphicLayoutEffect(() => {
    focusContext.onItemRegister({ id: itemId, ref: itemRef, disabled: Boolean(disabled) });
    if (!disabled) focusContext.onFocusableItemAdd();
    return () => {
      focusContext.onItemUnregister(itemId);
      if (!disabled) focusContext.onFocusableItemRemove();
    };
  }, [disabled, focusContext, itemId]);

  return (
    <Button
      type="button"
      data-slot="action-bar-item"
      variant="secondary"
      size="sm"
      disabled={disabled}
      tabIndex={isTabStop ? 0 : -1}
      ref={composedRef}
      className={cn(orientation === 'vertical' && 'w-full', className)}
      onClick={(event) => {
        onClickProp?.(event);
        if (event.defaultPrevented) return;
        const selectEvent = new CustomEvent('actionbar.itemSelect', { bubbles: true, cancelable: true });
        itemRef.current?.addEventListener('actionbar.itemSelect', (nativeEvent) => onSelect?.(nativeEvent), { once: true });
        itemRef.current?.dispatchEvent(selectEvent);
        if (!selectEvent.defaultPrevented) onOpenChange?.(false);
      }}
      onFocus={(event) => {
        onFocusProp?.(event);
        if (!event.defaultPrevented) focusContext.onItemFocus(itemId);
      }}
      onKeyDown={(event) => {
        onKeyDownProp?.(event);
        if (event.defaultPrevented) return;
        if (event.key === 'Tab' && event.shiftKey) {
          focusContext.onItemShiftTab();
          return;
        }
        if (event.target !== event.currentTarget) return;
        const key = directionAwareKey(event.key, dir);
        let intent: 'first' | 'last' | 'prev' | 'next' | undefined;
        if (orientation === 'horizontal') {
          if (key === 'ArrowLeft') intent = 'prev';
          if (key === 'ArrowRight') intent = 'next';
        } else {
          if (key === 'ArrowUp') intent = 'prev';
          if (key === 'ArrowDown') intent = 'next';
        }
        if (key === 'Home') intent = 'first';
        if (key === 'End') intent = 'last';
        if (!intent || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
        event.preventDefault();
        let refs = focusContext.getItems().filter((item) => !item.disabled).map((item) => item.ref);
        if (intent === 'last') refs = refs.reverse();
        if (intent === 'prev' || intent === 'next') {
          if (intent === 'prev') refs = refs.reverse();
          const currentIndex = refs.findIndex((itemRefCandidate) => itemRefCandidate.current === event.currentTarget);
          refs = loop ? wrapArray(refs, currentIndex + 1) : refs.slice(currentIndex + 1);
        }
        queueMicrotask(() => focusFirst(refs));
      }}
      onMouseDown={(event) => {
        onMouseDownProp?.(event);
        if (disabled) event.preventDefault();
        else if (!event.defaultPrevented) focusContext.onItemFocus(itemId);
      }}
      {...props}
    />
  );
}

function ActionBarClose({ asChild, className, onClick, ...props }: React.ComponentProps<'button'> & { asChild?: boolean }) {
  const { onOpenChange } = useActionBarContext('ActionBarClose');
  const Primitive = asChild ? SlotPrimitive.Slot : 'button';
  return (
    <Primitive
      type="button"
      data-slot="action-bar-close"
      className={cn(
        'rounded-sm opacity-70 outline-none hover:opacity-100 focus-visible:ring-2 focus-visible:ring-ring/50',
        className,
      )}
      onClick={(event) => {
        onClick?.(event);
        if (!event.defaultPrevented) onOpenChange?.(false);
      }}
      {...props}
    />
  );
}

function ActionBarSeparator({
  orientation: orientationProp,
  asChild,
  className,
  ...props
}: DivProps & { orientation?: Orientation }) {
  const context = useActionBarContext('ActionBarSeparator');
  const orientation = orientationProp ?? context.orientation;
  const Primitive = asChild ? SlotPrimitive.Slot : 'div';
  return (
    <Primitive
      role="separator"
      aria-orientation={orientation}
      aria-hidden="true"
      data-slot="action-bar-separator"
      className={cn(
        'bg-border',
        orientation === 'horizontal' ? 'h-6 w-px' : 'h-px w-full',
        className,
      )}
      {...props}
    />
  );
}

export {
  ActionBar,
  ActionBarClose,
  ActionBarGroup,
  ActionBarItem,
  ActionBarSelection,
  ActionBarSeparator,
};
