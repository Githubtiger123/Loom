import { useState, type ReactNode } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Button } from '@atomus';
import { ZodError } from 'zod';
import type { Operation } from '@/lib/types';
import { ErrorState } from '@/components/feedback';
import { useCommandKey } from '@/lib/use-command-key';
import { useMounted } from '@/lib/use-mounted';
import { registryApi } from '../api';

export function CommandForm({
  groupId,
  path,
  method = 'POST',
  payload,
  destination,
  label,
  disabled,
  children,
  onAccepted,
}: {
  groupId: string;
  path: string;
  method?: string;
  payload: () => unknown;
  destination: string | ((operation: Operation) => string);
  label: string;
  disabled?: boolean;
  children: ReactNode;
  onAccepted?: () => void;
}) {
  const navigate = useNavigate(),
    client = useQueryClient(),
    key = useCommandKey(),
    mounted = useMounted();
  const [validation, setValidation] = useState<unknown>();
  const mutation = useMutation({
    retry: false,
    mutationFn: (body: unknown) =>
      registryApi.command(path, method, body, key({ path, method, body })),
    onSuccess: (operation) => {
      void client.invalidateQueries({ queryKey: ['resources', groupId] });
      if (!mounted.current) return;
      onAccepted?.();
      void navigate(typeof destination === 'function' ? destination(operation) : destination, {
        state: { operationId: operation.operationId },
      });
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
              ? new Error(error.issues.map((i) => i.message).join('；'))
              : error,
          );
        }
      }}
    >
      <fieldset
        className="model-fieldset stack"
        disabled={mutation.isPending || mutation.isSuccess || disabled}
      >
        {children}
      </fieldset>
      {validation !== undefined && <ErrorState error={validation} />}
      {mutation.isError && <ErrorState error={mutation.error} />}
      <p className="muted small">
        提交受理不等于模型可加载或服务就绪。发生 409 时保留输入，请先核对最新修订，不会自动覆盖。
      </p>
      <div className="actions end">
        <Button
          type="submit"
          hierarchy="primary"
          disabled={disabled || mutation.isSuccess}
          loading={mutation.isPending}
        >
          {label}
        </Button>
      </div>
    </form>
  );
}
