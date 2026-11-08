import { useState, type HTMLAttributes, type ReactNode } from 'react';
import { cx, useFieldId } from '../../utils';
import { Button } from '../Button';
import { Icon } from '../Icon';
import { AILabel } from './AILabel';
import { AIMark, copyText } from './internal';

export type MessageRole = 'user' | 'assistant' | 'system' | 'tool';
export type MessageStatus = 'streaming' | 'done' | 'error' | 'stopped';

export interface MessageBranch {
  /** 1-based number of the version shown */
  index: number;
  /** How many versions exist */
  count: number;
  /** Shows the previous version */
  onPrevious?: () => void;
  /** Shows the next version */
  onNext?: () => void;
}

export interface MessageProps extends Omit<HTMLAttributes<HTMLElement>, 'role'> {
  /** Figma: Role — who is speaking. Sets layout, label and default name. */
  role?: MessageRole;
  /** Figma: Status — `streaming` keeps the message busy; `error` and `stopped` show an inline notice. */
  status?: MessageStatus;
  /** Figma: Name — the speaker ("You", "Atomus AI", "Research agent"). Always announced to screen readers. */
  name?: string;
  /** Figma: Avatar — an Avatar or logo; assistants default to the AI mark. */
  avatar?: ReactNode;
  /** Figma: Show name — show the speaker name visually (screen readers always get it). Defaults to true except for user messages. */
  showName?: boolean;
  /** Figma: AI label — shows the AI label next to an assistant's name. */
  aiLabel?: boolean;
  /** Figma: Timestamp — display text ("2:41 PM") */
  time?: string;
  /** Machine-readable time (ISO 8601) for the `<time>` element */
  dateTime?: string;
  /** Figma: Content */
  children?: ReactNode;
  /** Plain text of the message for the Copy action; Copy is hidden without it. */
  copyText?: string;
  /** Figma: Regenerate — assistant messages; also the retry action for `status="error"`. */
  onRegenerate?: () => void;
  /** Figma: Edit — user messages */
  onEdit?: () => void;
  /** Figma: Branch — ‹ 1/3 › switcher between regenerated or edited versions */
  branch?: MessageBranch;
  /** Extra actions after the built-in ones (e.g. Feedback) */
  actions?: ReactNode;
  /** Figma: Actions visibility — `always`, or on `hover` and keyboard focus (always shown on touch screens) */
  actionsVisibility?: 'always' | 'hover';
  /** Error text for `status="error"` */
  errorMessage?: ReactNode;
}

const DEFAULT_NAME: Record<MessageRole, string> = { user: 'You', assistant: 'Assistant', system: 'System', tool: 'Tool' };

/**
 * Figma: Message — one turn in a conversation. Every message is an `<article>` named by its speaker,
 * so screen-reader users always know who said what (Primer: speaker clarity).
 */
export function Message({ role = 'assistant', status = 'done', name, avatar, showName, aiLabel = true, time, dateTime, children, copyText: text, onRegenerate, onEdit, branch, actions, actionsVisibility = 'always', errorMessage = 'Something went wrong while generating this response.', className, ...rest }: MessageProps) {
  const id = useFieldId();
  const [copied, setCopied] = useState(false);
  const speaker = name ?? DEFAULT_NAME[role];
  const nameVisible = showName ?? role !== 'user';
  const streaming = status === 'streaming';

  if (role === 'system') {
    return (
      <article className={cx('at-msg', 'at-msg--system', className)} aria-labelledby={`${id}-who`} {...rest}>
        <span id={`${id}-who`} className="at-vh">{speaker}</span>
        <div className="at-msg__system"><span className="at-msg__system-text">{children}</span></div>
      </article>
    );
  }

  const copy = async () => {
    if (!text) return;
    if (await copyText(text)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    }
  };
  const hasActions = !streaming && (text || onRegenerate || onEdit || branch || actions);

  return (
    <article
      className={cx('at-msg', `at-msg--${role}`, `is-${status}`, actionsVisibility === 'hover' && 'at-msg--hover-actions', className)}
      aria-labelledby={`${id}-who`}
      aria-busy={streaming || undefined}
      {...rest}
    >
      {role !== 'user' ? (
        <div className="at-msg__avatar" aria-hidden="true">
          {avatar ?? (role === 'assistant' ? <span className="at-msg__ai-avatar"><AIMark size={18} /></span> : <span className="at-msg__tool-avatar"><Icon name="tool" size={16} /></span>)}
        </div>
      ) : null}
      <div className="at-msg__main">
        <div className={cx('at-msg__header', !nameVisible && !time && 'at-vh')}>
          <span id={`${id}-who`} className={cx('at-msg__name', !nameVisible && 'at-vh')}>{speaker}</span>
          {role === 'assistant' && aiLabel && nameVisible ? <AILabel size="xs" /> : null}
          {time ? <time className="at-msg__time" dateTime={dateTime}>{time}</time> : null}
        </div>
        <div className="at-msg__content">{children}</div>
        {status === 'error' ? (
          <div className="at-msg__notice at-msg__notice--error" role="alert">
            <Icon name="alert" size={16} />
            <span>{errorMessage}</span>
            {onRegenerate ? <Button size="xs" hierarchy="outline" iconLeading={<Icon name="refresh" size={14} />} onClick={onRegenerate}>Retry</Button> : null}
          </div>
        ) : null}
        {status === 'stopped' ? (
          <div className="at-msg__notice">
            <Icon name="stop" size={14} />
            <span>You stopped this response.</span>
          </div>
        ) : null}
        {hasActions ? (
          <div className="at-msg__actions" role="group" aria-label={`Actions for ${speaker}'s message`}>
            {branch && branch.count > 1 ? (
              <div className="at-msg__branch">
                <Button size="xs" hierarchy="tertiary" iconOnly aria-label="Previous version" disabled={branch.index <= 1} iconLeading={<Icon name="chevronLeft" size={14} />} onClick={branch.onPrevious} />
                <span className="at-msg__branch-count" aria-live="polite">
                  <span className="at-vh">Version </span>{branch.index}<span aria-hidden="true">/</span><span className="at-vh"> of </span>{branch.count}
                </span>
                <Button size="xs" hierarchy="tertiary" iconOnly aria-label="Next version" disabled={branch.index >= branch.count} iconLeading={<Icon name="chevronRight" size={14} />} onClick={branch.onNext} />
              </div>
            ) : null}
            {text ? <Button size="xs" hierarchy="tertiary" iconOnly aria-label={copied ? 'Copied' : 'Copy'} title={copied ? 'Copied' : 'Copy'} iconLeading={<Icon name={copied ? 'check' : 'copy'} size={16} />} onClick={copy} /> : null}
            {onEdit ? <Button size="xs" hierarchy="tertiary" iconOnly aria-label="Edit message" title="Edit" iconLeading={<Icon name="edit" size={16} />} onClick={onEdit} /> : null}
            {onRegenerate && status !== 'error' ? <Button size="xs" hierarchy="tertiary" iconOnly aria-label="Regenerate response" title="Regenerate" iconLeading={<Icon name="refresh" size={16} />} onClick={onRegenerate} /> : null}
            {actions}
            <span className="at-vh" role="status">{copied ? 'Copied to clipboard' : ''}</span>
          </div>
        ) : null}
      </div>
    </article>
  );
}
