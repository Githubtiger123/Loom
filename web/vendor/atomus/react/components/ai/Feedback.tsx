import { useEffect, useRef, useState } from 'react';
import { cx, useFieldId } from '../../utils';
import { Button } from '../Button';
import { Icon } from '../Icon';
import { useControllable } from './internal';

export type FeedbackRating = 'up' | 'down';

export interface FeedbackSubmission {
  /** The thumb that was pressed */
  rating: FeedbackRating;
  /** The reason chips the person picked */
  reasons: string[];
  /** Free text, may be empty */
  comment: string;
}

export interface FeedbackProps {
  /** Figma: Rating (controlled); null clears it */
  value?: FeedbackRating | null;
  /** Initial rating when uncontrolled */
  defaultValue?: FeedbackRating | null;
  /** Called on every thumb press */
  onChange?: (rating: FeedbackRating | null) => void;
  /** Called when the reason form is sent */
  onSubmit?: (feedback: FeedbackSubmission) => void;
  /** Figma: Reasons — chips offered after a thumbs down */
  reasons?: string[];
  /** Reasons offered after a thumbs up; no form opens for a thumbs up when empty */
  positiveReasons?: string[];
  /** Figma: Size — xs 24 · sm 32 */
  size?: 'xs' | 'sm';
  className?: string;
}

const DEFAULT_REASONS = ['Not accurate', 'Not helpful', 'Too long', 'Didn’t follow instructions', 'Unsafe or harmful'];

/**
 * Figma: Feedback — thumbs up / down on an AI reply, with an optional reason form.
 * Thumbs are toggle buttons (aria-pressed); the form opens in place and takes focus.
 */
export function Feedback({ value, defaultValue = null, onChange, onSubmit, reasons = DEFAULT_REASONS, positiveReasons = [], size = 'xs', className }: FeedbackProps) {
  const id = useFieldId();
  const [rating, setRating] = useControllable<FeedbackRating | null>(value, defaultValue, onChange);
  const [formOpen, setFormOpen] = useState(() => (defaultValue === 'down' && reasons.length > 0) || (defaultValue === 'up' && positiveReasons.length > 0));
  const [picked, setPicked] = useState<string[]>([]);
  const [comment, setComment] = useState('');
  const [sent, setSent] = useState(false);
  const first = useRef<HTMLButtonElement>(null);
  // Focus moves into the form only when the person opened it, never on mount.
  const focusForm = useRef(false);
  const options = rating === 'up' ? positiveReasons : reasons;

  useEffect(() => {
    if (formOpen && focusForm.current) first.current?.focus();
    focusForm.current = false;
  }, [formOpen]);

  const rate = (r: FeedbackRating) => {
    const next = rating === r ? null : r;
    setRating(next);
    setSent(false);
    setPicked([]);
    setComment('');
    focusForm.current = true;
    setFormOpen(!!next && (next === 'down' ? reasons.length > 0 : positiveReasons.length > 0));
  };
  const submit = () => {
    if (!rating) return;
    onSubmit?.({ rating, reasons: picked, comment });
    setFormOpen(false);
    setSent(true);
  };

  return (
    <div className={cx('at-feedback', className)}>
      <div className="at-feedback__thumbs" role="group" aria-label="Rate this response">
        <Button size={size} hierarchy="tertiary" iconOnly aria-label="Good response" aria-pressed={rating === 'up'} className={cx('at-feedback__thumb', rating === 'up' && 'is-on')} iconLeading={<Icon name="thumbUp" size={16} />} onClick={() => rate('up')} />
        <Button size={size} hierarchy="tertiary" iconOnly aria-label="Bad response" aria-pressed={rating === 'down'} className={cx('at-feedback__thumb', rating === 'down' && 'is-on')} iconLeading={<Icon name="thumbDown" size={16} />} onClick={() => rate('down')} />
      </div>
      <span className="at-feedback__status" role="status">{sent ? 'Thanks for your feedback' : ''}</span>
      {formOpen ? (
        <form
          className="at-feedback__form"
          aria-labelledby={`${id}-legend`}
          onSubmit={(e) => { e.preventDefault(); submit(); }}
          onKeyDown={(e) => { if (e.key === 'Escape') { setFormOpen(false); } }}
        >
          <p id={`${id}-legend`} className="at-feedback__legend">{rating === 'down' ? 'What went wrong?' : 'What did you like?'}</p>
          <div className="at-feedback__reasons" role="group" aria-labelledby={`${id}-legend`}>
            {options.map((r, i) => (
              <button
                key={r}
                ref={i === 0 ? first : undefined}
                type="button"
                className={cx('at-feedback__reason', picked.includes(r) && 'is-on')}
                aria-pressed={picked.includes(r)}
                onClick={() => setPicked(picked.includes(r) ? picked.filter((x) => x !== r) : [...picked, r])}
              >
                {picked.includes(r) ? <Icon name="check" size={14} /> : null}
                {r}
              </button>
            ))}
          </div>
          <label className="at-vh" htmlFor={`${id}-comment`}>Tell us more (optional)</label>
          <textarea id={`${id}-comment`} className="at-feedback__comment" rows={2} placeholder="Tell us more (optional)" value={comment} onChange={(e) => setComment(e.target.value)} />
          <div className="at-feedback__actions">
            <Button size="sm" hierarchy="tertiary" onClick={() => setFormOpen(false)}>Cancel</Button>
            <Button size="sm" hierarchy="secondary" type="submit">Send feedback</Button>
          </div>
        </form>
      ) : null}
    </div>
  );
}
