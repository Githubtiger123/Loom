import type { SVGProps } from 'react';
import { cx } from '../utils';

/**
 * Minimal glyphs the components need internally (check, close, status).
 * For product icons use the Atomus icon set (Font Awesome names) and pass them as ReactNodes.
 */
const PATHS = {
  check: 'M4 10.5l3.5 3.5L16 6',
  minus: 'M5 10h10',
  x: 'M5 5l10 10M15 5L5 15',
  info: 'M10 9v5M10 6.5v.01M10 18a8 8 0 100-16 8 8 0 000 16z',
  alert: 'M10 7v4M10 13.5v.01M8.6 3.3L2 15a1.6 1.6 0 001.4 2.4h13.2A1.6 1.6 0 0018 15L11.4 3.3a1.6 1.6 0 00-2.8 0z',
  success: 'M6.5 10l2.5 2.5 4.5-5M10 18a8 8 0 100-16 8 8 0 000 16z',
  plus: 'M10 4v12M4 10h12',
  search: 'M9 15A6 6 0 109 3a6 6 0 000 12zM17 17l-3.8-3.8',
  folder: 'M2.5 6.5A1.5 1.5 0 014 5h3.5l1.5 2h7a1.5 1.5 0 011.5 1.5v6A1.5 1.5 0 0116 16H4a1.5 1.5 0 01-1.5-1.5v-8z',
  user: 'M10 10a3.5 3.5 0 100-7 3.5 3.5 0 000 7zM3.5 17.5a6.5 6.5 0 0113 0',
  arrowUp: 'M10 15V5M5.5 9.5L10 5l4.5 4.5',
  arrowDown: 'M10 5v10M5.5 10.5L10 15l4.5-4.5',
  chevronDown: 'M5 7.5l5 5 5-5',
  chevronUp: 'M5 12.5l5-5 5 5',
  chevronLeft: 'M12.5 5l-5 5 5 5',
  chevronRight: 'M7.5 5l5 5-5 5',
  calendar: 'M3 7.5h14M6.5 2.5v3M13.5 2.5v3M4.5 4h11A1.5 1.5 0 0117 5.5v10a1.5 1.5 0 01-1.5 1.5h-11A1.5 1.5 0 013 15.5v-10A1.5 1.5 0 014.5 4z',
  sort: 'M6.5 8L10 4.5 13.5 8M6.5 12L10 15.5 13.5 12',
  menu: 'M3.5 5.5h13M3.5 10h13M3.5 14.5h13',
  home: 'M3 9l7-6 7 6v7.5a1 1 0 01-1 1h-3.5v-5h-5v5H4a1 1 0 01-1-1V9z',
  settings: 'M10 12.5a2.5 2.5 0 100-5 2.5 2.5 0 000 5zM16.2 12.3l1.3 1-1.6 2.8-1.6-.6a6 6 0 01-1.8 1l-.3 1.7H8.8l-.3-1.7a6 6 0 01-1.8-1l-1.6.6-1.6-2.8 1.3-1a6 6 0 010-2.1l-1.3-1 1.6-2.8 1.6.6a6 6 0 011.8-1l.3-1.7h3.2l.3 1.7a6 6 0 011.8 1l1.6-.6 1.6 2.8-1.3 1a6 6 0 010 2.1z',
  bell: 'M10 17.5a1.8 1.8 0 001.7-1.2M5 8a5 5 0 0110 0c0 4.5 2 5.5 2 5.5H3S5 12.5 5 8z',
  more: 'M10 5.5v.01M10 10v.01M10 14.5v.01',
  sparkle: 'M9 2.5l1.4 3.9a2 2 0 001.2 1.2L15.5 9l-3.9 1.4a2 2 0 00-1.2 1.2L9 15.5l-1.4-3.9a2 2 0 00-1.2-1.2L2.5 9l3.9-1.4a2 2 0 001.2-1.2L9 2.5zM15.5 13v4M13.5 15h4',
  copy: 'M7 7V4.5A1.5 1.5 0 018.5 3h7A1.5 1.5 0 0117 4.5v7a1.5 1.5 0 01-1.5 1.5H13M4.5 7h7A1.5 1.5 0 0113 8.5v7a1.5 1.5 0 01-1.5 1.5h-7A1.5 1.5 0 013 15.5v-7A1.5 1.5 0 014.5 7z',
  refresh: 'M16.5 10a6.5 6.5 0 11-1.9-4.6M16.5 3.5V7H13',
  edit: 'M11.5 5l3.5 3.5M3.5 16.5l.8-3.6 9.3-9.3a1.4 1.4 0 012 0l.8.8a1.4 1.4 0 010 2l-9.3 9.3-3.6.8z',
  thumbUp: 'M6.5 9v8M6.5 9l3-5.5a1.6 1.6 0 013 .9L12 8h3.8a1.5 1.5 0 011.5 1.8l-1.1 5.9a1.5 1.5 0 01-1.5 1.3H6.5M6.5 9H3.5v8h3',
  thumbDown: 'M13.5 11V3M13.5 11l-3 5.5a1.6 1.6 0 01-3-.9L8 12H4.2a1.5 1.5 0 01-1.5-1.8l1.1-5.9A1.5 1.5 0 015.3 3h8.2M13.5 11h3V3h-3',
  stop: 'M6.5 5h7A1.5 1.5 0 0115 6.5v7a1.5 1.5 0 01-1.5 1.5h-7A1.5 1.5 0 015 13.5v-7A1.5 1.5 0 016.5 5z',
  paperclip: 'M15.5 9.5l-5.8 5.8a3.5 3.5 0 01-5-5l6.4-6.4a2.3 2.3 0 013.3 3.3l-6.4 6.4a1.2 1.2 0 01-1.7-1.7l5.8-5.8',
  tool: 'M12.3 3.2a4 4 0 00-4.9 5.3l-4.3 4.3a1.6 1.6 0 002.3 2.3l4.3-4.3a4 4 0 005.3-4.9l-2.5 2.5-2.2-.5-.5-2.2 2.5-2.5z',
  shield: 'M10 2.5l6 2.2v4.6c0 3.9-2.6 6.6-6 8.2-3.4-1.6-6-4.3-6-8.2V4.7l6-2.2z',
  clock: 'M10 6v4l2.5 2M10 18a8 8 0 100-16 8 8 0 000 16z',
  globe: 'M10 18a8 8 0 100-16 8 8 0 000 16zM2 10h16M10 2c2.1 2.3 3.2 5 3.2 8s-1.1 5.7-3.2 8c-2.1-2.3-3.2-5-3.2-8s1.1-5.7 3.2-8z',
  file: 'M11.5 2.5H6A1.5 1.5 0 004.5 4v12A1.5 1.5 0 006 17.5h8a1.5 1.5 0 001.5-1.5V6.5l-4-4zM11.5 2.5v4h4',
  external: 'M11 3.5h5.5V9M16.5 3.5L9 11M14.5 12v3a1.5 1.5 0 01-1.5 1.5H5A1.5 1.5 0 013.5 15V7A1.5 1.5 0 015 5.5h3',
  undo: 'M7.5 4.5L4 8l3.5 3.5M4 8h8a4.5 4.5 0 010 9H9',
  lightbulb: 'M7.5 15h5M8.5 17.5h3M10 2.5a5 5 0 00-3 9c.6.5 1 1.2 1 2v.5h4v-.5c0-.8.4-1.5 1-2a5 5 0 00-3-9z',
  code: 'M7 6l-4 4 4 4M13 6l4 4-4 4',
} as const;

export type IconName = keyof typeof PATHS;

export interface IconProps extends Omit<SVGProps<SVGSVGElement>, 'name'> {
  name: IconName;
  size?: number;
}

export function Icon({ name, size = 20, className, ...rest }: IconProps) {
  return (
    <svg
      className={cx('at-icon', className)}
      width={size}
      height={size}
      viewBox="0 0 20 20"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.67}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...rest}
    >
      <path d={PATHS[name]} />
    </svg>
  );
}
