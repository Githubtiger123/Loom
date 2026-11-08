import { useRef } from 'react';

// Keep the same key after an ambiguous failure. Editing the payload starts a new
// intent; a successful command closes its form/dialog and discards this key.
export function useCommandKey() {
  const intent = useRef<{ fingerprint: string; key: string } | undefined>(undefined);
  return (payload: unknown) => {
    const fingerprint = JSON.stringify(payload);
    if (!intent.current || intent.current.fingerprint !== fingerprint) {
      intent.current = { fingerprint, key: crypto.randomUUID() };
    }
    return intent.current.key;
  };
}
