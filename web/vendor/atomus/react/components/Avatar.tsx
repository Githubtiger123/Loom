import type { HTMLAttributes, ReactNode } from 'react';
import { cx } from '../utils';
import { Icon } from './Icon';

export type AvatarSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl' | '2xl';

export interface AvatarProps extends HTMLAttributes<HTMLSpanElement> {
  /** Person or workspace name — used for initials, tooltip and accessible name */
  name?: string;
  /** Figma: Type=Image */
  src?: string;
  /** Figma: Initials — overrides the initials derived from name */
  initials?: string;
  /** Figma: Type=Icon + Icon swap */
  icon?: ReactNode;
  /** Figma: Size — xs 24 · sm 32 · md 40 · lg 48 · xl 56 · 2xl 64 */
  size?: AvatarSize;
  /** Figma: Shape */
  shape?: 'circle' | 'rounded';
  /** Figma: Status */
  status?: 'online' | 'away' | 'offline';
}

function deriveInitials(name?: string): string {
  if (!name) return '';
  const parts = name.trim().split(/\s+/);
  const first = parts[0]?.[0] ?? '';
  const last = parts.length > 1 ? parts[parts.length - 1][0] : '';
  return (first + last).toUpperCase();
}

export function Avatar({ name, src, initials, icon, size = 'md', shape = 'circle', status, className, ...rest }: AvatarProps) {
  const text = initials ?? deriveInitials(name);
  return (
    <span className={cx('at-avatar', `at-avatar--${size}`, `at-avatar--${shape}`, className)} title={name} {...rest}>
      {src ? (
        <img src={src} alt={name ?? ''} />
      ) : text ? (
        <span className="at-avatar__initials" role="img" aria-label={name ?? text}>{text}</span>
      ) : (
        <span className="at-avatar__icon" role="img" aria-label={name ?? 'User'}>{icon ?? <Icon name="user" size={20} />}</span>
      )}
      {status ? <span className={cx('at-avatar__status', `at-avatar__status--${status}`)} role="status" aria-label={status} /> : null}
    </span>
  );
}
