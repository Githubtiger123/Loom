import { useRef, useState } from 'react';
import { Input } from '@atomus';
import { ErrorState } from '@/components/feedback';
import { FormTextarea } from '@/components/form-textarea';
import { useMounted } from '@/lib/use-mounted';

// Shared by version import/edit. File reads cannot overwrite later manual edits.
export function IRInput({
  value,
  onChange,
  onBlocked,
}: {
  value: string;
  onChange: (value: string) => void;
  onBlocked: (blocked: boolean) => void;
}) {
  const generation = useRef(0);
  const mounted = useMounted();
  const [error, setError] = useState<unknown>();
  return (
    <>
      <Input
        label="选择 IR 文件（最多 512 KiB）"
        type="file"
        accept=".yaml,.yml,.json"
        onChange={(event) => {
          const file = event.target.files?.[0];
          const current = ++generation.current;
          setError(undefined);
          if (!file) {
            onBlocked(false);
            return;
          }
          if (file.size > 512 * 1024) {
            setError(new Error('文件不能超过 512 KiB'));
            onBlocked(true);
            return;
          }
          onBlocked(true);
          void file
            .text()
            .then((text) => {
              if (mounted.current && current === generation.current) {
                onChange(text);
                onBlocked(false);
              }
            })
            .catch((reason: unknown) => {
              if (mounted.current && current === generation.current) setError(reason);
            });
        }}
      />
      <FormTextarea
        label="Pipeline IR YAML / JSON"
        rows={20}
        value={value}
        onChange={(event) => {
          generation.current++;
          setError(undefined);
          onBlocked(false);
          onChange(event.target.value);
        }}
        required
        spellCheck={false}
        hint="官方 Python SDK 编译结果；不是 Python 源码，也不是 Argo Workflow YAML。"
      />
      {error !== undefined && <ErrorState error={error} />}
    </>
  );
}
