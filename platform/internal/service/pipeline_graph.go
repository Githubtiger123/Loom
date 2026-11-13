package service

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/githubtiger123/loom/platform/internal/dto"
)

type pipelineIR struct {
	Root       pipelineComponent            `json:"root"`
	Components map[string]pipelineComponent `json:"components"`
}
type pipelineComponent struct {
	DAG *struct {
		Tasks   map[string]pipelineTaskSpec           `json:"tasks"`
		Outputs map[string]map[string]json.RawMessage `json:"outputs"`
	} `json:"dag"`
	ExecutorLabel string                                `json:"executorLabel"`
	Inputs        map[string]map[string]json.RawMessage `json:"inputDefinitions"`
	Outputs       map[string]map[string]json.RawMessage `json:"outputDefinitions"`
}
type pipelineTaskSpec struct {
	Info struct {
		Name string `json:"name"`
	} `json:"taskInfo"`
	Component struct {
		Name string `json:"name"`
	} `json:"componentRef"`
	Inputs            map[string]map[string]json.RawMessage `json:"inputs"`
	Dependencies      []string                              `json:"dependentTasks"`
	Trigger           json.RawMessage                       `json:"triggerPolicy,omitempty"`
	Caching           json.RawMessage                       `json:"cachingOptions,omitempty"`
	Retry             json.RawMessage                       `json:"retryPolicy,omitempty"`
	ParameterIterator json.RawMessage                       `json:"parameterIterator,omitempty"`
	ArtifactIterator  json.RawMessage                       `json:"artifactIterator,omitempty"`
	IteratorPolicy    json.RawMessage                       `json:"iteratorPolicy,omitempty"`
}

type pipelineParameterSelector struct {
	Task string `json:"producerSubtask"`
	Key  string `json:"outputParameterKey"`
}

type PipelineNodeDefinition struct {
	ID                string                                `json:"nodeId"`
	ParentID          string                                `json:"parentNodeId,omitempty"`
	Path              []string                              `json:"path"`
	TaskName          string                                `json:"taskName"`
	DisplayName       string                                `json:"displayName"`
	Component         string                                `json:"component"`
	Kind              string                                `json:"kind"`
	ExecutorLabel     string                                `json:"executorLabel,omitempty"`
	Inputs            map[string]map[string]json.RawMessage `json:"inputDefinitions"`
	Outputs           map[string]map[string]json.RawMessage `json:"outputDefinitions"`
	Bindings          map[string]map[string]json.RawMessage `json:"inputBindings"`
	DAGOutputs        map[string]map[string]json.RawMessage `json:"dagOutputBindings,omitempty"`
	Trigger           json.RawMessage                       `json:"triggerPolicy,omitempty"`
	Caching           json.RawMessage                       `json:"cachingOptions,omitempty"`
	Retry             json.RawMessage                       `json:"retryPolicy,omitempty"`
	ParameterIterator json.RawMessage                       `json:"parameterIterator,omitempty"`
	ArtifactIterator  json.RawMessage                       `json:"artifactIterator,omitempty"`
	IteratorPolicy    json.RawMessage                       `json:"iteratorPolicy,omitempty"`
}
type PipelineDAGEdge struct {
	ScopeID    string `json:"scopeNodeId"`
	Source     string `json:"sourceNodeId"`
	Target     string `json:"targetNodeId"`
	Kind       string `json:"kind"` // CONTROL, PARAMETER, ARTIFACT, FINAL_STATUS
	SourcePort string `json:"sourcePort,omitempty"`
	TargetPort string `json:"targetPort,omitempty"`
	Boundary   string `json:"boundary,omitempty"` // INPUT or OUTPUT of ScopeID, not containment.
}

type pipelineGraph struct {
	Definitions []PipelineNodeDefinition
	Edges       []PipelineDAGEdge
	byID        map[string]*PipelineNodeDefinition
	children    map[string]map[string]string
}

