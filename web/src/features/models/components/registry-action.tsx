import { Modal } from '@atomus';
import { CommandForm } from './command-form';
export type RegistryAction = {
  title: string;
  description: string;
  path: string;
  body: unknown;
  destination: string;
  destructive?: boolean;
  method?: string;
};
export function RegistryActionDialog({
  groupId,
  action,
  close,
}: {
  groupId: string;
  action: RegistryAction;
  close: () => void;
}) {
  return (
    <Modal
      open
      onClose={close}
      title={action.title}
      type={action.destructive ? 'destructive' : 'default'}
      description={action.description}
    >
      <CommandForm
        groupId={groupId}
        path={action.path}
        method={action.method}
        payload={() => action.body}
        destination={action.destination}
        label={action.title}
        onAccepted={close}
      >
        <p>请确认本次操作。服务状态以后台观测为准；归档不会删除存储文件，也不会停止已发布服务。</p>
      </CommandForm>
    </Modal>
  );
}
