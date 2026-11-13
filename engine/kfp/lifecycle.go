package kfpengine

import (
	"encoding/json"
	"fmt"

	wfapi "github.com/argoproj/argo-workflows/v4/pkg/apis/workflow/v1alpha1"
)

// VerifyFrozenWorkflow checks the final compiler+transport plan. Only the two
// lifecycle controls owned by Loom may differ; arbitrary changes to templates,
// images, volumes, SA, arguments or root cannot hide behind a copied hash label.
// Controller admission/default patches must be frozen before enabling a backend.
func VerifyFrozenWorkflow(plan, observed *wfapi.Workflow) error {
	if plan == nil || observed == nil || plan.Name == "" || plan.Namespace == "" || plan.Name != observed.Name || plan.Namespace != observed.Namespace || len(plan.Spec.Templates) == 0 {
		return fmt.Errorf("frozen Workflow identity is required")
	}
	for _, key := range []string{"app.kubernetes.io/managed-by", "loom.ml/group-id", "loom.ml/pipeline-run-id"} {
		if plan.Labels[key] == "" || plan.Labels[key] != observed.Labels[key] {
			return fmt.Errorf("Workflow ownership changed")
		}
	}
	for _, key := range []string{"loom.ml/instance-id", "loom.ml/pipeline-namespace-uid", "loom.ml/pipeline-engine-release", "loom.ml/pipeline-plan-hash"} {
		if plan.Annotations[key] == "" || plan.Annotations[key] != observed.Annotations[key] {
			return fmt.Errorf("Workflow plan binding changed")
		}
	}
	baseline, err := json.Marshal(plan.Spec)
	if err != nil || len(baseline) > MaxIRBytes || sum(baseline) != plan.Annotations["loom.ml/pipeline-plan-hash"] || plan.Spec.Suspend == nil || !*plan.Spec.Suspend {
		return fmt.Errorf("frozen plan digest is invalid")
	}
	actual := observed.Spec.DeepCopy()
	actual.Suspend = ptr(true)
	actual.Shutdown = plan.Spec.Shutdown
	encoded, err := json.Marshal(actual)
	if err != nil || sum(encoded) != sum(baseline) {
		return fmt.Errorf("Workflow execution spec differs from frozen plan")
	}
	return nil
}
