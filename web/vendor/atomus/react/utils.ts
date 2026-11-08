import { useEffect, useId as useReactId, type RefObject } from "react";

export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ');
}

/** Stable id for label/aria wiring; uses the caller's id when given. */
export function useFieldId(given?: string): string {
  const generated = useReactId();
  return given ?? `at${generated.replace(/:/g, '')}`;
}


/** Calls handler when a pointer goes down outside every given element. */
export function useOutsideClick(refs: Array<RefObject<HTMLElement | null>>, handler: () => void, active = true): void {
  useEffect(() => {
    if (!active) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (refs.every((r) => !r.current || !r.current.contains(t))) handler();
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [active, handler, refs]);
}
