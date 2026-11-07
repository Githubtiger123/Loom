package kfp

import "encoding/json"

// RuntimeError keeps the useful status, not arbitrary google.rpc.Any bodies.
type RuntimeError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type RuntimeStatus struct {
	State string        `json:"state"`
	Time  string        `json:"update_time"`
	Error *RuntimeError `json:"error,omitempty"`
}
type ArtifactIDs struct {
	IDs []json.Number `json:"artifact_ids"`
}
type ChildTask struct {
	ID      string `json:"task_id"`
	PodName string `json:"pod_name"`
}
type ExecutorDetail struct {
	MainJob                   string   `json:"main_job"`
	PreCachingCheckJob        string   `json:"pre_caching_check_job"`
	FailedMainJobs            []string `json:"failed_main_jobs"`
	FailedPreCachingCheckJobs []string `json:"failed_pre_caching_check_jobs"`
}
type TaskDetail struct {
	RunID       string                 `json:"run_id"`
	ID          string                 `json:"task_id"`
	Name        string                 `json:"display_name"`
	PodName     string                 `json:"pod_name"`
	ExecutionID json.Number            `json:"execution_id"`
	State       string                 `json:"state"`
	ParentID    string                 `json:"parent_task_id"`
	CreateTime  string                 `json:"create_time"`
	StartTime   string                 `json:"start_time"`
	EndTime     string                 `json:"end_time"`
	Error       *RuntimeError          `json:"error,omitempty"`
	History     []RuntimeStatus        `json:"state_history"`
	Inputs      map[string]ArtifactIDs `json:"inputs"`
	Outputs     map[string]ArtifactIDs `json:"outputs"`
	Children    []ChildTask            `json:"child_tasks"`
	Executor    *ExecutorDetail        `json:"executor_detail,omitempty"`
}
type RunDetails struct {
	ContextID json.Number  `json:"pipeline_run_context_id"`
	Tasks     []TaskDetail `json:"task_details"`
}
