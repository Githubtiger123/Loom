import { type ReactNode } from 'react';
import { cx } from '../../utils';
import { Icon } from '../Icon';

export interface SuggestionItem {
  /** Stable key; defaults to the label */
  id?: string;
  /** Text on the chip */
  label: string;
  /** Prompt to send or insert; defaults to `label` */
  prompt?: string;
  /** One line under the label (`cards` variant) */
  description?: string;
  /** Leading icon */
  icon?: ReactNode;
}

export interface SuggestionsProps {
  /** Figma: Items — strings or items with a separate prompt, description and icon. */
  suggestions: Array<string | SuggestionItem>;
  /** Called with the chosen prompt and the item. */
  onSelect?: (prompt: string, item: SuggestionItem) => void;
  /**
   * Figma: Action.
   * `send` (default) — choosing a chip sends the prompt right away.
   * `insert` — puts the prompt into the input so the person can edit it first.
   */
  mode?: 'send' | 'insert';
  /**
   * Figma: Style.
   * `chips` (default) — one row of pills that scrolls sideways (wraps with `wrap`).
   * `cards` — a grid of prompt starters for a welcome state.
   */
  variant?: 'chips' | 'cards';
  /** Chips wrap onto several lines instead of scrolling */
  wrap?: boolean;
  /** Accessible name of the group */
  label?: string;
  className?: string;
}

const toItem = (s: string | SuggestionItem): SuggestionItem => (typeof s === 'string' ? { label: s } : s);

/** Figma: Suggestions — follow-up prompts and prompt starters as chips or cards. */
export function Suggestions({ suggestions, onSelect, mode = 'send', variant = 'chips', wrap = false, label = 'Suggested prompts', className }: SuggestionsProps) {
  const items = suggestions.map(toItem);
  return (
    <div className={cx('at-suggestions', `at-suggestions--${variant}`, wrap && 'is-wrap', className)} role="group" aria-label={label}>
      <ul className="at-suggestions__list">
        {items.map((it) => {
          const prompt = it.prompt ?? it.label;
          return (
            <li key={it.id ?? it.label}>
              <button
                type="button"
                className="at-suggestion"
                title={mode === 'insert' ? 'Add to message' : undefined}
                onClick={() => onSelect?.(prompt, it)}
              >
                {it.icon ? <span className="at-suggestion__icon" aria-hidden="true">{it.icon}</span> : null}
                <span className="at-suggestion__text">
                  <span className="at-suggestion__label">{it.label}</span>
                  {variant === 'cards' && it.description ? <span className="at-suggestion__desc">{it.description}</span> : null}
                </span>
                <Icon name={mode === 'insert' ? 'plus' : 'arrowUp'} size={14} className="at-suggestion__go" />
                {mode === 'insert' ? <span className="at-vh"> (add to message)</span> : null}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
