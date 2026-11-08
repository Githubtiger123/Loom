import { useEffect, useRef, useState, type HTMLAttributes, type ReactNode } from 'react';
import { cx } from '../../utils';

export interface StreamingTextProps extends Omit<HTMLAttributes<HTMLDivElement>, 'children'> {
  /** The text received so far. Pass the growing string on every chunk; the component only appends. */
  text: string;
  /** Figma: State=Streaming — shows the caret and keeps the region busy for screen readers. */
  streaming?: boolean;
  /** Figma: Caret — blinking caret at the end while streaming (static under reduced motion). */
  caret?: boolean;
  /**
   * How screen readers hear the stream (Primer: announce progress, not every token).
   * `polite` (default) — finished sentences in batches, at most every `announceInterval` ms.
   * `end` — only `doneMessage` once the stream ends.
   * `off` — nothing; use when an outer live region already announces the reply.
   */
  announce?: 'polite' | 'end' | 'off';
  /** Minimum time between announcements in ms */
  announceInterval?: number;
  /** Announced when the stream ends with `announce="end"` */
  doneMessage?: string;
  /** Renders the text (e.g. Markdown). Defaults to plain text with preserved line breaks. */
  render?: (text: string) => ReactNode;
}

const SENTENCE_END = /[.!?…:;](\s|$)|\n/g;

/** Last index (exclusive) of a finished sentence in `s`, or 0. */
function sentenceCut(s: string): number {
  let cut = 0;
  for (const m of s.matchAll(SENTENCE_END)) cut = (m.index ?? 0) + m[0].length;
  return cut;
}

/**
 * Figma: Streaming text — renders an AI reply as it arrives, with a caret and batched,
 * polite screen-reader announcements. Framework-agnostic: feed it any growing string.
 */
export function StreamingText({ text, streaming = false, caret = true, announce = 'polite', announceInterval = 1500, doneMessage = 'Response complete', render, className, ...rest }: StreamingTextProps) {
  const [live, setLive] = useState('');
  const spoken = useRef(0);
  const last = useRef(0);
  const textRef = useRef(text);
  textRef.current = text;

  // A new stream (text got shorter or was cleared) starts announcing from the top.
  useEffect(() => {
    if (text.length < spoken.current) spoken.current = 0;
  }, [text]);

  useEffect(() => {
    if (announce !== 'polite' || !streaming) return;
    const tick = () => {
      const pending = textRef.current.slice(spoken.current);
      const cut = sentenceCut(pending);
      if (cut > 0 && Date.now() - last.current >= announceInterval) {
        setLive(pending.slice(0, cut).trim());
        spoken.current += cut;
        last.current = Date.now();
      }
    };
    const t = window.setInterval(tick, Math.max(250, announceInterval / 3));
    return () => window.clearInterval(t);
  }, [announce, streaming, announceInterval]);

  // When the stream ends: say the rest (polite) or the done message (end), then clear the region.
  const wasStreaming = useRef(streaming);
  useEffect(() => {
    if (wasStreaming.current && !streaming) {
      if (announce === 'polite') {
        const rest = textRef.current.slice(spoken.current).trim();
        spoken.current = textRef.current.length;
        if (rest) setLive(rest);
      } else if (announce === 'end') setLive(doneMessage);
    }
    wasStreaming.current = streaming;
  }, [streaming, announce, doneMessage]);

  useEffect(() => {
    if (!live) return;
    const t = window.setTimeout(() => setLive(''), 6000);
    return () => window.clearTimeout(t);
  }, [live]);

  return (
    <div className={cx('at-stream', streaming && 'is-streaming', className)} {...rest}>
      <div className="at-stream__text" aria-busy={streaming || undefined}>
        {render ? render(text) : text}
        {streaming && caret ? <span className="at-stream__caret" aria-hidden="true" /> : null}
      </div>
      {announce !== 'off' ? <div className="at-vh" role="status" aria-live="polite" aria-atomic="true">{live}</div> : null}
    </div>
  );
}

export interface ShimmerProps extends HTMLAttributes<HTMLSpanElement> {
  /** Figma: Label — short status text such as "Thinking…" or "Searching the web…" */
  children: ReactNode;
  /** Figma: Animated — when false the text renders static (as under reduced motion). */
  active?: boolean;
}

/**
 * Figma: Shimmer — a light sweep across status text while the agent works.
 * The sweep uses --gradient-ai and --motion-stream-shimmer-duration; reduced motion shows static text.
 */
export function Shimmer({ children, active = true, className, ...rest }: ShimmerProps) {
  return (
    <span className={cx('at-shimmer', active && 'is-active', className)} {...rest}>
      {children}
    </span>
  );
}
