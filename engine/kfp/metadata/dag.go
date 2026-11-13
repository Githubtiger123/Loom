// Derived from upstream metadata client. Modified by Loom: inject the client for
// DAG aggregation and separate the unchanged task-indexing loop from RPC paging.
package metadata

import (
	"context"
	"fmt"
	"github.com/golang/glog"
	pb "github.com/kubeflow/pipelines/third_party/ml-metadata/go/ml_metadata"
)

func ReconcileDAG(ctx context.Context, c ClientInterface, dag *DAG, pipeline *Pipeline) error {
	// Prefer runtime iteration_count (set by ParallelFor) over compile-time total_dag_tasks.
	// We must check key presence because iteration_count=0 is valid (empty loop)
	// and distinct from the key being absent (non-iterator DAG).
	customProperties := dag.Execution.GetExecution().GetCustomProperties()
	iterationCountValue, hasIterationCount := customProperties[keyIterationCount]

	var expectedTaskCount int64
	if hasIterationCount {
		expectedTaskCount = iterationCountValue.GetIntValue()
	} else {
		expectedTaskCount = customProperties[keyTotalDagTasks].GetIntValue()
	}

	if expectedTaskCount == 0 && !hasIterationCount {
		glog.V(4).Infof("DAG %d has no expected task count (root DAG or unknown), skipping state update", dag.Execution.GetID())
		return nil
	}

	tasks, err := c.GetExecutionsInDAG(ctx, dag, pipeline, true)
	if err != nil {
		return err
	}

	glog.V(4).Info("runtime payload omitted by Loom policy")
	glog.V(4).Infof("Checking Tasks' State")
	completedTasks := 0
	failedTasks := 0
	for _, task := range tasks {
		taskState := task.GetExecution().LastKnownState.String()
		glog.V(4).Infof("task: %s", task.TaskName())
		glog.V(4).Infof("task state: %s", taskState)
		switch taskState {
		case "FAILED":
			failedTasks++
		case "COMPLETE":
			completedTasks++
		case "CACHED":
			completedTasks++
		case "CANCELED":
			completedTasks++
		}
	}
	glog.V(4).Infof("completedTasks: %d", completedTasks)
	glog.V(4).Infof("failedTasks: %d", failedTasks)
	glog.V(4).Infof("expectedTaskCount: %d", expectedTaskCount)

	glog.Infof("Attempting to update DAG state for execution %d", dag.Execution.GetID())

	var newState pb.Execution_State
	switch {
	case completedTasks == int(expectedTaskCount):
		newState = pb.Execution_COMPLETE
	case failedTasks > 0:
		newState = pb.Execution_FAILED
	default:
		glog.V(4).Infof("DAG is still running")
		return nil
	}

	if err := c.PutDAGExecutionState(ctx, dag.Execution.GetID(), newState); err != nil {
		return err
	}

	// Propagate upward to parent DAG if this is not the root.
	parentDagID := dag.Execution.GetExecution().GetCustomProperties()[keyParentDagID].GetIntValue()
	if parentDagID == 0 {
		glog.V(4).Infof("DAG %d is root or has no parent, stopping propagation", dag.Execution.GetID())
		return nil
	}

	glog.Infof("Propagating state update to parent DAG %d", parentDagID)
	parentDAG, err := c.GetDAG(ctx, parentDagID)
	if err != nil {
		return fmt.Errorf("failed to get parent DAG %d for recursive state update: %w", parentDagID, err)
	}

	return c.UpdateDAGExecutionsState(ctx, parentDAG, pipeline)
}

func IndexDAGExecutions(dag *DAG, execs []*pb.Execution) (map[string]*Execution, error) {
	if dag == nil || dag.Execution == nil {
		return nil, fmt.Errorf("DAG is required")
	}
	parentDAGID := dag.Execution.GetID()
	executionsMap := make(map[string]*Execution)
	for _, e := range execs {
		execution := &Execution{Execution: e}
		glog.V(4).Infof("taskName before DAG injection: %s", execution.TaskName())
		// Sometimes components in nested DAGs have identical task names. We
		// update all task names to include the DAG ID to avoid potential
		// key collisions in the executions map.
		taskName := GetTaskNameWithDagID(execution.TaskName(), parentDAGID)
		glog.V(4).Infof("taskName after DAG Injection: %s", taskName)
		glog.V(4).Info("runtime payload omitted by Loom policy")
		if taskName == "" {
			if e.GetCustomProperties()[keyParentDagID] != nil {
				return nil, fmt.Errorf("empty task name for execution ID: %v", execution.GetID())
			}
			// When retrieving executions without the parentDAGFilter, the
			// rootDAG execution is supplied but does not have an associated
			// TaskName nor is the parentDagID set, therefore we won't
			// include it in the executionsMap.
			continue
		}
		// Handle for parallelFor subdags & their tasks that consume the
		// values from the iterator. Within a ParallelFor DAG, the iteration
		// DAGs share the same name. In order to avoid collisions in the
		// taskMap, the iteration index will be appended to the taskName.
		// This also fortifies against potential collisions of tasks across
		// iterations.
		if e.GetCustomProperties()[keyIterationIndex] != nil {
			taskName = GetParallelForTaskName(taskName, e.GetCustomProperties()[keyIterationIndex].GetIntValue())

		} else if dag.Execution.GetExecution().GetCustomProperties()[keyIterationIndex] != nil {
			// Handle for tasks within a parallelFor subdag that do not
			// consume the values from the iterator as input but rather the
			// output of a task that does.
			taskName = GetParallelForTaskName(taskName, dag.Execution.GetExecution().GetCustomProperties()[keyIterationIndex].GetIntValue())
		}

		existing, ok := executionsMap[taskName]
		if ok {
			// TODO: The failure to handle this results in a specific edge
			// case which has yet to be solved for. If you have three nested
			// pipelines: A, which calls B, which calls C, and B and C share
			// a task that A does not have but depends on in a producer
			// subtask, when GetExecutionsInDAG is called, it will raise
			// this error.

			// TODO(Bobgy): to support retry, we need to handle multiple tasks with the same task name.
			return nil, fmt.Errorf("two tasks have the same task name %q, id1=%v id2=%v", taskName, existing.GetID(), execution.GetID())
		}
		executionsMap[taskName] = execution
	}

	return executionsMap, nil
}