func buildPipelineGraph(spec dto.PipelineRunSpec) (*pipelineGraph, error) {
	var ir pipelineIR
	if len(spec.PipelineSpec) == 0 || json.Unmarshal(spec.PipelineSpec, &ir) != nil || ir.Root.DAG == nil {
		return nil, fmt.Errorf("run has no supported KFP v2 IR snapshot")
	}
	g := &pipelineGraph{Definitions: []PipelineNodeDefinition{}, Edges: []PipelineDAGEdge{}, byID: map[string]*PipelineNodeDefinition{}, children: map[string]map[string]string{}}
	var walk func(string, string, []string, string, pipelineTaskSpec, pipelineComponent, map[string]bool) error
	walk = func(id, parent string, path []string, taskName string, task pipelineTaskSpec, component pipelineComponent, ancestors map[string]bool) error {
		if len(path) > 32 || len(g.Definitions) >= 2000 {
			return fmt.Errorf("DAG expansion limit exceeded")
		}
		kind := "EXECUTOR"
		if component.DAG != nil {
			kind = "DAG"
		}
		name := task.Info.Name
		if name == "" {
			name = taskName
		}
		definition := PipelineNodeDefinition{ID: id, ParentID: parent, Path: path, TaskName: taskName, DisplayName: name, Component: task.Component.Name, Kind: kind, ExecutorLabel: component.ExecutorLabel, Inputs: component.Inputs, Outputs: component.Outputs, Bindings: task.Inputs, Trigger: task.Trigger, Caching: task.Caching, Retry: task.Retry, ParameterIterator: task.ParameterIterator, ArtifactIterator: task.ArtifactIterator, IteratorPolicy: task.IteratorPolicy}
		if component.DAG != nil {
			definition.DAGOutputs = component.DAG.Outputs
		}
		g.Definitions = append(g.Definitions, definition)
		if component.DAG == nil {
			return nil
		}
		g.children[id] = map[string]string{}
		for _, key := range sortedKeys(component.DAG.Tasks) {
			p := append(append([]string{}, path...), key)
			raw, _ := json.Marshal(p)
			g.children[id][key] = "spec-" + mlHash(raw)[:32]
		}
		for _, key := range sortedKeys(component.DAG.Tasks) {
			t := component.DAG.Tasks[key]
			child, exists := ir.Components[t.Component.Name]
			if !exists || ancestors[t.Component.Name] {
				return fmt.Errorf("invalid or cyclic DAG component")
			}
			next := map[string]bool{}
			for k, v := range ancestors {
				next[k] = v
			}
			next[t.Component.Name] = true
			if err := walk(g.children[id][key], id, append(append([]string{}, path...), key), key, t, child, next); err != nil {
				return err
			}
			childID := g.children[id][key]
			for _, dep := range t.Dependencies {
				up := g.children[id][dep]
				if up == "" {
					return fmt.Errorf("missing dependency")
				}
				g.Edges = append(g.Edges, PipelineDAGEdge{ScopeID: id, Source: up, Target: childID, Kind: "CONTROL"})
			}
			for _, category := range []string{"parameters", "artifacts"} {
				for _, port := range sortedKeys(t.Inputs[category]) {
					var binding struct {
						Parameter *struct {
							Task string `json:"producerTask"`
							Key  string `json:"outputParameterKey"`
						} `json:"taskOutputParameter"`
						Artifact *struct {
							Task string `json:"producerTask"`
							Key  string `json:"outputArtifactKey"`
						} `json:"taskOutputArtifact"`
						Final *struct {
							Task string `json:"producerTask"`
						} `json:"taskFinalStatus"`
						InputParameter string `json:"componentInputParameter"`
						InputArtifact  string `json:"componentInputArtifact"`
					}
					if json.Unmarshal(t.Inputs[category][port], &binding) != nil {
						return fmt.Errorf("invalid input binding")
					}
					e := PipelineDAGEdge{ScopeID: id, Target: childID, TargetPort: port}
					var producer string
					switch {
					case binding.Parameter != nil:
						producer = binding.Parameter.Task
						e.SourcePort = binding.Parameter.Key
						e.Kind = "PARAMETER"
					case binding.Artifact != nil:
						producer = binding.Artifact.Task
						e.SourcePort = binding.Artifact.Key
						e.Kind = "ARTIFACT"
					case binding.Final != nil:
						producer = binding.Final.Task
						e.Kind = "FINAL_STATUS"
					case binding.InputParameter != "":
						e.Source = id
						e.SourcePort = binding.InputParameter
						e.Kind = "PARAMETER"
						e.Boundary = "INPUT"
					case binding.InputArtifact != "":
						e.Source = id
						e.SourcePort = binding.InputArtifact
						e.Kind = "ARTIFACT"
						e.Boundary = "INPUT"
					default:
						continue
					}
					if producer != "" {
						e.Source = g.children[id][producer]
					}
					if e.Source == "" {
						return fmt.Errorf("missing input producer")
					}
					g.Edges = append(g.Edges, e)
				}
			}
		}
		for _, category := range []string{"parameters", "artifacts"} {
			for _, port := range sortedKeys(component.DAG.Outputs[category]) {
				// Parameters have valueFromParameter; artifacts have an artifactSelectors list.
				var output struct {
					Parameter *pipelineParameterSelector `json:"valueFromParameter"`
					OneOf     *struct {
						Selectors []pipelineParameterSelector `json:"parameterSelectors"`
					} `json:"valueFromOneof"`
					Artifacts []struct {
						Task string `json:"producerSubtask"`
						Key  string `json:"outputArtifactKey"`
					} `json:"artifactSelectors"`
				}
				if json.Unmarshal(component.DAG.Outputs[category][port], &output) != nil {
					return fmt.Errorf("invalid DAG output")
				}
				selectors := []pipelineParameterSelector{}
				if output.Parameter != nil {
					selectors = append(selectors, *output.Parameter)
				}
				if output.OneOf != nil {
					selectors = append(selectors, output.OneOf.Selectors...)
				}
				for _, selector := range selectors {
					up := g.children[id][selector.Task]
					if up == "" {
						return fmt.Errorf("missing output producer")
					}
					g.Edges = append(g.Edges, PipelineDAGEdge{ScopeID: id, Source: up, Target: id, Kind: "PARAMETER", SourcePort: selector.Key, TargetPort: port, Boundary: "OUTPUT"})
				}
				for _, a := range output.Artifacts {
					up := g.children[id][a.Task]
					if up == "" {
						return fmt.Errorf("missing artifact producer")
					}
					g.Edges = append(g.Edges, PipelineDAGEdge{ScopeID: id, Source: up, Target: id, Kind: "ARTIFACT", SourcePort: a.Key, TargetPort: port, Boundary: "OUTPUT"})
				}
			}
		}
		return nil
	}
	if err := walk("root", "", []string{}, "root", pipelineTaskSpec{}, ir.Root, map[string]bool{}); err != nil {
		return nil, err
	}
	for i := range g.Definitions {
		g.byID[g.Definitions[i].ID] = &g.Definitions[i]
	}
	return g, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
