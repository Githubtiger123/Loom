import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button, Card, Input, Table } from '@atomus';
import { ErrorState, NoData } from '@/components/feedback';
import { ApiError } from '@/lib/api';
import { useMounted } from '@/lib/use-mounted';
import { pipelineApi, pipelineKeys } from '../api';
import type { ArtifactFile, ArtifactFiles, Preview, Run } from '../types';
import { QueryState } from './query-state';

type Tree = { path: string; directories: Map<string, Tree> };
function directoryTree(files: ArtifactFile[]): Tree {
  const root: Tree = { path: '', directories: new Map() };
  for (const file of files) {
    let node = root;
    for (const part of file.name.split('/').slice(0, -1)) {
      let child = node.directories.get(part);
      if (!child) {
        child = { path: node.path ? `${node.path}/${part}` : part, directories: new Map() };
        node.directories.set(part, child);
      }
      node = child;
    }
  }
  return root;
}
function TreeBranch({
  node,
  selected,
  onSelect,
}: {
  node: Tree;
  selected: string;
  onSelect: (path: string) => void;
}) {
  const [open, setOpen] = useState(selected === node.path || selected.startsWith(`${node.path}/`));
  return (
    <li>
      <div className="actions">
        {!!node.directories.size && (
          <Button
            hierarchy="link"
            size="sm"
            aria-expanded={open}
            onClick={() => setOpen((v) => !v)}
          >
            {open ? '收起' : '展开'}
          </Button>
        )}
        <button
          type="button"
          className="text-link"
          aria-current={selected === node.path ? 'location' : undefined}
          onClick={() => onSelect(node.path)}
        >
          {node.path.split('/').at(-1)}
        </button>
      </div>
      {open && !!node.directories.size && (
        <ul>
          {[...node.directories.values()].map((child) => (
            <TreeBranch key={child.path} node={child} selected={selected} onSelect={onSelect} />
          ))}
        </ul>
      )}
    </li>
  );
}

