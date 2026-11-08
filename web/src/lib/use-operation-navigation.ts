import { useEffect, useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';

export function useOperationNavigation() {
  const location = useLocation();
  const navigate = useNavigate();
  const state = location.state as { operationId?: unknown } | null;
  const incoming =
    typeof state?.operationId === 'string' && /^[a-f0-9-]{36}$/i.test(state.operationId)
      ? state.operationId
      : undefined;
  const [operation, setOperation] = useState<string | undefined>(incoming);
  useEffect(() => {
    if (incoming) {
      setOperation(incoming);
      void navigate(
        { pathname: location.pathname, search: location.search },
        { replace: true, state: null },
      );
    }
  }, [incoming, navigate, location.pathname, location.search]);
  return { operation, setOperation };
}
