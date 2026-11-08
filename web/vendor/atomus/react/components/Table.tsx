import { useMemo, useState, type ReactNode } from 'react';
import { cx } from '../utils';
import { Icon } from './Icon';

export interface TableColumn<Row> {
  key: string;
  /** Figma: Table header cell — Label */
  header: ReactNode;
  /** Figma: Table header cell — Sortable */
  sortable?: boolean;
  /** Value used for sorting; defaults to row[key] */
  sortValue?: (row: Row) => string | number;
  /** Cell content; defaults to row[key]. Use Avatar, Badge or Buttons for the Figma cell types. */
  render?: (row: Row) => ReactNode;
  /** Supporting text under the main text (Figma: Type=Text + supporting) */
  supporting?: (row: Row) => ReactNode;
  align?: 'start' | 'end';
  width?: number | string;
}

export interface TableProps<Row> {
  columns: TableColumn<Row>[];
  rows: Row[];
  rowKey: (row: Row) => string;
  /** Figma: Table cell Size — sm 52 · md 72 */
  size?: 'sm' | 'md';
  /** Figma: Table header cell — Checkbox; adds a select-all column */
  selectable?: boolean;
  selected?: string[];
  onSelectedChange?: (keys: string[]) => void;
  /** Accessible table caption */
  caption?: string;
  /** Rendered when rows is empty, e.g. an EmptyState */
  empty?: ReactNode;
  className?: string;
}

/** Figma: Table header cell + Table cell. Sorting is built in; filtering and pagination stay with the page (Filter bar, Pagination). */
export function Table<Row extends Record<string, unknown>>({ columns, rows, rowKey, size = 'sm', selectable, selected, onSelectedChange, caption, empty, className }: TableProps<Row>) {
  const [sort, setSort] = useState<{ key: string; dir: 'asc' | 'desc' } | null>(null);
  const [innerSel, setInnerSel] = useState<string[]>([]);
  const sel = selected ?? innerSel;
  const setSel = (k: string[]) => { if (!selected) setInnerSel(k); onSelectedChange?.(k); };

  const sorted = useMemo(() => {
    if (!sort) return rows;
    const col = columns.find((c) => c.key === sort.key);
    const get = col?.sortValue ?? ((r: Row) => r[sort.key] as string | number);
    return [...rows].sort((a, b) => {
      const x = get(a), y = get(b);
      const r = typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y), undefined, { numeric: true });
      return sort.dir === 'asc' ? r : -r;
    });
  }, [rows, sort, columns]);

  const allKeys = rows.map(rowKey);
  const all = allKeys.length > 0 && allKeys.every((k) => sel.includes(k));
  const some = !all && allKeys.some((k) => sel.includes(k));

  return (
    <div className={cx('at-table-wrap', className)}>
      <table className={cx('at-table', `at-table--${size}`)}>
        {caption ? <caption className="at-table__caption">{caption}</caption> : null}
        <thead>
          <tr>
            {selectable ? (
              <th className="at-table__check" scope="col">
                <input
                  type="checkbox"
                  className="at-checkbox"
                  aria-label="Select all rows"
                  checked={all}
                  ref={(el) => { if (el) el.indeterminate = some; }}
                  onChange={() => setSel(all ? [] : allKeys)}
                />
              </th>
            ) : null}
            {columns.map((c) => {
              const dir = sort?.key === c.key ? sort.dir : null;
              return (
                <th key={c.key} scope="col" style={{ width: c.width }} className={cx(c.align === 'end' && 'is-end')} aria-sort={dir === 'asc' ? 'ascending' : dir === 'desc' ? 'descending' : undefined}>
                  {c.sortable ? (
                    <button type="button" className="at-table__sort" onClick={() => setSort(dir === 'asc' ? { key: c.key, dir: 'desc' } : dir === 'desc' ? null : { key: c.key, dir: 'asc' })}>
                      {c.header}
                      <Icon name={dir === 'asc' ? 'arrowUp' : dir === 'desc' ? 'arrowDown' : 'sort'} size={12} />
                    </button>
                  ) : c.header}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {sorted.length === 0 && empty ? (
            <tr><td className="at-table__empty" colSpan={columns.length + (selectable ? 1 : 0)}>{empty}</td></tr>
          ) : sorted.map((r) => {
            const k = rowKey(r);
            const on = sel.includes(k);
            return (
              <tr key={k} className={cx(on && 'is-selected')}>
                {selectable ? (
                  <td className="at-table__check">
                    <input type="checkbox" className="at-checkbox" aria-label={`Select row ${k}`} checked={on} onChange={() => setSel(on ? sel.filter((x) => x !== k) : [...sel, k])} />
                  </td>
                ) : null}
                {columns.map((c, ci) => (
                  <td key={c.key} className={cx(c.align === 'end' && 'is-end', ci === 0 && 'is-primary')}>
                    <div className="at-table__cell">
                      <span>{c.render ? c.render(r) : String(r[c.key] ?? '')}</span>
                      {c.supporting ? <span className="at-table__supporting">{c.supporting(r)}</span> : null}
                    </div>
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
