import { useState, type ReactNode } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Button } from '@atomus';
import { ZodError } from 'zod';
import { ErrorState } from '@/components/feedback';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { pipelineApi, pipelineKeys } from '../api';

// One key per payload and endpoint; ambiguous failures retain the intent.
export function CommandForm({
  groupId,
  path,
  method = 'POST',
  destination,
  payload,
  disabled,
  label,
  children,
  resultLink,
}: {
  groupId: string;
  path: string;
  method?: 'POST' | 'PUT';
  destination: string;
  payload: () => unknown;
  disabled?: boolean;
  label: string;
  children: ReactNode;
  resultLink?: 'pipeline' | 'run';
}) {
  const navigate = useNavigate();
  const client = useQueryClient();
  const mounted = useMounted();
  const commandKey = useCommandKey();
  const [validation, setValidation] = useState<unknown>();
  const mutation = useMutation({
    retry: false,
    mutationFn: (body: unknown) =>
      pipelineApi.command(path, method, body, commandKey({ path, method, body })),
    onSuccess: (operation) => {
      void client.invalidateQueries({ queryKey: pipelineKeys.all(groupId) });
      if (!mounted.current) return;
      let target = destination;
      const result = operation.result;
      if (resultLink && result && typeof result === 'object') {
        const id = (result as Record<string, unknown>)[resultLink === 'run' ? 'resourceId' : 'id'];
        if (typeof id === 'number' && Number.isSafeInteger(id) && id > 0)
          target = `/workspaces/${groupId}/${resultLink === 'run' ? 'pipeline-runs' : 'pipelines'}/${id}`;
      }
      void navigate(target, { state: { operationId: operation.operationId } });
    },
  });
  return (
    <form
      className="stack"
      onSubmit={(event) => {
        event.preventDefault();
        if (disabled || mutation.isPending || mutation.isSuccess) return;
        setValidation(undefined);
        try {
          mutation.mutate(payload());
        } catch (error) {
          setValidation(
            error instanceof ZodError
              ? new Error(error.issues.map((issue) => issue.message).join('；'))
              : error,
          );
        }
      }}
    >
      <fieldset
        className="pipeline-fieldset stack"
        disabled={mutation.isPending || mutation.isSuccess}
      >
        {children}
      </fieldset>
      {validation !== undefined && <ErrorState error={validation} />}
      {mutation.isError && <ErrorState error={mutation.error} />}
      <p className="muted small">
        提交后显示后台操作进度；受理不代表运行已就绪。发生版本冲突时保留输入，请先核对最新版本。
      </p>
      <div className="actions end">
        <Button
          type="submit"
          hierarchy="primary"
          loading={mutation.isPending}
          disabled={disabled || mutation.isSuccess}
        >
          {label}
        </Button>
      </div>
    </form>
  );
}
