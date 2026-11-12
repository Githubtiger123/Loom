package k8s

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
)

func NativeRuntimeNodeKey(definition, id string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(definition+"\x00"+id)))
}

// Recognize actual node IDs through template, boundary and invocation hierarchy.
// Loop values/display names never select a definition, and Pod names are not
// guessed. Structural TaskGroup/Retry nodes and each attempt retain identities.
func MatchNativeAdvancedWorkflowNodes(ref PipelineWorkflowRef, plan *pipelineir.Plan, obs *PipelineWorkflowObservation) (map[string]PipelineWorkflowNode, error) {
	bad := func() (map[string]PipelineWorkflowNode, error) {
		return nil, &Permanent{Reason: "native dynamic node source identity conflict"}
	}
	matched := map[string]PipelineWorkflowNode{}
	byID := map[string]PipelineWorkflowNode{}
	byTemplate := map[string]pipelineir.Task{}
	for _, task := range plan.Tasks() {
		byTemplate[task.TemplateName] = task
	}
	var rootID string
	seenNames := map[string]bool{}
	for _, n := range obs.Nodes {
		if n.Name == "" || seenNames[n.Name] || n.TemplateScope != "" && n.TemplateScope != "local/"+ref.Name {
			return bad()
		}
		seenNames[n.Name] = true
		children := map[string]bool{}
		for _, child := range n.Children {
			if child == n.ID || children[child] {
				return bad()
			}
			children[child] = true
		}
		if n.Name == ref.Name {
			if rootID != "" || n.Type != "DAG" || n.BoundaryID != "" || n.TemplateName != plan.Root().TemplateName {
				return bad()
			}
			rootID = n.ID
			n.DefinitionKey = plan.Root().NodeKey
			byID[n.ID] = n
			matched[plan.Root().NodeKey] = n
		}
	}
	if len(obs.Nodes) == 0 {
		return matched, nil
	}
	if rootID == "" {
		return bad()
	}
	for pass := 0; pass < MaxNativeWorkflowNodes && len(byID) < len(obs.Nodes); pass++ {
		progress := false
		for id, node := range obs.Nodes {
			if _, done := byID[id]; done {
				continue
			}
			if node.TemplateScope != "" && node.TemplateScope != "local/"+ref.Name {
				return bad()
			}
			task, found := byTemplate[node.TemplateName]
			// Argo omits templateName for condition-false/omitted invocations.
			if !found && (node.Type == "Skipped" || node.Type == "Omitted" || node.Type == "TaskGroup") {
				for _, t := range plan.Tasks() {
					if parent, ok := byID[node.BoundaryID]; ok && node.Name == parent.Name+"."+t.TaskName {
						task, found = t, true
						break
					}
				}
				// Pre-execution failures may omit templateName even for a retry
				// attempt or loop iteration. Recover only through the already
				// recognized structural parent's explicit child IDs; names and
				// phases are still checked below.
				if !found {
					for _, candidate := range byID {
						if candidate.BoundaryID != node.BoundaryID || candidate.Type != "Retry" && candidate.Type != "TaskGroup" {
							continue
						}
						for _, childID := range candidate.Children {
							if childID != id {
								continue
							}
							if found {
								return bad()
							}
							for _, definition := range plan.Tasks() {
								if definition.NodeKey == candidate.DefinitionKey {
									task, found = definition, true
									break
								}
							}
						}
					}
				}
			}
			if !found {
				if _, ready := byID[node.BoundaryID]; !ready {
					continue
				}
				return bad()
			}
			parent, ready := byID[node.BoundaryID]
			if !ready {
				continue
			}
			parentScope := ""
			if parent.ID != rootID {
				for _, t := range plan.Tasks() {
					if t.NodeKey == parent.DefinitionKey {
						parentScope = t.TaskName
						break
					}
				}
			}
			base := parent.Name + "." + task.TaskName
			logicalParent := parent
			iteration := append([]int{}, parent.IterationPath...)
			retryIndex := 0
			if node.Name != base {
				// Iteration and retry children are explicitly listed by their
				// structural parent, even though Argo retains the DAG boundary.
				ready = false
				for _, candidate := range byID {
					if candidate.BoundaryID != node.BoundaryID || candidate.DefinitionKey != task.NodeKey || candidate.Type != "TaskGroup" && candidate.Type != "Retry" {
						continue
					}
					child := false
					for _, childID := range candidate.Children {
						child = child || childID == id
					}
					if !child {
						continue
					}
					if candidate.Type == "Retry" {
						if task.Retry == nil {
							return bad()
						}
						for i, childID := range candidate.Children {
							if childID == id {
								retryIndex = i
								break
							}
						}
						preExecutionFailure := (node.Type == "Skipped" || node.Type == "Omitted") && node.Phase == "FAILED"
						if node.Name != candidate.Name+"("+strconv.Itoa(retryIndex)+")" || retryIndex > int(task.Retry.Limit) || node.Type != "Pod" && !preExecutionFailure {
							return bad()
						}
					} else {
						prefix := candidate.Name + "("
						if !strings.HasPrefix(node.Name, prefix) || !strings.HasSuffix(node.Name, ")") {
							return bad()
						}
						tail := strings.TrimPrefix(node.Name, prefix)
						colon := strings.IndexByte(tail, ':')
						if colon < 1 {
							return bad()
						}
						index, err := strconv.Atoi(tail[:colon])
						if err != nil || index < 0 || index >= pipelineir.MaxTasks || task.Group == nil || task.Group.Iterator == nil || (node.Type != "DAG" && node.Type != "Skipped" && node.Type != "Omitted") {
							return bad()
						}
						iteration = append(append([]int{}, candidate.IterationPath...), index)
					}
					logicalParent = candidate
					ready = true
					break
				}
				if !ready {
					continue
				}
			} else {
				if task.Scope != parentScope {
					return bad()
				}
				switch node.Type {
				case "TaskGroup":
					if task.Group == nil || task.Group.Iterator == nil {
						return bad()
					}
				case "Retry":
					if task.Retry == nil {
						return bad()
					}
				case "DAG":
					if task.Group == nil || task.Group.Iterator != nil {
						return bad()
					}
				case "Pod":
					// Argo checks memoization before injecting the Retry wrapper.
					// Cached nodes have no Pod; Argo v4 normally retains Retry
					// type for retryable templates (handled above).
					if task.Group != nil || task.Retry != nil && !node.CacheHit {
						return bad()
					}
				case "Skipped", "Omitted":
					// Argo also uses Skipped-shaped nodes for errors that occur
					// before a template/Pod can be instantiated.
					if node.Phase != "SKIPPED" && node.Phase != "FAILED" {
						return bad()
					}
				default:
					return bad()
				}
			}
			node.DefinitionKey = task.NodeKey
			node.ParentNodeID = logicalParent.ID
			node.IterationPath = iteration
			node.RetryIndex = retryIndex
			byID[id] = node
			matched[NativeRuntimeNodeKey(task.NodeKey, id)] = node
			progress = true
		}
		if !progress {
			break
		}
	}
	if len(byID) != len(obs.Nodes) {
		return bad()
	}
	return matched, nil
}
