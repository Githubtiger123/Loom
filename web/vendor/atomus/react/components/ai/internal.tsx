// Shared helpers for the Agent kit (react/src/components/ai). Internal: not exported from the package.
import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { cx } from '../../utils';

/** Controlled / uncontrolled value in one hook. */
export function useControllable<T>(value: T | undefined, defaultValue: T, onChange?: (v: T) => void): [T, (v: T) => void] {
  const [inner, setInner] = useState<T>(defaultValue);
  const controlled = value !== undefined;
  const current = controlled ? (value as T) : inner;
  const set = useCallback((v: T) => {
    if (!controlled) setInner(v);
    onChange?.(v);
  }, [controlled, onChange]);
  return [current, set];
}

/** Closes a popover on Escape (returning focus to the trigger) and on a pointer-down outside it. */
export function useDismiss(open: boolean, close: () => void, refs: Array<RefObject<HTMLElement | null>>, trigger?: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return;
      e.stopPropagation();
      close();
      trigger?.current?.focus();
    };
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (refs.every((r) => !r.current || !r.current.contains(t))) close();
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('pointerdown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('pointerdown', onDown);
    };
  }, [open, close, refs, trigger]);
}

/** Seconds → "12s", "1m 05s"; milliseconds when `ms` is true and under one second ("420ms"). */
export function formatDuration(value: number, unit: 's' | 'ms' = 's'): string {
  const ms = unit === 'ms' ? value : value * 1000;
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 10) return `${s.toFixed(1).replace(/\.0$/, '')}s`;
  if (s < 60) return `${Math.round(s)}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${String(Math.round(s % 60)).padStart(2, '0')}s`;
}

/** Whether the user asked for reduced motion (updates live). */
export function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return;
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReduced(mq.matches);
    update();
    mq.addEventListener?.('change', update);
    return () => mq.removeEventListener?.('change', update);
  }, []);
  return reduced;
}

/** Text only screen readers get. */
export function VisuallyHidden({ children, id }: { children: ReactNode; id?: string }) {
  return <span id={id} className="at-vh">{children}</span>;
}

/** The AI mark: a sparkle filled with --gradient-ai (decorative, aria-hidden). */
export function AIMark({ size = 16, className }: { size?: number; className?: string }) {
  return <span className={cx('at-ai-mark', className)} style={{ width: size, height: size }} aria-hidden="true" />;
}

/** Copies text to the clipboard; resolves false when the API is unavailable. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/** Keeps a ref pointing at the latest value (for timers and listeners). */
export function useLatest<T>(value: T) {
  const ref = useRef(value);
  ref.current = value;
  return ref;
}
