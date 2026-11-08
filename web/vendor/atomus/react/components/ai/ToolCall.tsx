import { type ReactNode } from 'react';
import { cx, useFieldId } from '../../utils';
import { Icon, type IconName } from '../Icon';
import { formatDuration, useControllable } from './internal';

export type ToolCallStatus = 'pending' | 'running' | 'success' | 'error';

export interface ToolCallProps {
  /** Figma: Tool name — the function the agent called, shown in monospace (`search_docs`). */
  name: string;
  /** Figma: Title — what the call does in plain language ("Searched the docs"). Defaults to `name`. */
  title?: ReactNode;
  /** Figma: Status */
  status?: ToolCallStatus;
  /** Figma: Input — the arguments. Objects are shown as formatted JSON. */
  input?: unknown;
  /** Figma: Output — the result. Objects are shown as formatted JSON. */
  output?: unknown;
  /** Error message for `status="error"`; shown in place of the output. */
  error?: ReactNode;
  /** Figma: Duration — run time in milliseconds */
  duration?: number;
  /** Replaces the wrench icon (e.g. the integration's logo) */
  icon?: ReactNode;
  /** Expanded (controlled) */
  open?: boolean;
  /** Initial expanded state when uncontrolled */
  defaultOpen?: boolean;
  /** Called when the person expands or collapses it */
  onOpenChange?: (open: boolean) => void;
  className?: string;
}

const STATUS: Record<ToolCallStatus, { label: string; icon?: IconName }> = {
  pending: { label: 'Pending', icon: 'clock' },
  running: { label: 'Running' },
  success: { label: 'Done', icon: 'check' },
  error: { label: 'Failed', icon: 'alert' },
};

function show(value: unknown): string {
  if (value === undefined) return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/**
 * Figma: Tool call — one function call by the agent: name, status, duration, and a disclosure with
 * the input and output. Status changes are announced politely.
 */
export function ToolCall({ name, title, status = 'success', input, output, error, duration, icon, open, defaultOpen = false, onOpenChange, className }: ToolCallProps) {
  const id = useFieldId();
  const [isOpen, setOpen] = useControllable(open, defaultOpen, onOpenChange);
  const s = STATUS[status];
  const hasBody = input !== undefined || output !== undefined || !!error;
  return (
    <div className={cx('at-tool', `at-tool--${status}`, isOpen && 'is-open', className)}>
      <button
        type="button"
        className="at-tool__head"
        aria-expanded={hasBody ? isOpen : undefined}
        aria-controls={hasBody ? `${id}-body` : undefined}
        disabled={!hasBody}
        onClick={() => setOpen(!isOpen)}
      >
        <span className="at-tool__icon" aria-hidden="true">{icon ?? <Icon name="tool" size={16} />}</span>
        <span className="at-tool__title">
          <span className="at-tool__label">{title ?? name}</span>
          {title ? <code className="at-tool__name">{name}</code> : null}
        </span>
        <span className="at-tool__meta">
          {duration !== undefined && (status === 'success' || status === 'error') ? <span className="at-tool__duration">{formatDuration(duration, 'ms')}</span> : null}
          <span className={cx('at-tool__status', `at-tool__status--${status}`)} aria-live="polite">
            {status === 'running' ? <span className="at-spinner" aria-hidden="true" /> : <Icon name={s.icon!} size={12} />}
            {s.label}
          </span>
          {hasBody ? <Icon name="chevronDown" size={16} className="at-tool__chev" /> : null}
        </span>
      </button>
      {hasBody && isOpen ? (
        <div id={`${id}-body`} className="at-tool__body">
          {input !== undefined ? (
            <div className="at-tool__section">
              <span className="at-tool__section-label">Input</span>
              <pre className="at-code" tabIndex={0} aria-label={`${name} input`}><code>{show(input)}</code></pre>
            </div>
          ) : null}
          {error ? (
            <div className="at-tool__section">
              <span className="at-tool__section-label">Error</span>
              <div className="at-tool__error"><Icon name="alert" size={16} />{error}</div>
            </div>
          ) : output !== undefined ? (
            <div className="at-tool__section">
              <span className="at-tool__section-label">Output</span>
              <pre className="at-code" tabIndex={0} aria-label={`${name} output`}><code>{show(output)}</code></pre>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
