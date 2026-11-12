export type Model = {
  id: number;
  groupId: number;
  name: string;
  displayName: string;
  description: string;
  createdBy: number | null;
  revision: number;
  status: string;
  createdAt: string;
  updatedAt: string;
  deletedAt: string | null;
};
export type Version = {
  id: number;
  groupId: number;
  modelId: number;
  name: string;
  description: string;
  sourcePipelineRunId: number | null;
  sourcePipelineInstanceId: string | null;
  sourcePipelineArtifactId: number | null;
  importOperationId: string | null;
  createdBy: number | null;
  revision: number;
  status: string;
  createdAt: string;
  updatedAt: string;
  deletedAt: string | null;
};
export type Artifact = {
  id: number;
  groupId: number;
  modelId: number;
  versionId: number;
  name: string;
  uri: string;
  modelFormat: string;
  modelFormatVersion: string;
  digest: string;
  sizeBytes: number | null;
  storageType: string;
  createdBy: number | null;
  revision: number;
  status: string;
  createdAt: string;
  deletedAt: string | null;
};
export type ServingSpec = {
  deploymentType: 'MODEL' | 'CUSTOM';
  protocol: 'v1' | 'v2';
  minReplicas: number;
  maxReplicas: number;
  resources: { cpu: string; memory: string; gpu: number };
  model?: {
    artifactId?: number;
    runtime?: string;
    registeredModelId?: number;
    modelVersionId?: number;
    storageUri?: string;
    modelFormat?: string;
    modelFormatVersion?: string;
  };
  custom?: { image: string; command?: string[]; args?: string[]; port: number; healthPath: string };
};
export type ServingService = {
  id: number;
  groupId: number;
  name: string;
  createdBy: number | null;
  revision: number;
  instanceId: string;
  namespace: string;
  externalId: string | null;
  appliedRevision: number;
  configRevisionId: number;
  desiredState: string;
  observedState: string;
  status: string;
  statusReason: string | null;
  operationId: string | null;
  spec: ServingSpec;
  kserveReady: boolean;
  kserveUrl?: string | null;
  kserveAddress?: string | null;
  kserveReason?: string | null;
  kserveGeneration: number;
  kserveObservedGeneration: number;
  kserveSyncedAt?: string | null;
  kserveSyncAttemptedAt?: string | null;
  createdAt: string;
  deletedAt: string | null;
};
export type ServingRevision = {
  id: number;
  revision: number;
  spec: ServingSpec;
  createdAt: string;
};
export type ServingHistory = {
  id: number;
  revision: number;
  event: string;
  action: string;
  desiredState: string;
  observedState: string;
  status: string;
  operationId: string;
  createdAt: string;
};
export type Prediction = {
  revision: number;
  instanceId: string;
  durationMs: number;
  response: unknown;
};
export type ServingRequest = Omit<ServingSpec, 'model'> & {
  name: string;
  model?: Pick<
    NonNullable<ServingSpec['model']>,
    'artifactId' | 'runtime' | 'storageUri' | 'modelFormat' | 'modelFormatVersion'
  >;
};
export type ServingRuntime = {
  name: string;
  scope: 'NAMESPACE' | 'CLUSTER';
  disabled: boolean;
  multiModel: boolean;
  multiNode: boolean;
  protocols: string[];
  formats: { name: string; version?: string; autoSelect: boolean }[];
};
export type ServingRuntimeList = {
  namespace: string;
  items: ServingRuntime[];
  observedAt: string;
};
export type ServingPreflight = {
  ready: boolean;
  checks: { code: string; passed: boolean; message: string }[];
  runtimes: ServingRuntime[];
  storageAccess: string;
  inferenceValidated: boolean;
  observedAt: string;
};
export type ServingContainer = {
  name: string;
  init: boolean;
  ready: boolean;
  state: string;
  reason?: string;
  restarts: number;
  exitCode?: number;
};
export type ServingPod = {
  name: string;
  uid: string;
  phase: string;
  ready: boolean;
  reason?: string;
  containers: ServingContainer[];
};
export type ServingDiagnostics = {
  serviceId: number;
  instanceId: string;
  revision: number;
  appliedRevision: number;
  uid: string;
  state: string;
  ready: boolean;
  reason?: string;
  pods: ServingPod[];
  events: {
    kind: string;
    name: string;
    uid: string;
    type: string;
    reason: string;
    message: string;
    count: number;
    at: string;
  }[];
  truncated: boolean;
  observedAt: string;
};
export type ServingLogQuery = {
  instanceId: string;
  revision: number;
  pod: string;
  podUID: string;
  container: string;
  tailLines: number;
  limitBytes: number;
  sinceSeconds: number;
  previous: boolean;
};
export type ServingLogs = {
  pod: string;
  podUID: string;
  container: string;
  text: string;
  truncated: boolean;
  observedAt: string;
};
