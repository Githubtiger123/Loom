package k8s

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func nativeExpr(s string) string { return "{{=" + s + "}}" }
func nativeScopeValues() string  { return "fromJson(sprig.b64dec(inputs.parameters['loom-values']))" }
func nativeScopeArtifacts() string {
	return "fromJson(sprig.b64dec(inputs.parameters['loom-artifacts']))"
}
func nativeBundle(task string) string {
	return "fromJson(sprig.b64dec(tasks[" + pipelineir.ExprLiteral(task) + "].outputs.parameters['loom-bundle']))"
}

func buildAdvancedPythonTemplates(obj *unstructured.Unstructured, ref PipelineWorkflowRef, plan *pipelineir.Plan, policy NativePipelineWorkflowPolicy) error {
	u, err := url.Parse("https://" + policy.ArtifactEndpoint)
	if err != nil || u.Host != policy.ArtifactEndpoint || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !validator.IsDNS1123Subdomain(policy.ArtifactBucket) || !validator.IsDNS1123Subdomain(policy.ArtifactSecret) {
		return fmt.Errorf("invalid native artifact policy")
	}
	tasks := plan.Tasks()
	byName := map[string]pipelineir.Task{}
	for _, task := range tasks {
		byName[task.TaskName] = task
	}
	spec := obj.Object["spec"].(map[string]any)
	templates := spec["templates"].([]any)
	spec["securityContext"] = map[string]any{"runAsUser": int64(1000), "runAsGroup": int64(1000), "fsGroup": int64(1000)}
	spec["volumes"] = []any{map[string]any{"name": "loom-data", "emptyDir": map[string]any{}}}
	root := map[string]any{}
	for name, port := range plan.RootPorts().Parameters {
		v, ok := policy.Parameters[name]
		if !ok && len(port.Default) > 0 {
			if json.Unmarshal(port.Default, &v) != nil {
				return fmt.Errorf("invalid root default")
			}
			ok = true
		}
		raw, _ := json.Marshal(v)
		if !ok || !pipelineir.AdvancedParameterValid(port.Type, raw) {
			return fmt.Errorf("invalid root parameter")
		}
		root[name] = v
	}
	for name := range policy.Parameters {
		if _, ok := root[name]; !ok {
			return fmt.Errorf("unknown root parameter")
		}
	}
	rootJSON, _ := json.Marshal(root)
	inputParams := func() []any {
		return []any{map[string]any{"name": "loom-values"}, map[string]any{"name": "loom-artifacts"}}
	}
	transport := func(key string) map[string]any {
		return map[string]any{"endpoint": policy.ArtifactEndpoint, "bucket": policy.ArtifactBucket, "key": key, "region": policy.ArtifactRegion, "insecure": policy.ArtifactInsecure, "accessKeySecret": map[string]any{"name": policy.ArtifactSecret, "key": "accessKey"}, "secretKeySecret": map[string]any{"name": policy.ArtifactSecret, "key": "secretKey"}}
	}
	// A loop's output parameter is Argo's JSON array of base64 bundles. Decode
	// each element and collect the requested output, preserving iteration order.
	parameterValue := func(b pipelineir.Binding) (string, error) {
		v := ""
		if len(b.Value) > 0 {
			v = "fromJson(sprig.b64dec(" + pipelineir.ExprLiteral(base64.StdEncoding.EncodeToString(b.Value)) + "))"
		}
		if b.Root != "" {
			v = nativeScopeValues() + "[" + pipelineir.ExprLiteral(b.Root) + "]"
		} else if b.Task != "" {
			producer := byName[b.Task]
			if producer.Group != nil && producer.Group.Iterator != nil {
				v = "map(fromJson(tasks[" + pipelineir.ExprLiteral(b.Task) + "].outputs.parameters['loom-bundle']),{fromJson(sprig.b64dec(#)).parameters[" + pipelineir.ExprLiteral(b.Output) + "]})"
			} else {
				v = nativeBundle(b.Task) + ".parameters[" + pipelineir.ExprLiteral(b.Output) + "]"
			}
		}
		if v == "" {
			return "", fmt.Errorf("missing parameter binding")
		}
		return pipelineir.ArgoSelector(b.Selector, v)
	}
	artifactValue := func(b pipelineir.Binding) string {
		if b.Root != "" {
			return nativeScopeArtifacts() + "[" + pipelineir.ExprLiteral(b.Root) + "]"
		}
		return nativeBundle(b.Task) + ".artifacts[" + pipelineir.ExprLiteral(b.Output) + "]"
	}
	artifactTransport := func(b pipelineir.Binding, scope string) string {
		if b.Root != "" {
			ports := plan.RootPorts()
			if parent, ok := byName[scope]; ok {
				ports = parent.Runtime.Inputs
			}
			return "inputs.artifacts[" + pipelineir.ExprLiteral(pipelineir.PortIndex(ports.Artifacts, b.Root)) + "]"
		}
		return "tasks[" + pipelineir.ExprLiteral(b.Task) + "].outputs.artifacts[" + pipelineir.ExprLiteral(pipelineir.PortIndex(byName[b.Task].Runtime.Outputs.Artifacts, b.Output)) + "]"
	}
	graphs := map[string][]any{"": {}}
	for i, task := range tasks {
		template := templates[i+1].(map[string]any)
		rt := task.Runtime
		if rt == nil {
			return fmt.Errorf("missing task runtime")
		}
		values := []string{}
		for _, name := range nativeSortedKeys(rt.Parameters) {
			v, err := parameterValue(rt.Parameters[name])
			if err != nil {
				return err
			}
			values = append(values, pipelineir.ExprLiteral(name)+":"+v)
		}
		conditionValues := "{" + strings.Join(values, ",") + "}"
		if task.Group != nil && task.Group.Iterator != nil {
			it := task.Group.Iterator
			item := "item"
			switch rt.Inputs.Parameters[it.ItemInput].Type {
			case "NUMBER_INTEGER":
				item = "asInt(item)"
			case "NUMBER_DOUBLE":
				item = "asFloat(item)"
			case "BOOLEAN":
				item = "(string(item) == 'true')"
			case "STRUCT", "LIST":
				// Argo's processItem exposes collections as JSON text.
				item = "fromJson(item)"
			}
			values = append(values, pipelineir.ExprLiteral(it.ItemInput)+":"+item)
		}
		artifacts := []string{}
		inputArtifacts, argumentArtifacts, outputArtifacts := []any{}, []any{}, []any{}
		for _, name := range nativeSortedKeys(rt.Artifacts) {
			b := rt.Artifacts[name]
			key := pipelineir.PortIndex(rt.Inputs.Artifacts, name)
			artifacts = append(artifacts, pipelineir.ExprLiteral(name)+":"+artifactValue(b))
			entry := map[string]any{"name": key}
			if task.Group == nil {
				entry["path"] = "/tmp/loom/inputs/" + key + "/data"
			}
			inputArtifacts = append(inputArtifacts, entry)
			argumentArtifacts = append(argumentArtifacts, map[string]any{"name": key, "fromExpression": artifactTransport(b, task.Scope)})
		}
		arguments := map[string]any{"parameters": []any{map[string]any{"name": "loom-values", "value": nativeExpr("sprig.b64enc(toJson({" + strings.Join(values, ",") + "}))")}, map[string]any{"name": "loom-artifacts", "value": nativeExpr("sprig.b64enc(toJson({" + strings.Join(artifacts, ",") + "}))")}}, "artifacts": argumentArtifacts}
		invocation := map[string]any{"name": task.TaskName, "template": task.TemplateName, "arguments": arguments}
		if len(task.Dependencies) > 0 {
			deps := []any{}
			for _, dep := range task.Dependencies {
				deps = append(deps, dep)
			}
			invocation["dependencies"] = deps
		}
		if task.Condition != "" {
			expr, err := pipelineir.ArgoCondition(task.Condition, conditionValues)
			if err != nil {
				return err
			}
			invocation["when"] = nativeExpr(expr)
		}
		if task.Group != nil && task.Group.Iterator != nil {
			it := task.Group.Iterator
			items := "fromJson(sprig.b64dec(" + pipelineir.ExprLiteral(base64.StdEncoding.EncodeToString(it.Raw)) + "))"
			if it.Input != "" {
				v, err := parameterValue(rt.Parameters[it.Input])
				if err != nil {
					return err
				}
				items = v
				if rt.Inputs.Parameters[it.Input].Type == "STRING" {
					items = "fromJson(" + items + ")"
				}
			}
			// Enforce the runtime expansion bound before Argo creates iteration
			// nodes, including JSON lists supplied by an upstream STRING.
			invocation["withParam"] = nativeExpr("let loomItems = " + items + "; type(loomItems) == 'array' && len(loomItems) <= " + fmt.Sprint(pipelineir.MaxTasks) + " ? toJson(loomItems) : 'loom-invalid-iterator-input'")
			if it.Parallelism > 0 {
				// Hold one semaphore for the entire iteration DAG. A template's
				// parallelism only limits tasks inside each individual iteration.
				template["synchronization"] = map[string]any{"semaphores": []any{map[string]any{"configMapKeyRef": map[string]any{"name": NativeCacheConfigMapName(ref), "key": "parallelism-" + task.TaskName}}}}
			}
		}
		graphs[task.Scope] = append(graphs[task.Scope], invocation)
		template["inputs"] = map[string]any{"parameters": inputParams(), "artifacts": inputArtifacts}
		if task.Group != nil {
			delete(template, "container")
			bundleParams, bundleArtifacts := []string{}, []string{}
			for _, name := range nativeSortedKeys(task.Group.Parameters) {
				v, err := parameterValue(task.Group.Parameters[name])
				if err != nil {
					return err
				}
				bundleParams = append(bundleParams, pipelineir.ExprLiteral(name)+":"+v)
			}
			for _, name := range nativeSortedKeys(task.Group.Artifacts) {
				b := task.Group.Artifacts[name]
				bundleArtifacts = append(bundleArtifacts, pipelineir.ExprLiteral(name)+":"+artifactValue(b))
				outputArtifacts = append(outputArtifacts, map[string]any{"name": pipelineir.PortIndex(rt.Outputs.Artifacts, name), "fromExpression": artifactTransport(b, task.TaskName)})
			}
			bundle := "{'protocol':" + pipelineir.ExprLiteral(pipelineir.AdvancedOutputProtocol) + ",'parameters':{" + strings.Join(bundleParams, ",") + "},'artifacts':{" + strings.Join(bundleArtifacts, ",") + "}}"
			template["outputs"] = map[string]any{"parameters": []any{map[string]any{"name": "loom-bundle", "valueFrom": map[string]any{"expression": "sprig.b64enc(toJson(" + bundle + "))"}}}, "artifacts": outputArtifacts}
		} else {
			// Arguments were resolved by Argo; the launcher still invokes the
			// SDK's executor_main and official OutputPath conversion helpers.
			copy := *rt
			copy.Parameters = map[string]pipelineir.Binding{}
			copy.Artifacts = map[string]pipelineir.Binding{}
			for name := range rt.Inputs.Parameters {
				copy.Parameters[name] = pipelineir.Binding{Root: name}
			}
			for name := range rt.Inputs.Artifacts {
				copy.Artifacts[name] = pipelineir.Binding{Task: "inputs", Output: name}
			}
			runtimeJSON, _ := json.Marshal(map[string]any{"runtime": copy, "command": task.Command, "args": task.Args, "upstream": map[string]string{"inputs": "LOOM_INPUT_ARTIFACTS"}})
			container := template["container"].(map[string]any)
			if task.Cache {
				key := nativeExpr("sprig.sha256sum(" + pipelineir.ExprLiteral(pipelineir.CacheIdentity(task)+":") + " + inputs.parameters['loom-values'] + ':' + inputs.parameters['loom-artifacts'])")
				template["memoize"] = map[string]any{"key": key, "maxAge": "24h", "cache": map[string]any{"configMap": map[string]any{"name": NativeCacheConfigMapName(ref)}}}
				// Argo rechecks memoization after acquiring this mutex. Identical
				// concurrent iterations therefore cannot race cache publication.
				template["synchronization"] = map[string]any{"mutexes": []any{map[string]any{"name": NativeCacheConfigMapName(ref) + "-" + key}}}
			}
			container["command"] = []any{"python3", "-c", pipelineir.LauncherForCompiler(plan.Compiler())}
			container["args"] = []any{}
			container["env"] = []any{map[string]any{"name": "HOME", "value": "/tmp/loom/home"}, map[string]any{"name": "LOOM_ROOT", "value": "{{inputs.parameters.loom-values}}"}, map[string]any{"name": "LOOM_INPUT_ARTIFACTS", "value": nativeExpr("sprig.b64enc(toJson({'artifacts':" + nativeScopeArtifacts() + "}))")}, map[string]any{"name": "LOOM_TASK", "value": base64.StdEncoding.EncodeToString(runtimeJSON)}}
			container["volumeMounts"] = []any{map[string]any{"name": "loom-data", "mountPath": "/tmp/loom"}}
			for _, name := range nativeSortedKeys(rt.Outputs.Artifacts) {
				key := pipelineir.PortIndex(rt.Outputs.Artifacts, name)
				outputArtifacts = append(outputArtifacts, map[string]any{"name": key, "path": "/tmp/loom/outputs/" + key + "/data", "archive": map[string]any{"none": map[string]any{}}, "s3": transport(NativeArtifactKey(ref, task.TaskName, key) + "/pods/{{pod.name}}")})
			}
			template["outputs"] = map[string]any{"parameters": []any{map[string]any{"name": "loom-bundle", "valueFrom": map[string]any{"path": "/tmp/loom/bundle"}}}, "artifacts": outputArtifacts}
			if task.Retry != nil {
				r := task.Retry
				strategy := map[string]any{"limit": int64(r.Limit), "backoff": map[string]any{"duration": r.Duration, "factor": fmt.Sprint(r.Factor), "maxDuration": r.MaxDuration}}
				if r.Policy != "" {
					strategy["retryPolicy"] = r.Policy
				}
				template["retryStrategy"] = strategy
			}
			if p := policy.LogArchive; p != nil {
				u, err := url.Parse("https://" + p.Endpoint)
				if err != nil || u.Host != p.Endpoint || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !validator.IsArchivePath(p.Prefix) || !validator.IsArchiveSegment(p.Cluster) || !validator.IsDNS1123Subdomain(p.Bucket) || !validator.IsDNS1123Subdomain(p.Secret) {
					return fmt.Errorf("invalid log archive policy")
				}
				template["archiveLocation"] = map[string]any{"archiveLogs": true, "s3": map[string]any{"endpoint": p.Endpoint, "bucket": p.Bucket, "region": p.Region, "insecure": p.Insecure, "key": strings.Join([]string{p.Prefix, p.Cluster, ref.Namespace, ref.InstanceID, "{{pod.name}}"}, "/"), "accessKeySecret": map[string]any{"name": p.Secret, "key": "accessKey"}, "secretKeySecret": map[string]any{"name": p.Secret, "key": "secretKey"}}}
			}
		}
	}
	rootTemplate := templates[0].(map[string]any)
	rootTemplate["inputs"] = map[string]any{"parameters": []any{map[string]any{"name": "loom-values", "default": base64.StdEncoding.EncodeToString(rootJSON)}, map[string]any{"name": "loom-artifacts", "default": "e30="}}}
	rootTemplate["dag"] = map[string]any{"failFast": true, "tasks": graphs[""]}
	for i, task := range tasks {
		if task.Group != nil {
			templates[i+1].(map[string]any)["dag"] = map[string]any{"failFast": true, "tasks": graphs[task.TaskName]}
		}
	}
	encoded, _ := json.Marshal(obj.Object)
	if len(encoded) > pipelineir.MaxIRBytes {
		return fmt.Errorf("native Workflow exceeds size policy")
	}
	return nil
}

func nativeSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
