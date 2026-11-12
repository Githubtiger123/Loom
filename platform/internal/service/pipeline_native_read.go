package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/githubtiger123/loom/platform/internal/infra/kfp"
	"github.com/githubtiger123/loom/platform/internal/infra/mlmd"
	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/model"
	"gorm.io/gorm"
)

func nativeTimeString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// Reuses frontend-neutral DAG/IO DTOs and metric parsers only, never KFP or
// MLMD network/database services. All records are scoped by group/run/instance.
func (s *Service) readNativePipelineDAG(ctx context.Context, out *pipelineDAGRead) (*pipelineDAGRead, error) {
	n := out.run
	if n.ExecutionCleaned && n.Engine != model.PipelineEngineNativeArgo {
		out.dag.Runtime.Status = "CLEANED"
		out.dag.Metadata.Status = "CLEANED"
		return out, nil
	}
	a, err := s.Store.PipelineRunAttempt(ctx, n.GroupID, n.ID, n.InstanceID, false)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	j := &model.PipelineSubmission{}
	if err := s.DB.WithContext(ctx).Where("instance_id = ?", n.InstanceID).First(j).Error; err != nil {
		return nil, err
	}
	frozen, err := restoreNativeStaticNodes(a, j)
	if err != nil {
		return nil, err
	}
	rows := []model.PipelineTaskExecution{}
	if err := s.DB.WithContext(ctx).Where("group_id = ? AND run_id = ? AND instance_id = ?", n.GroupID, n.ID, n.InstanceID).Order("id").Limit(k8s.MaxNativeWorkflowNodes + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	advanced := frozen.Plan.Compiler() == pipelineir.AdvancedCompilerVersion
	if !advanced && len(rows) != len(frozen.Plan.Tasks())+1 || len(rows) > k8s.MaxNativeWorkflowNodes {
		return nil, nativeFinalConflict()
	}
	byKey, byTask := map[string]model.PipelineTaskExecution{}, map[string]model.PipelineTaskExecution{}
	for _, row := range rows {
		byKey[row.NodeKey] = row
	}
	for _, task := range frozen.Plan.Tasks() {
		byTask[task.TaskName] = byKey[task.NodeKey]
	}
	ports := map[uint64]map[string]uint64{}
	values := map[uint64]map[string]any{}
	out.metadata = &mlmd.Snapshot{ContextID: n.InstanceID, Executions: []mlmd.Execution{}, Events: []mlmd.Event{}, Artifacts: map[string]mlmd.Artifact{}}
	for _, row := range rows {
		var io struct {
			Parameters map[string]any `json:"parameters"`
			Artifacts  map[string]struct {
				ID uint64 `json:"id"`
			} `json:"artifacts"`
		}
		decoder := json.NewDecoder(bytes.NewReader(row.Outputs))
		decoder.UseNumber()
		if decoder.Decode(&io) != nil {
			return nil, nativeFinalConflict()
		}
		values[row.ID] = io.Parameters
		ports[row.ID] = map[string]uint64{}
		for name, artifact := range io.Artifacts {
			ports[row.ID][name] = artifact.ID
		}
	}
	rootValues, err := pipelineRuntimeParameters(out.spec)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		definitionID := "root"
		displayName := "root"
		parent := ""
		parameters := rootValues
		if row.NodeKey != frozen.Plan.Root().NodeKey {
			var task pipelineir.Task
			var state struct {
				DefinitionKey string `json:"definitionKey"`
			}
			if advanced && json.Unmarshal(row.RuntimeState, &state) != nil {
				return nil, nativeFinalConflict()
			}
			for _, t := range frozen.Plan.Tasks() {
				if t.NodeKey == row.NodeKey || advanced && t.NodeKey == state.DefinitionKey {
					task = t
					break
				}
			}
			if task.TaskName == "" {
				return nil, nativeFinalConflict()
			}
			// Flat-v1 mapping uses the immutable original IR key, not the
			// generated Argo display name or a hash guessed from runtime IDs.
			irName := strings.TrimPrefix(task.DefinitionPath, "/root/dag/tasks/")
			irName = strings.ReplaceAll(strings.ReplaceAll(irName, "~1", "/"), "~0", "~")
			definitionID = out.graph.children["root"][irName]
			if advanced {
				definitionID = "root"
				for _, name := range task.DefinitionNames {
					definitionID = out.graph.children[definitionID][name]
				}
			}
			definition := out.graph.byID[definitionID]
			if definition == nil {
				return nil, nativeFinalConflict()
			}
			displayName = definition.DisplayName
			rootRow := byKey[frozen.Plan.Root().NodeKey]
			parent = "execution-" + fmt.Sprint(rootRow.ID)
			if advanced && row.ParentID != nil {
				parent = "execution-" + fmt.Sprint(*row.ParentID)
			}
			parameters = map[string]any{}
			if task.Runtime != nil && !advanced {
				for name, b := range task.Runtime.Parameters {
					if b.Root != "" {
						parameters[name] = rootValues[b.Root]
					} else if b.Task != "" {
						if v, ok := values[byTask[b.Task].ID][b.Output]; ok {
							parameters[name] = v
						}
					} else {
						var v any
						if json.Unmarshal(b.Value, &v) == nil {
							parameters[name] = v
						}
					}
				}
				for name, b := range task.Runtime.Artifacts {
					if id := ports[byTask[b.Task].ID][b.Output]; id != 0 {
						out.metadata.Events = append(out.metadata.Events, mlmd.Event{ExecutionID: fmt.Sprint(row.ID), ArtifactID: fmt.Sprint(id), Type: 3, Path: []any{name}})
					}
				}
			}
			if advanced {
				var input struct {
					Parameters map[string]any `json:"parameters"`
					Artifacts  map[string]struct {
						ID uint64 `json:"id"`
					} `json:"artifacts"`
				}
				decoder := json.NewDecoder(bytes.NewReader(row.Inputs))
				decoder.UseNumber()
				if decoder.Decode(&input) != nil {
					return nil, nativeFinalConflict()
				}
				parameters = input.Parameters
				for name, artifact := range input.Artifacts {
					out.metadata.Events = append(out.metadata.Events, mlmd.Event{ExecutionID: fmt.Sprint(row.ID), ArtifactID: fmt.Sprint(artifact.ID), Type: 3, Path: []any{name}})
				}
			}
		}
		id := fmt.Sprint(row.ID)
		e := mlmd.Execution{ID: id, Name: displayName, State: row.Phase, Custom: map[string]any{"inputs": parameters, "outputs": values[row.ID]}}
		out.metadata.Executions = append(out.metadata.Executions, e)
		copy := e
		out.executions[id] = &copy
		pod := ""
		if row.PodName != nil {
			pod = *row.PodName
		}
		out.dag.Instances = append(out.dag.Instances, PipelineNodeInstance{ID: "execution-" + id, DefinitionID: definitionID, Mapping: "EXACT", ParentID: parent, TaskID: id, ExecutionID: id, Name: displayName, State: row.Phase, PodName: pod,
			StartTime: nativeTimeString(row.StartedAt), EndTime: nativeTimeString(row.FinishedAt), History: []kfp.RuntimeStatus{}, Children: []kfp.ChildTask{}})
		if row.CacheSourceTaskID != nil {
			out.dag.Instances[len(out.dag.Instances)-1].State = "CACHED"
			out.dag.Instances[len(out.dag.Instances)-1].CachedExecutionID = fmt.Sprint(*row.CacheSourceTaskID)
		}
		if advanced {
			var iterations []int
			_ = json.Unmarshal(row.IterationPath, &iterations)
			if len(iterations) > 0 {
				out.dag.Instances[len(out.dag.Instances)-1].IterationIndex = iterations[len(iterations)-1]
			}
		}
		attempts, err := nativeRetryAttempts(row.RuntimeState)
		if err != nil {
			return nil, err
		}
		for _, attempt := range attempts {
			attemptID := "execution-" + id + "-attempt-" + jsonIndex(attempt.Index)
			out.dag.Instances = append(out.dag.Instances, PipelineNodeInstance{ID: attemptID, DefinitionID: definitionID, Mapping: "EXACT", ParentID: "execution-" + id, TaskID: attemptID, Name: displayName + " (attempt " + jsonIndex(attempt.Index) + ")", State: attempt.Phase, PodName: attempt.PodName, StartTime: nativeTimeString(attempt.StartedAt), EndTime: nativeTimeString(attempt.FinishedAt), History: []kfp.RuntimeStatus{}, Children: []kfp.ChildTask{}})
		}
		for name, artifact := range ports[row.ID] {
			out.metadata.Events = append(out.metadata.Events, mlmd.Event{ExecutionID: id, ArtifactID: fmt.Sprint(artifact), Type: 4, Path: []any{name}})
		}
	}
	artifacts := []model.PipelineArtifact{}
	artifactIDs := []uint64{}
	seenArtifacts := map[uint64]bool{}
	for _, outputs := range ports {
		for _, id := range outputs {
			if !seenArtifacts[id] {
				artifactIDs = append(artifactIDs, id)
				seenArtifacts[id] = true
			}
		}
	}
	if err := s.DB.WithContext(ctx).Where("group_id = ? AND (producer_instance_id = ? OR id IN ?) AND state = ?", n.GroupID, n.InstanceID, artifactIDs, "PUBLISHED").Order("id").Limit(k8s.MaxNativeWorkflowNodes*32 + 1).Find(&artifacts).Error; err != nil {
		return nil, err
	}
	if len(artifacts) > k8s.MaxNativeWorkflowNodes*32 {
		return nil, nativeFinalConflict()
	}
	for _, artifact := range artifacts {
		metadata := map[string]any{}
		if json.Unmarshal(artifact.Metadata, &metadata) != nil {
			return nil, nativeFinalConflict()
		}
		id := fmt.Sprint(artifact.ID)
		uri := ""
		if artifact.URI != nil {
			uri = *artifact.URI
		}
		var provenance struct {
			Port string `json:"port"`
		}
		_ = json.Unmarshal(artifact.Provenance, &provenance)
		out.metadata.Artifacts[id] = mlmd.Artifact{ID: id, Name: provenance.Port, Type: artifact.TypeName, TypeVersion: artifact.TypeSchemaVersion, StorageURI: uri, State: artifact.State, Properties: map[string]any{}, Custom: metadata}
	}
	out.dag.Runtime = PipelineDataSource{Status: "AVAILABLE", ObservedAt: a.ObservedAt}
	out.dag.Metadata = PipelineDataSource{Status: "AVAILABLE", ObservedAt: a.ObservedAt}
	return out, nil
}

func (s *Service) nativePipelineRuntime(ctx context.Context, actor uint64, n *model.PipelineRun) (*PipelineRuntime, error) {
	d, err := s.readPipelineDAG(ctx, actor, n.GroupID, n.ID, "")
	if err != nil {
		return nil, err
	}
	out := &PipelineRuntime{State: d.dag.State, Tasks: []PipelineRuntimeTask{}}
	if n.ExternalID != nil {
		out.ExternalID = *n.ExternalID
	}
	for _, node := range d.dag.Instances {
		out.Tasks = append(out.Tasks, PipelineRuntimeTask{ID: node.ID, Name: node.Name, PodName: node.PodName, ExecutionID: node.ExecutionID, State: node.State, ParentID: node.ParentID, StartTime: node.StartTime, EndTime: node.EndTime})
	}
	if err := s.checkPipelineDAGRead(ctx, actor, n); err != nil {
		return nil, err
	}
	return out, nil
}
