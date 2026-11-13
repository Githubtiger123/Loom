export type Identity = {
  id: number;
  groupId: number;
  name: string;
  createdBy: number | null;
  createdAt: string;
  updatedAt: string;
  deletedAt: string | null;
};
export type Pipeline = Identity & { displayName: string; description: string; revision: number };
export type ParameterSchema = { name: string; type: string; required: boolean; default?: string };
export type ParameterValue = { name: string; value?: string; credentialRef?: { ref: string } };
export type VersionSpec = {
  packageRef: string;
  packageDigest: string;
  parameters: ParameterSchema[];
  pipelineSpec?: Record<string, unknown>;
};
export type Version = {
  id: number;
  pipelineId: number;
  name: string;
  revision: number;
  spec: VersionSpec;
  specHash: string;
  createdAt: string;
};
export type Run = Identity & {
  instanceId: string;
  pipelineId: number;
  versionId: number;
  versionRevision?: number;
  engine: string;
  revision: number;
  status: string;
  statusReason: string | null;
  desiredState: string;
  observedState: string;
  observedAt: string | null;
  namespace: string;
  cluster: string;
  executionCleaned: boolean;
  operationId: string | null;
  spec: VersionSpec & { values: ParameterValue[]; versionRevision?: number };
};
export type Compatibility = {
  versionId: number;
  revision: number;
  packageDigest: string;
  compilerVersion: string;
  compilable: boolean;
  runnable: boolean;
  executionReason: string;
  supportedFeatures: string[];
  issues: { code: string; path: string }[];
  issuesTruncated: boolean;
  taskCount: number;
  dependencyCount: number;
};
export type Definition = {
  nodeId: string;
  parentNodeId?: string;
  taskName: string;
  displayName: string;
  kind: string;
  component: string;
  triggerPolicy?: unknown;
  retryPolicy?: unknown;
  cachingOptions?: unknown;
  parameterIterator?: unknown;
};
export type NodeInstance = {
  retries?: {
    epoch: number;
    podName: string;
    podUID: string;
    state: string;
    publicationReplayed: boolean;
  }[];
  nodeId: string;
  definitionNodeId?: string;
  mapping: string;
  parentNodeId?: string;
  taskId?: string;
  name: string;
  state: string;
  podName?: string;
  iterationIndex?: unknown;
  startTime?: string;
  endTime?: string;
  cachedExecutionId?: string;
  error?: unknown;
};
export type DataSource = { status: string; observedAt?: string };
export type Edge = {
  scopeNodeId: string;
  sourceNodeId: string;
  targetNodeId: string;
  kind: string;
  sourcePort?: string;
  targetPort?: string;
  boundary?: string;
};
export type DAG = {
  instanceId: string;
  revision: number;
  state: string;
  readAt: string;
  runtime: DataSource;
  metadata: DataSource;
  definitions: Definition[];
  edges: Edge[];
  instances: NodeInstance[];
};
export type NodeIO = {
  parametersStatus: string;
  parameters: Record<string, { available: boolean; value: unknown; source: string }>;
  artifactsStatus: string;
  artifacts: Record<string, { artifactId: string; status: string; metadata?: unknown }[]>;
};
export type NodeDetail = {
  instanceId: string;
  revision: number;
  readAt: string;
  definition: Definition | null;
  instance: NodeInstance | null;
  inputs: NodeIO;
  outputs: NodeIO;
};
export type View = {
  kind: string;
  status: string;
  slice?: string;
  data?: unknown;
  message?: string;
};
export type Visualization = {
  artifactId: string;
  name: string;
  type: string;
  state: string;
  nodeIds: string[];
  views: View[];
  contentStatus: string;
};
export type Visualizations = { instanceId: string; revision: number; items: Visualization[] };
export type Preview = {
  kind: string;
  name: string;
  etag: string;
  mediaType: string;
  encoding?: string;
  text?: string;
  data?: unknown;
};
export type ArtifactResult = {
  instanceId: string;
  revision: number;
  artifactId: string;
  preview?: Preview;
};
export type LiveLog = {
  pod: string;
  podUID: string;
  container: string;
  text: string;
  truncated: boolean;
  observedAt: string;
};
export type Attempt = {
  instanceId: string;
  revision: number;
  createdAt: string;
  phase?: string;
  reason?: string;
};
export type ArchiveEntry = {
  id: string;
  taskId: string;
  pod: string;
  container: string;
  sizeBytes: number;
  etag: string;
};
export type Manifest = {
  instanceId: string;
  complete: boolean;
  source: string;
  entries: ArchiveEntry[];
};
export type LogChunk = {
  instanceId: string;
  entryId: string;
  etag: string;
  offset: number;
  nextOffset: number;
  eof: boolean;
  sizeBytes: number;
  text: string;
};
