import { useEffect } from 'react';
import { useBlocker } from 'react-router-dom';

// The caller chooses inline confirmation or a standalone dialog. Keeping the
// blocker separate avoids opening a second modal over an existing form modal.
export function useUnsavedChangesBlocker(when: boolean, watchSearch = false) {
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      when &&
      (currentLocation.pathname !== nextLocation.pathname ||
        (watchSearch && currentLocation.search !== nextLocation.search)),
  );
  useEffect(() => {
    if (!when) return;
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', beforeUnload);
    return () => window.removeEventListener('beforeunload', beforeUnload);
  }, [when]);
  return blocker;
}
