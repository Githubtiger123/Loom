import { useEffect, useRef, type ReactNode } from 'react';
import { cx, useFieldId } from '../../utils';
import { Icon } from '../Icon';
import { formatDuration, useControllable } from './internal';
import { Shimmer } from './StreamingText';

export interface ReasoningStep {
  /** Stable key; defaults to the index */
  id?: string;
  /** What the agent did or concluded, in one line */
  label: ReactNode;
  /** Optional supporting detail under the label */
  detail?: ReactNode;
  /** `done` (default) · `active` (in progress) · `pending` (planned) */
  status?: 'pending' | 'active' | 'done';
}

export interface ReasoningProps {
  /** Figma: State — `thinking` while the model reasons, `done` afterwards. */
  status?: 'thinking' | 'done';
  /** Figma: Duration — seconds spent thinking; the trigger reads "Thought for 12s". */
  duration?: number;
  /** Figma: Steps — a short list of what the agent did. */
  steps?: ReasoningStep[];
  /** Free-form reasoning text (summary or raw thoughts) under the steps. */
  children?: ReactNode;
  /** Figma: Label — overrides "Thinking…" / "Thought for 12s". */
  label?: string;
  /** Expanded (controlled) */
  open?: boolean;
  /** Initial expanded state when uncontrolled. Defaults to open while thinking. */
  defaultOpen?: boolean;
  /** Called when the person expands or collapses it */
  onOpenChange?: (open: boolean) => void;
  /** Collapse automatically when `status` turns `done` (uncontrolled only). */
  autoCollapse?: boolean;
  className?: string;
}

/**
 * Figma: Reasoning — a collapsible "Thought for 12s" disclosure with the agent's steps.
 * Separates the model's working from its answer (Primer: speaker clarity).
 */
export function Reasoning({ status = 'done', duration, steps, children, label, open, defaultOpen, onOpenChange, autoCollapse = true, className }: ReasoningProps) {
  const id = useFieldId();
  const thinking = status === 'thinking';
  const [isOpen, setOpen] = useControllable(open, defaultOpen ?? thinking, onOpenChange);
  const prev = useRef(status);
  useEffect(() => {
    if (autoCollapse && open === undefined && prev.current === 'thinking' && status === 'done') setOpen(false);
    prev.current = status;
  }, [status, autoCollapse, open, setOpen]);
  const text = label ?? (thinking ? 'Thinking…' : duration !== undefined ? `Thought for ${formatDuration(duration)}` : 'Reasoning');
  const hasBody = !!(steps?.length || children);

  return (
    <div className={cx('at-reasoning', thinking && 'is-thinking', isOpen && 'is-open', className)}>
      <button
        type="button"
        className="at-reasoning__trigger"
        aria-expanded={hasBody ? isOpen : undefined}
        aria-controls={hasBody ? `${id}-body` : undefined}
        disabled={!hasBody}
        onClick={() => setOpen(!isOpen)}
      >
        <Icon name="lightbulb" size={16} className="at-reasoning__icon" />
        {thinking ? <Shimmer>{text}</Shimmer> : <span>{text}</span>}
        {hasBody ? <Icon name="chevronDown" size={16} className="at-reasoning__chev" /> : null}
      </button>
      <span className="at-vh" role="status">{thinking ? '' : text}</span>
      {hasBody && isOpen ? (
        <div id={`${id}-body`} className="at-reasoning__body">
          {steps?.length ? (
            <ol className="at-reasoning__steps">
              {steps.map((s, i) => (
                <li key={s.id ?? i} className={cx('at-reasoning__step', `is-${s.status ?? 'done'}`)}>
                  <span className="at-reasoning__dot" aria-hidden="true" />
                  <span className="at-reasoning__step-text">
                    <span className="at-reasoning__step-label">
                      {s.status === 'active' ? <Shimmer>{s.label}</Shimmer> : s.label}
                      {s.status && s.status !== 'done' ? <span className="at-vh"> ({s.status === 'active' ? 'in progress' : 'pending'})</span> : null}
                    </span>
                    {s.detail ? <span className="at-reasoning__step-detail">{s.detail}</span> : null}
                  </span>
                </li>
              ))}
            </ol>
          ) : null}
          {children ? <div className="at-reasoning__text">{children}</div> : null}
        </div>
      ) : null}
    </div>
  );
}