export function DirectoryBrowser({
  groupId,
  run,
  artifact,
  renderPreview,
}: {
  groupId: string;
  run: Run;
  artifact: string;
  renderPreview: (preview: Preview) => ReactNode;
}) {
  const [failure, setFailure] = useState<unknown>();
  const query = useQuery({
    queryKey: pipelineKeys.query(
      groupId,
      'artifact-files',
      run.id,
      run.instanceId,
      run.revision,
      artifact,
    ),
    queryFn: async ({ signal }) => {
      const result = await pipelineApi.files(groupId, run, artifact, signal);
      if (
        result.instanceId !== run.instanceId ||
        result.revision !== run.revision ||
        result.artifactId !== artifact
      )
        throw new ApiError(409, 'INSTANCE_CHANGED', '产物所属实例或修订已变化，请刷新运行详情');
      return result;
    },
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  return (
    <Card
      title="文件与目录（只读）"
      supportingText="最多 4096 文件、64 层目录；预览 2 MiB，单文件下载 32 MiB。不上传、编辑或删除文件。"
      headerAction={
        <Button
          hierarchy="outline"
          size="sm"
          loading={query.isFetching}
          onClick={() => {
            void query.refetch().then((result) => {
              if (result.isSuccess) setFailure(undefined);
            });
          }}
        >
          刷新目录
        </Button>
      }
    >
      <QueryState query={query}>
        {(listing) =>
          failure !== undefined ? (
            <ErrorState
              error={failure}
              retry={() => {
                void query.refetch().then((result) => {
                  if (result.isSuccess) setFailure(undefined);
                });
              }}
            />
          ) : (
            <DirectoryContent
              key={listing.manifestDigest}
              groupId={groupId}
              run={run}
              listing={listing}
              renderPreview={renderPreview}
              onFailure={setFailure}
            />
          )
        }
      </QueryState>
    </Card>
  );
}

function DirectoryContent({
  groupId,
  run,
  listing,
  renderPreview,
  onFailure,
}: {
  groupId: string;
  run: Run;
  listing: ArtifactFiles;
  renderPreview: (preview: Preview) => ReactNode;
  onFailure: (error: unknown) => void;
}) {
  const [folder, setFolder] = useState('');
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<ArtifactFile>();
  const root = directoryTree(listing.files);
  const files = listing.files.filter((file) =>
    search.trim()
      ? file.name.toLowerCase().includes(search.trim().toLowerCase())
      : file.name.split('/').slice(0, -1).join('/') === folder,
  );
  const navigate = (path: string) => {
    setFolder(path);
    setSearch('');
    setSelected(undefined);
  };
  const parts = folder ? folder.split('/') : [];
  return (
    <div className="stack">
      <p className="muted small">
        {listing.directory ? '目录产物' : '单文件产物'} · {listing.files.length}{' '}
        个文件。目录来自对象清单；空目录没有对象时不会显示。
      </p>
      <nav className="actions" aria-label="产物路径">
        <Button hierarchy="link" size="sm" onClick={() => navigate('')}>
          根目录
        </Button>
        {parts.map((part, i) => (
          <Button
            key={i}
            hierarchy="link"
            size="sm"
            onClick={() => navigate(parts.slice(0, i + 1).join('/'))}
          >
            / {part}
          </Button>
        ))}
      </nav>
      <Input
        label="搜索全部文件的相对路径"
        value={search}
        onChange={(event) => {
          setSearch(event.target.value);
          setSelected(undefined);
        }}
      />
      <div className="pipeline-file-layout">
        <nav className="pipeline-file-tree" aria-label="产物目录树">
          <Button hierarchy="link" size="sm" onClick={() => navigate('')}>
            根目录
          </Button>
          <ul>
            {[...root.directories.values()].map((node) => (
              <TreeBranch key={node.path} node={node} selected={folder} onSelect={navigate} />
            ))}
          </ul>
        </nav>
        <div className="stack">
          {!search.trim() &&
            [
              ...(folder
                ? (findDirectory(root, parts)?.directories.values() ?? [])
                : root.directories.values()),
            ].map((node) => (
              <Button key={node.path} hierarchy="link" onClick={() => navigate(node.path)}>
                目录：{node.path.split('/').at(-1)} /
              </Button>
            ))}
          <Table
            rows={files}
            rowKey={(file) => file.name}
            caption={search.trim() ? '匹配的文件（全目录）' : '当前目录文件'}
            empty={<NoData title="此处没有文件" description="可选择子目录，或搜索相对路径。" />}
            columns={[
              {
                key: 'name',
                header: '相对路径',
                render: (file) => (
                  <Button hierarchy="link" size="sm" onClick={() => setSelected(file)}>
                    {file.name}
                  </Button>
                ),
              },
              {
                key: 'sizeBytes',
                header: '大小',
                render: (file) => `${file.sizeBytes.toLocaleString()} B`,
              },
            ]}
          />
        </div>
      </div>
      {selected && (
        <FileContent
          key={`${selected.name}/${selected.etag}`}
          groupId={groupId}
          run={run}
          listing={listing}
          file={selected}
          renderPreview={renderPreview}
          onFailure={onFailure}
        />
      )}
    </div>
  );
}
function findDirectory(root: Tree, parts: string[]) {
  let node: Tree | undefined = root;
  for (const part of parts) node = node?.directories.get(part);
  return node;
}

function FileContent({
  groupId,
  run,
  listing,
  file,
  renderPreview,
  onFailure,
}: {
  groupId: string;
  run: Run;
  listing: ArtifactFiles;
  file: ArtifactFile;
  renderPreview: (preview: Preview) => ReactNode;
  onFailure: (error: unknown) => void;
}) {
  const [preview, setPreview] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState<unknown>();
  const mounted = useMounted();
  const controller = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => controller.current?.abort(), []);
  const query = useQuery({
    queryKey: pipelineKeys.query(
      groupId,
      'artifact-file-preview',
      run.id,
      run.instanceId,
      run.revision,
      listing.artifactId,
      listing.manifestDigest,
      file.name,
      file.etag,
    ),
    queryFn: async ({ signal }) => {
      const result = await pipelineApi.filePreview(groupId, run, listing, file, signal);
      if (
        result.instanceId !== run.instanceId ||
        result.revision !== run.revision ||
        result.artifactId !== listing.artifactId ||
        result.preview?.etag !== file.etag
      )
        throw new ApiError(409, 'FILE_CHANGED', '文件身份已变化，请刷新目录');
      return result;
    },
    enabled: preview,
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    if (query.isError) onFailure(query.error);
  }, [query.isError, query.error, onFailure]);
  async function download() {
    if (controller.current) return;
    const current = new AbortController();
    controller.current = current;
    setDownloading(true);
    setError(undefined);
    try {
      const response = await pipelineApi.fileDownload(groupId, run, listing, file, current.signal);
      if (!mounted.current || current.signal.aborted) return;
      const url = URL.createObjectURL(response.blob);
      const anchor = document.createElement('a');
      anchor.href = url;
      anchor.download = response.name;
      anchor.rel = 'noopener';
      document.body.append(anchor);
      anchor.click();
      anchor.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (reason) {
      if (mounted.current && !current.signal.aborted) {
        setPreview(false);
        setError(reason);
        onFailure(reason);
      }
    } finally {
      controller.current = undefined;
      if (mounted.current) setDownloading(false);
    }
  }
  return (
    <section className="stack">
      <h4 className="break-word">{file.name}</h4>
      <div className="actions">
        <Button
          hierarchy="outline"
          size="sm"
          disabled={file.sizeBytes > 2 * 1024 * 1024 || downloading}
          onClick={() => setPreview((value) => !value)}
        >
          {preview ? '收起文件预览' : '预览此文件'}
        </Button>
        <Button
          hierarchy="outline"
          size="sm"
          disabled={file.sizeBytes > 32 * 1024 * 1024}
          loading={downloading}
          onClick={() => {
            void download();
          }}
        >
          下载此文件
        </Button>
      </div>
      <p className="muted small">
        预览支持 UTF-8 文本、JSON、CSV／TSV、Markdown、隔离 HTML 和
        PNG／JPEG／GIF／WebP；其他格式请下载，不执行模型或脚本。超限文件请使用受管存储工具读取。
      </p>
      {error !== undefined && <ErrorState error={error} />}
      {preview && (
        <QueryState query={query}>
          {(result) =>
            result.preview ? renderPreview(result.preview) : <NoData title="没有可预览内容" />
          }
        </QueryState>
      )}
    </section>
  );
}
