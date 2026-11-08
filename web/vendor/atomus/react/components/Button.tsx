import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from 'react';
import { cx } from '../utils';

export type ButtonHierarchy = 'primary' | 'secondary' | 'outline' | 'tertiary' | 'link';
export type ButtonSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** Figma: Hierarchy */
  hierarchy?: ButtonHierarchy;
  /** Figma: Size — xs 24 · sm 32 · md 40 · lg 48 · xl 56 */
  size?: ButtonSize;
  /** Figma: Leading icon + Leading icon swap */
  iconLeading?: ReactNode;
  /** Figma: Trailing icon + Trailing icon swap */
  iconTrailing?: ReactNode;
  /** Figma: State=Loading */
  loading?: boolean;
  /** Square icon-only button (Figma: Button icon). Requires aria-label. */
  iconOnly?: boolean;
  fullWidth?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { hierarchy = 'secondary', size = 'md', iconLeading, iconTrailing, loading, iconOnly, fullWidth, disabled, className, children, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cx('at-btn', `at-btn--${hierarchy}`, `at-btn--${size}`, iconOnly && 'at-btn--icon', fullWidth && 'at-btn--full', className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading ? <span className="at-spinner" aria-hidden="true" /> : iconLeading}
      {iconOnly ? null : children}
      {iconOnly ? null : iconTrailing}
    </button>
  );
});
