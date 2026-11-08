import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Input, Modal } from '@atomus';
import type { Group } from '@/lib/types';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { ErrorState } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { UnsavedChangesGuard } from '@/components/unsaved-changes-guard';
import { groupApi } from '../api';
import { refreshGroup } from '../cache';
import { metadataSchema, type MetadataValues } from '../form-schema';

export function MetadataForm({
  groupId,
  group,
  allowed,
  onCancel,
  onAccepted,
}: {
  groupId: string;
  group: Group;
  allowed: boolean;
  onCancel: () => void;
  onAccepted: (id: string) => void;
}) {
  const [baseline] = useState(() => ({
    displayName: group.displayName,
    description: group.description,
  }));
  const [discard, setDiscard] = useState(false);
  const form = useForm<MetadataValues>({
    resolver: zodResolver(metadataSchema),
    defaultValues: baseline,
  });
  const key = useCommandKey();
  const mounted = useMounted();
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (v: MetadataValues) => {
      if (!allowed) throw new Error('当前没有编辑权限');
      const body = {
        expected: baseline,
        ...(v.displayName !== baseline.displayName ? { displayName: v.displayName } : {}),
        ...(v.description !== baseline.description ? { description: v.description } : {}),
      };
      return groupApi.metadata(groupId, body, key(body));
    },
    onSuccess: (op) => {
      refreshGroup(client, groupId);
      if (mounted.current) onAccepted(op.operationId);
    },
  });
  const changed =
    form.watch('displayName') !== baseline.displayName ||
    form.watch('description') !== baseline.description;
  const stale =
    group.displayName !== baseline.displayName || group.description !== baseline.description;
  return (
    <>
      <form
        className="stack"
        onSubmit={form.handleSubmit((v) => {
          if (!mutation.isPending && !mutation.isSuccess && changed) mutation.mutate(v);
        })}
      >
        <UnsavedChangesGuard
          when={form.formState.isDirty && !mutation.isPending && !mutation.isSuccess}
        />
        {stale && (
          <Alert color="warning" title="服务器信息已变化">
            你的草稿仍保留；请记录草稿，取消后重新编辑。提交时后端会再次校验旧值。
          </Alert>
        )}
        <fieldset className="form-section" disabled={mutation.isPending}>
          <Input
            label="展示名称"
            {...form.register('displayName')}
            error={form.formState.errors.displayName?.message}
          />
          <FormTextarea
            label="描述"
            {...form.register('description')}
            error={form.formState.errors.description?.message}
          />
        </fieldset>
        {mutation.isError && <ErrorState error={mutation.error} />}
        <div className="actions end">
          <Button
            hierarchy="outline"
            disabled={mutation.isPending}
            onClick={() => {
              if (form.formState.isDirty) setDiscard(true);
              else onCancel();
            }}
          >
            取消
          </Button>
          <Button
            hierarchy="primary"
            type="submit"
            loading={mutation.isPending}
            disabled={!allowed || !changed}
          >
            保存信息
          </Button>
        </div>
      </form>
      {discard && (
        <Modal
          open
          title="放弃空间信息修改？"
          description="尚未保存的名称和描述将丢失。"
          onClose={() => setDiscard(false)}
          actions={
            <>
              <Button hierarchy="outline" onClick={() => setDiscard(false)}>
                继续编辑
              </Button>
              <Button hierarchy="primary" onClick={onCancel}>
                放弃修改
              </Button>
            </>
          }
        />
      )}
    </>
  );
}
