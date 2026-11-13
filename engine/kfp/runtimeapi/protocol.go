// Package runtimeapi defines the workload transport contract, not IR semantics.
package runtimeapi

const (
	Audience        = "loom-pipeline-runtime"
	Protocol        = "loom-kfp-runtime-v1"
	MaxMessageBytes = 1 << 20
)

// FrozenPolicy is committed in the immutable attempt snapshot before submission.
// It deliberately contains no token or storage key. Mutable deployment config
// must not silently change the identity accepted for an existing attempt.
type FrozenPolicy struct {
	Protocol          string `json:"protocol"`
	GroupInstanceID   string `json:"groupInstanceId"`
	ServiceAccount    string `json:"serviceAccount"`
	ServiceAccountUID string `json:"serviceAccountUid"`
	EngineRevision    string `json:"engineRevision"`
	PlanDigest        string `json:"planDigest"`
	PipelineName      string `json:"pipelineName"`
	PipelineID        uint64 `json:"pipelineId"`
	PipelineRoot      string `json:"pipelineRoot"`
	StoreSessionInfo  string `json:"storeSessionInfo"`
	CacheEnabled      bool   `json:"cacheEnabled"`
	DriverImage       string `json:"driverImage"`
	LauncherImage     string `json:"launcherImage"`
}

// Scope is returned by server-side workload authorization. Request bodies must
// never choose or override these IDs. Execution-level rights are narrower still.
type Scope struct {
	GroupID           uint64 `json:"groupId"`
	RunID             uint64 `json:"runId"`
	InstanceID        string `json:"instanceId"`
	Namespace         string `json:"namespace"`
	NamespaceUID      string `json:"namespaceUid"`
	WorkflowName      string `json:"workflowName"`
	WorkflowUID       string `json:"workflowUid"`
	PodName           string `json:"podName"`
	PodUID            string `json:"podUid"`
	ServiceAccount    string `json:"serviceAccount"`
	ServiceAccountUID string `json:"serviceAccountUid"`
	EngineRevision    string `json:"engineRevision"`
	Role              string `json:"role"`
	ExecutionID       int64  `json:"executionId"`
	ParentDAGID       int64  `json:"parentDagId"`
	TaskName          string `json:"taskName"`
	NodeID            string `json:"nodeId"`
	NodeName          string `json:"nodeName"`
	IterationIndex    int    `json:"iterationIndex"`
}
