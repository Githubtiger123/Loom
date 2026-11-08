import { useState, type ReactNode } from 'react';
import { cx, useFieldId } from '../../utils';
import { Button } from '../Button';
import { Checkbox } from '../Choice';
import { Icon, type IconName } from '../Icon';

export type ApprovalRisk = 'low' | 'medium' | 'high';

export interface ApprovalDecision {
  /** The "always allow" checkbox was ticked */
  alwaysAllow: boolean;
  /** The edited text, when the person used Edit before approving */
  editedText?: string;
}

export interface ApprovalProps {
  /** Figma: Title — the action as a question: "Send this email to 12 people?" */
  title: ReactNode;
  /** Figma: Summary — what will happen and what it affects, in one or two sentences. */
  children?: ReactNode;
  /** Figma: Risk — low · medium · high; shown with an icon and a word, never colour alone. */
  risk?: ApprovalRisk;
  /** Figma: Tool name — the function that will run (`send_email`). */
  toolName?: string;
  /** Figma: Details — a preview of what will be sent or changed (read-only). */
  details?: ReactNode;
  /** Figma: Editable — plain text the person may edit before approving ("edit then approve"). */
  editableText?: string;
  /** Label of the editable field */
  editLabel?: string;
  /** Figma: Always allow — shows the checkbox with this label ("Always allow send_email in this chat"). */
  alwaysAllowLabel?: string;
  /** Figma: State — `pending` asks; `approved` and `denied` show the outcome in place of the buttons. */
  status?: 'pending' | 'approved' | 'denied';
  /** Figma: Approve label — name the action for high risk ("Delete variables") */
  approveLabel?: string;
  /** Figma: Deny label */
  denyLabel?: string;
  /** Called with the decision when the person approves (after any edits) */
  onApprove?: (decision: ApprovalDecision) => void;
  /** Called when the person denies; keep the card and set `status="denied"` */
  onDeny?: () => void;
  className?: string;
}

const RISK: Record<ApprovalRisk, { label: string; icon: IconName }> = {
  low: { label: 'Low risk', icon: 'shield' },
  medium: { label: 'Medium risk', icon: 'alert' },
  high: { label: 'High risk', icon: 'alert' },
};

/**
 * Figma: Approval — human-in-the-loop confirmation before an agent acts. Approve, deny, or edit then approve.
 * The person stays the decision-maker; the outcome stays visible after they decide.
 */
export function Approval({ title, children, risk = 'medium', toolName, details, editableText, editLabel = 'Edit before approving', alwaysAllowLabel, status = 'pending', approveLabel = 'Approve', denyLabel = 'Deny', onApprove, onDeny, className }: ApprovalProps) {
  const id = useFieldId();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(editableText ?? '');
  const [always, setAlways] = useState(false);
  const r = RISK[risk];
  const pending = status === 'pending';
  const edited = editableText !== undefined && draft !== editableText;

  return (
    <section className={cx('at-approval', `at-approval--${risk}`, !pending && `is-${status}`, className)} aria-labelledby={`${id}-title`} aria-describedby={children ? `${id}-desc` : undefined}>
      <div className="at-approval__head">
        <span className="at-approval__icon" aria-hidden="true"><Icon name={r.icon} size={18} /></span>
        <div className="at-approval__heading">
          <p className="at-approval__eyebrow">
            <span className={cx('at-approval__risk', `at-approval__risk--${risk}`)}>{r.label}</span>
            {toolName ? <code className="at-approval__tool">{toolName}</code> : null}
          </p>
          <p id={`${id}-title`} className="at-approval__title">{title}</p>
          {children ? <div id={`${id}-desc`} className="at-approval__desc">{children}</div> : null}
        </div>
      </div>

      {editing && pending ? (
        <div className="at-field at-approval__edit">
          <label htmlFor={`${id}-edit`} className="at-field__label">{editLabel}</label>
          <textarea id={`${id}-edit`} className="at-approval__textarea" value={draft} rows={Math.min(8, Math.max(3, draft.split('\n').length))} onChange={(e) => setDraft(e.target.value)} />
        </div>
      ) : details || editableText !== undefined ? (
        <div className="at-approval__details">{editableText !== undefined ? <p className="at-approval__preview">{draft}</p> : details}</div>
      ) : null}

      {pending ? (
        <div className="at-approval__foot">
          {alwaysAllowLabel ? <Checkbox size="sm" label={alwaysAllowLabel} checked={always} onChange={(e) => setAlways(e.target.checked)} /> : <span />}
          <div className="at-approval__actions">
            <Button size="sm" hierarchy="tertiary" onClick={onDeny}>{denyLabel}</Button>
            {editableText !== undefined && !editing ? (
              <Button size="sm" hierarchy="outline" iconLeading={<Icon name="edit" size={16} />} onClick={() => setEditing(true)}>Edit</Button>
            ) : null}
            <Button size="sm" hierarchy="primary" onClick={() => onApprove?.({ alwaysAllow: always, editedText: edited ? draft : undefined })}>
              {editing && edited ? 'Save and approve' : approveLabel}
            </Button>
          </div>
        </div>
      ) : (
        <p className={cx('at-approval__outcome', `at-approval__outcome--${status}`)} role="status">
          <Icon name={status === 'approved' ? 'success' : 'x'} size={16} />
          {status === 'approved' ? 'Approved' : 'Denied'}
          {status === 'approved' && edited ? ' with your edits' : ''}
        </p>
      )}
    </section>
  );
}
