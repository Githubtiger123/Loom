import { useSyncExternalStore } from 'react';
import { sessionStore } from './api';

export function useSession() {
  return useSyncExternalStore(sessionStore.subscribe, sessionStore.getSnapshot);
}
