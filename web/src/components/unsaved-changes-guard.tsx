import { useEffect } from 'react';
import { useBlocker } from 'react-router-dom';
import { Button, Modal } from '@atomus';

export function UnsavedChangesGuard({
  when,
  watchSearch = false,
}: {
  when: boolean;
  watchSearch?: boolean;
}) {
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
  if (blocker.state !== 'blocked') return null;
  return (
    <Modal
      open
      title="离开未保存的表单？"
      description="当前修改尚未提交，离开后需要重新填写。"
      onClose={() => blocker.reset()}
      actions={
        <>
          <Button hierarchy="outline" onClick={() => blocker.reset()}>
            继续编辑
          </Button>
          <Button hierarchy="primary" onClick={() => blocker.proceed()}>
            放弃修改并离开
          </Button>
        </>
      }
    />
  );
}
