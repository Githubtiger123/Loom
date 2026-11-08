import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { cx } from '../utils';
import { Icon, type IconName } from './Icon';

export type ToastColor = 'brand' | 'gray' | 'error' | 'warning' | 'success';

export interface ToastProps {
  /** Figma: Title */
  title: ReactNode;
  /** Figma: Description */
  description?: ReactNode;
  /** Figma: Color */
  color?: ToastColor;
  /** Figma: Close */
  onClose?: () => void;
  action?: ReactNode;
  className?: string;
}

const ICONS: Record<ToastColor, IconName> = { brand: 'info', gray: 'info', error: 'alert', warning: 'alert', success: 'success' };

/** Figma: Toast — a single floating notification. Use ToastProvider + useToast to show them. */
export function Toast({ title, description, color = 'gray', onClose, action, className }: ToastProps) {
  return (
    <div className={cx('at-toast', `at-toast--${color}`, className)} role={color === 'error' ? 'alert' : 'status'}>
      <span className="at-toast__icon"><Icon name={ICONS[color]} size={20} /></span>
      <div className="at-toast__body">
        <p className="at-toast__title">{title}</p>
        {description ? <p className="at-toast__desc">{description}</p> : null}
        {action ? <div className="at-toast__action">{action}</div> : null}
      </div>
      {onClose ? (
        <button type="button" className="at-toast__close" aria-label="Dismiss" onClick={onClose}><Icon name="x" size={16} /></button>
      ) : null}
    </div>
  );
}

export interface ToastOptions extends Omit<ToastProps, 'onClose' | 'className'> {
  /** ms before auto-dismiss; 0 keeps it until closed. Default 5000 (errors 0). */
  duration?: number;
}

interface ToastCtx { show: (t: ToastOptions) => string; dismiss: (id: string) => void }
const Ctx = createContext<ToastCtx | null>(null);

function TimedToast({ id, t, dismiss }: { id: string; t: ToastOptions; dismiss: (id: string) => void }) {
  const ms = t.duration ?? (t.color === 'error' ? 0 : 5000);
  const timer = useRef<number | undefined>(undefined);
  const start = useCallback(() => { if (ms > 0) timer.current = window.setTimeout(() => dismiss(id), ms); }, [ms, id, dismiss]);
  const stop = () => window.clearTimeout(timer.current);
  useEffect(() => { start(); return stop; }, [start]);
  return (
    <div onMouseEnter={stop} onMouseLeave={start} onFocus={stop} onBlur={start}>
      <Toast {...t} onClose={() => dismiss(id)} />
    </div>
  );
}

export interface ToastProviderProps {
  children: ReactNode;
  position?: 'top-right' | 'bottom-right' | 'bottom-center';
}

/** Hosts toasts. Pauses auto-dismiss on hover and focus. */
export function ToastProvider({ children, position = 'bottom-right' }: ToastProviderProps) {
  const [list, setList] = useState<Array<{ id: string; t: ToastOptions }>>([]);
  const n = useRef(0);
  const dismiss = useCallback((id: string) => setList((l) => l.filter((x) => x.id !== id)), []);
  const show = useCallback((t: ToastOptions) => {
    const id = `t${++n.current}`;
    setList((l) => [...l, { id, t }].slice(-4));
    return id;
  }, []);
  const value = useMemo(() => ({ show, dismiss }), [show, dismiss]);
  return (
    <Ctx.Provider value={value}>
      {children}
      <div className={cx('at-toaster', `at-toaster--${position}`)} aria-live="polite">
        {list.map(({ id, t }) => <TimedToast key={id} id={id} t={t} dismiss={dismiss} />)}
      </div>
    </Ctx.Provider>
  );
}

export function useToast(): ToastCtx {
  const c = useContext(Ctx);
  if (!c) throw new Error('useToast must be used inside <ToastProvider>');
  return c;
}
