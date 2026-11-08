import { useEffect, useRef, type ReactNode } from 'react';
import { cx, useFieldId } from '../utils';
import { Icon } from './Icon';

export interface ModalProps {
  open: boolean;
  onClose: () => void;
  /** Figma: Title */
  title: ReactNode;
  /** Figma: Description */
  description?: ReactNode;
  /** Figma: Content slot */
  children?: ReactNode;
  /** Figma: Actions — buttons, primary last */
  actions?: ReactNode;
  /** Figma: Featured icon — true for the default glyph, or pass an icon */
  featuredIcon?: boolean | ReactNode;
  /** Figma: Close button */
  closeButton?: boolean;
  /** Figma: Size — sm 400 · md 544 · lg 720 */
  size?: 'sm' | 'md' | 'lg';
  /** Figma: Type — destructive tints the featured icon red */
  type?: 'default' | 'destructive';
  className?: string;
}

/** Figma: Modal. Uses the native dialog element: focus trap, Esc to close and inert background come built in. */
export function Modal({ open, onClose, title, description, children, actions, featuredIcon, closeButton = true, size = 'md', type = 'default', className }: ModalProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useFieldId();
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  const icon = featuredIcon === true ? <Icon name={type === 'destructive' ? 'alert' : 'info'} size={24} /> : featuredIcon || null;
  return (
    <dialog
      ref={ref}
      className={cx('at-modal', `at-modal--${size}`, `at-modal--${type}`, className)}
      aria-labelledby={`${id}-title`}
      aria-describedby={description ? `${id}-desc` : undefined}
      onClose={onClose}
      onCancel={(e) => { e.preventDefault(); onClose(); }}
      onClick={(e) => { if (e.target === ref.current) onClose(); }}
    >
      <div className="at-modal__panel">
        <header className="at-modal__header">
          {icon ? <span className="at-modal__icon" aria-hidden="true">{icon}</span> : null}
          <div className="at-modal__heading">
            <h2 id={`${id}-title`} className="at-modal__title">{title}</h2>
            {description ? <p id={`${id}-desc`} className="at-modal__desc">{description}</p> : null}
          </div>
          {closeButton ? (
            <button type="button" className="at-modal__close" aria-label="Close" onClick={onClose}>
              <Icon name="x" size={20} />
            </button>
          ) : null}
        </header>
        {children ? <div className="at-modal__content">{children}</div> : null}
        {actions ? <footer className="at-modal__actions">{actions}</footer> : null}
      </div>
    </dialog>
  );
}
