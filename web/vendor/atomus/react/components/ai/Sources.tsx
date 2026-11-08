import { useCallback, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { cx, useFieldId } from '../../utils';
import { Icon } from '../Icon';
import { useControllable } from './internal';

export interface SourceItem {
  /** Stable key */
  id: string;
  /** Page or document title */
  title: string;
  /** Link to the source */
  url?: string;
  /** Site shown under the title; derived from `url` when omitted */
  domain?: string;
  /** The passage the answer relied on */
  snippet?: string;
  /** Site icon or file-type icon (16px) */
  icon?: ReactNode;
}

function domainOf(s: SourceItem): string {
  if (s.domain) return s.domain;
  try {
    return s.url ? new URL(s.url).hostname.replace(/^www\./, '') : '';
  } catch {
    return '';
  }
}

export interface SourcesProps {
  /** Figma: Sources — in citation order; item 1 is citation [1]. */
  sources: SourceItem[];
  /** Figma: Label — the disclosure text; defaults to "N sources". */
  label?: string;
  /**
   * Figma: Style.
   * `collapsible` (default) — a "N sources" disclosure under the answer.
   * `list` — always expanded, for side panels.
   */
  variant?: 'collapsible' | 'list';
  /** Expanded (controlled, `collapsible` only) */
  open?: boolean;
  /** Initial expanded state when uncontrolled */
  defaultOpen?: boolean;
  /** Called when the person expands or collapses it */
  onOpenChange?: (open: boolean) => void;
  className?: string;
}

/** Figma: Sources — the numbered list of sources behind an AI answer. Numbers match InlineCitation. */
export function Sources({ sources, label, variant = 'collapsible', open, defaultOpen = false, onOpenChange, className }: SourcesProps) {
  const id = useFieldId();
  const [isOpen, setOpen] = useControllable(open, defaultOpen, onOpenChange);
  const expanded = variant === 'list' || isOpen;
  const text = label ?? `${sources.length} source${sources.length === 1 ? '' : 's'}`;
  const list = (
    <ol id={`${id}-list`} className="at-sources__list" aria-label={variant === 'list' ? text : undefined}>
      {sources.map((s, i) => {
        const domain = domainOf(s);
        const inner = (
          <>
            <span className="at-sources__num" aria-hidden="true">{i + 1}</span>
            <span className="at-sources__main">
              <span className="at-sources__title">{s.title}</span>
              <span className="at-sources__domain">
                {s.icon ?? <Icon name="globe" size={12} />}
                {domain}
              </span>
              {s.snippet ? <span className="at-sources__snippet">{s.snippet}</span> : null}
            </span>
            {s.url ? <Icon name="external" size={14} className="at-sources__ext" /> : null}
          </>
        );
        return (
          <li key={s.id} id={`source-${s.id}`} className="at-sources__item">
            {s.url ? (
              <a className="at-sources__link" href={s.url} target="_blank" rel="noreferrer noopener">
                {inner}
                <span className="at-vh"> (source {i + 1}, opens in a new tab)</span>
              </a>
            ) : (
              <div className="at-sources__link">{inner}</div>
            )}
          </li>
        );
      })}
    </ol>
  );
  if (variant === 'list') return <div className={cx('at-sources', 'at-sources--list', className)}>{list}</div>;
  return (
    <div className={cx('at-sources', expanded && 'is-open', className)}>
      <button type="button" className="at-sources__trigger" aria-expanded={expanded} aria-controls={`${id}-list`} onClick={() => setOpen(!isOpen)}>
        <span className="at-sources__stack" aria-hidden="true">
          {sources.slice(0, 3).map((s) => <span key={s.id} className="at-sources__favicon">{s.icon ?? <Icon name="globe" size={12} />}</span>)}
        </span>
        {text}
        <Icon name="chevronDown" size={16} className="at-sources__chev" />
      </button>
      {expanded ? list : null}
    </div>
  );
}

export interface InlineCitationProps {
  /** Figma: Number — the citation number, matching the source's position in Sources */
  index: number;
  /** The cited source; its title, site and snippet fill the preview card */
  source: SourceItem;
  className?: string;
}

/**
 * Figma: Inline citation — a numbered chip after a claim. Hover or focus shows a preview card;
 * the chip links to the source. Esc hides the preview.
 */
export function InlineCitation({ index, source, className }: InlineCitationProps) {
  const id = useFieldId();
  const [open, setOpen] = useState(false);
  const hideTimer = useRef<number | undefined>(undefined);
  const show = useCallback(() => { window.clearTimeout(hideTimer.current); setOpen(true); }, []);
  const hide = useCallback(() => { hideTimer.current = window.setTimeout(() => setOpen(false), 120); }, []);
  const domain = domainOf(source);
  const chipProps = {
    className: 'at-cite__chip',
    'aria-describedby': `${id}-card`,
    onFocus: show,
    onBlur: hide,
    onKeyDown: (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); },
  };
  const label = `Source ${index}: ${source.title}`;
  return (
    <span className={cx('at-cite', className)} onMouseEnter={show} onMouseLeave={hide}>
      {source.url ? (
        <a {...chipProps} href={source.url} target="_blank" rel="noreferrer noopener" aria-label={`${label} (opens in a new tab)`}>{index}</a>
      ) : (
        <span {...chipProps} tabIndex={0} role="note" aria-label={label}>{index}</span>
      )}
      <span id={`${id}-card`} role="tooltip" className={cx('at-cite__card', open && 'is-open')}>
        <span className="at-cite__domain">{source.icon ?? <Icon name="globe" size={12} />}{domain || `Source ${index}`}</span>
        <span className="at-cite__title">{source.title}</span>
        {source.snippet ? <span className="at-cite__snippet">{source.snippet}</span> : null}
      </span>
    </span>
  );
}
