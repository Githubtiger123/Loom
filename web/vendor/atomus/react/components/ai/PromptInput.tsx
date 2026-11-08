import { useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react';
import { cx, useFieldId } from '../../utils';
import { Button } from '../Button';
import { Icon } from '../Icon';
import { useControllable } from './internal';

export interface PromptAttachment {
  /** Stable key */
  id: string;
  /** File name shown on the chip */
  name: string;
  /** `image` shows `previewUrl` as a thumbnail */
  kind?: 'file' | 'image';
  /** Human-readable size ("2.4 MB") */
  size?: string;
  /** Thumbnail for images */
  previewUrl?: string;
  /** `uploading` shows a spinner; `error` marks the chip */
  status?: 'uploading' | 'ready' | 'error';
}

export interface PromptCommand {
  /** Inserted after the trigger character ("summarize" → "/summarize ") */
  value: string;
  /** Text in the menu */
  label: string;
  /** One line under the label */
  description?: string;
  /** Leading icon (16px) */
  icon?: ReactNode;
  /** Exact text to insert instead of trigger + value */
  insert?: string;
}

export interface PromptTrigger {
  /** The character that opens the menu: "/" for commands, "@" for mentions */
  char: string;
  /** Accessible name of the menu ("Commands", "People") */
  label?: string;
  /** The menu items, filtered by what is typed after the character */
  items: PromptCommand[];
  /** Called when an item is chosen (after its text is inserted) */
  onSelect?: (item: PromptCommand) => void;
}

export interface PromptInputProps {
  /** Figma: Text (controlled) */
  value?: string;
  /** Initial text when uncontrolled */
  defaultValue?: string;
  onChange?: (value: string) => void;
  /** Called with the trimmed text and the attachments on Enter or the send button */
  onSubmit?: (text: string, attachments: PromptAttachment[]) => void;
  /** Called by the stop button while `status` is `submitted` or `streaming` */
  onStop?: () => void;
  /** Figma: State — `submitted` and `streaming` turn Send into Stop and block new submits. */
  status?: 'ready' | 'submitted' | 'streaming' | 'error';
  /** Figma: Placeholder — a hint, never the label */
  placeholder?: string;
  /** Accessible name of the text area (visually hidden) */
  label?: string;
  /** Figma: Attachments — chips above the text */
  attachments?: PromptAttachment[];
  /** Figma: Attach button — shows the paperclip button; open your file picker here */
  onAttach?: () => void;
  /** Called by a chip's remove button */
  onRemoveAttachment?: (id: string) => void;
  /** Figma: Toolbar slot — left of the bottom bar (ModelSelector, tool toggles) */
  toolbar?: ReactNode;
  /** Figma: Actions slot — right of the bottom bar, before Send (ContextMeter, voice) */
  actions?: ReactNode;
  /** Figma: Command menu — "/" and "@" menus that open while typing */
  triggers?: PromptTrigger[];
  /** Figma: Disclaimer — one line under the input ("AI can make mistakes. Check important info.") */
  disclaimer?: ReactNode;
  /** Rows shown when empty */
  minRows?: number;
  /** Rows before the text area scrolls */
  maxRows?: number;
  /** Figma: Size — md for side panels, lg for a full-page chat */
  size?: 'md' | 'lg';
  disabled?: boolean;
  /** Focuses the text area on mount — only on pages whose main task is the chat */
  autoFocus?: boolean;
  /** Accessible name of the send button */
  submitLabel?: string;
  /** Accessible name of the stop button */
  stopLabel?: string;
  /** Clears the text after submit (uncontrolled only) */
  clearOnSubmit?: boolean;
  className?: string;
}

interface MenuState { trigger: PromptTrigger; start: number; query: string }

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * Figma: Prompt input — the composer of an AI chat: auto-growing text, attachments, a toolbar slot,
 * send ↔ stop, and "/" or "@" menus. Enter sends, Shift+Enter adds a line, Esc closes a menu.
 */
export function PromptInput({ value, defaultValue = '', onChange, onSubmit, onStop, status = 'ready', placeholder = 'Ask anything…', label = 'Message', attachments = [], onAttach, onRemoveAttachment, toolbar, actions, triggers = [], disclaimer, minRows = 1, maxRows = 8, size = 'md', disabled, autoFocus, submitLabel = 'Send message', stopLabel = 'Stop generating', clearOnSubmit = true, className }: PromptInputProps) {
  const id = useFieldId();
  const [text, setText] = useControllable(value, defaultValue, onChange);
  const area = useRef<HTMLTextAreaElement>(null);
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [active, setActive] = useState(0);
  const busy = status === 'submitted' || status === 'streaming';
  const canSend = !disabled && !busy && (text.trim().length > 0 || attachments.length > 0) && !attachments.some((a) => a.status === 'uploading');

  // Auto-grow between minRows and maxRows.
  useLayoutEffect(() => {
    const el = area.current;
    if (!el) return;
    const cs = getComputedStyle(el);
    const lh = parseFloat(cs.lineHeight) || 24;
    const pad = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom);
    el.style.height = 'auto';
    const max = lh * maxRows + pad;
    const min = lh * minRows + pad;
    el.style.height = `${Math.max(min, Math.min(el.scrollHeight, max))}px`;
    el.style.overflowY = el.scrollHeight > max ? 'auto' : 'hidden';
  }, [text, minRows, maxRows]);

  const matches = useMemo(() => {
    if (!menu) return [];
    const q = menu.query.toLowerCase();
    return menu.trigger.items.filter((it) => !q || it.value.toLowerCase().includes(q) || it.label.toLowerCase().includes(q));
  }, [menu]);
  useEffect(() => setActive(0), [menu?.query, menu?.trigger]);

  const findMenu = (t: string, caret: number): MenuState | null => {
    if (!triggers.length) return null;
    const before = t.slice(0, caret);
    const chars = triggers.map((tr) => escapeRe(tr.char)).join('|');
    const m = before.match(new RegExp(`(^|\\s)(${chars})([^\\s]*)$`));
    if (!m) return null;
    const trigger = triggers.find((tr) => tr.char === m[2]);
    return trigger ? { trigger, start: before.length - m[3].length - m[2].length, query: m[3] } : null;
  };

  const update = (t: string, caret: number) => {
    setText(t);
    setMenu(findMenu(t, caret));
  };

  const choose = (item: PromptCommand) => {
    if (!menu || !area.current) return;
    const caret = area.current.selectionStart;
    const ins = (item.insert ?? `${menu.trigger.char}${item.value}`) + ' ';
    const next = text.slice(0, menu.start) + ins + text.slice(caret);
    setText(next);
    setMenu(null);
    menu.trigger.onSelect?.(item);
    const pos = menu.start + ins.length;
    requestAnimationFrame(() => { area.current?.focus(); area.current?.setSelectionRange(pos, pos); });
  };

  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    if (busy) { onStop?.(); return; }
    if (!canSend) return;
    onSubmit?.(text.trim(), attachments);
    if (clearOnSubmit && value === undefined) setText('');
    setMenu(null);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (menu && matches.length) {
      if (e.key === 'ArrowDown') { e.preventDefault(); setActive((active + 1) % matches.length); return; }
      if (e.key === 'ArrowUp') { e.preventDefault(); setActive((active - 1 + matches.length) % matches.length); return; }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); choose(matches[active]); return; }
    }
    if (menu && e.key === 'Escape') { e.preventDefault(); setMenu(null); return; }
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      if (!busy) submit();
    }
  };

  const menuOpen = !!menu && matches.length > 0;
  const listId = `${id}-menu`;

  return (
    <form className={cx('at-prompt-wrap', className)} onSubmit={submit}>
      <div
        className={cx('at-prompt', `at-prompt--${size}`, busy && 'is-busy', disabled && 'is-disabled', status === 'error' && 'is-error')}
        onClick={(e) => { if (e.target === e.currentTarget) area.current?.focus(); }}
      >
        {attachments.length ? (
          <ul className="at-prompt__files" aria-label="Attachments">
            {attachments.map((a) => (
              <li key={a.id} className={cx('at-prompt__file', a.status === 'error' && 'is-error')}>
                <span className="at-prompt__file-thumb" aria-hidden="true">
                  {a.status === 'uploading' ? <span className="at-spinner" /> : a.kind === 'image' && a.previewUrl ? <img src={a.previewUrl} alt="" /> : <Icon name="file" size={16} />}
                </span>
                <span className="at-prompt__file-text">
                  <span className="at-prompt__file-name">{a.name}</span>
                  {a.size || a.status === 'uploading' || a.status === 'error' ? (
                    <span className="at-prompt__file-meta">{a.status === 'uploading' ? 'Uploading…' : a.status === 'error' ? 'Upload failed' : a.size}</span>
                  ) : null}
                </span>
                {onRemoveAttachment ? (
                  <button type="button" className="at-prompt__file-x" aria-label={`Remove ${a.name}`} onClick={() => onRemoveAttachment(a.id)}>
                    <Icon name="x" size={14} />
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        ) : null}
        <label htmlFor={id} className="at-vh">{label}</label>
        <textarea
          ref={area}
          id={id}
          className="at-prompt__textarea"
          value={text}
          rows={minRows}
          placeholder={placeholder}
          disabled={disabled}
          autoFocus={autoFocus}
          aria-describedby={disclaimer ? `${id}-disc` : undefined}
          aria-autocomplete={triggers.length ? 'list' : undefined}
          aria-controls={menuOpen ? listId : undefined}
          aria-activedescendant={menuOpen ? `${listId}-${active}` : undefined}
          onChange={(e) => update(e.target.value, e.target.selectionStart)}
          onKeyDown={onKeyDown}
          onClick={(e) => setMenu(findMenu(text, e.currentTarget.selectionStart))}
          onBlur={() => setMenu(null)}
        />
        <div className="at-prompt__bar">
          <div className="at-prompt__tools">
            {onAttach ? <Button size="sm" hierarchy="tertiary" iconOnly aria-label="Attach files" title="Attach files" disabled={disabled} iconLeading={<Icon name="paperclip" size={18} />} onClick={onAttach} /> : null}
            {toolbar}
          </div>
          <div className="at-prompt__actions">
            {actions}
            {busy ? (
              <button type="submit" className="at-prompt__send at-prompt__send--stop" aria-label={stopLabel} title={stopLabel} disabled={!onStop}>
                <span className="at-prompt__stop" aria-hidden="true" />
              </button>
            ) : (
              <button type="submit" className="at-prompt__send" aria-label={submitLabel} title={submitLabel} disabled={!canSend}>
                <Icon name="arrowUp" size={18} />
              </button>
            )}
          </div>
        </div>
        {menuOpen ? (
          <div id={listId} role="listbox" aria-label={menu.trigger.label ?? (menu.trigger.char === '@' ? 'Mentions' : 'Commands')} className="at-menu at-prompt__menu">
            {matches.map((it, i) => (
              <div
                key={it.value}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                className={cx('at-menu-item', 'at-menu-item--sm', 'at-prompt__option', i === active && 'is-active')}
                onMouseDown={(e) => e.preventDefault()}
                onMouseMove={() => setActive(i)}
                onClick={() => choose(it)}
              >
                {it.icon ? <span className="at-menu-item__icon">{it.icon}</span> : null}
                <span className="at-prompt__option-text">
                  <span className="at-prompt__option-label">{it.label}</span>
                  {it.description ? <span className="at-prompt__option-desc">{it.description}</span> : null}
                </span>
                <kbd className="at-menu-item__shortcut">{it.insert ?? `${menu.trigger.char}${it.value}`}</kbd>
              </div>
            ))}
          </div>
        ) : null}
      </div>
      <span className="at-vh" role="status" aria-live="polite">
        {menuOpen ? `${matches.length} ${matches.length === 1 ? 'suggestion' : 'suggestions'}. Use up and down arrows to choose, Enter to insert.` : busy ? 'Generating response' : ''}
      </span>
      {disclaimer ? <p id={`${id}-disc`} className="at-prompt__disclaimer">{disclaimer}</p> : null}
    </form>
  );
}
