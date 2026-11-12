package k8s

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/githubtiger123/loom/platform/internal/infra/pipelineir"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func NativeArtifactKey(ref PipelineWorkflowRef, task, port string) string {
	return fmt.Sprintf("loom-native/%d/%s/%s/%s/%s", ref.GroupID, ref.NamespaceUID, ref.InstanceID, task, port)
}

// Operator-owned S3 settings. No credentials enter the Workflow's command,
// metadata bundle, plan, API or database; Argo reads namespace-local Secrets.
func buildNativePythonTemplates(obj *unstructured.Unstructured, ref PipelineWorkflowRef, plan *pipelineir.Plan, policy NativePipelineWorkflowPolicy) error {
	u, err := url.Parse("https://" + policy.ArtifactEndpoint)
	if err != nil || u.Host != policy.ArtifactEndpoint || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
		!validator.IsDNS1123Subdomain(policy.ArtifactBucket) || !validator.IsDNS1123Subdomain(policy.ArtifactSecret) {
		return fmt.Errorf("invalid native artifact policy")
	}
	spec := obj.Object["spec"].(map[string]any)
	spec["securityContext"] = map[string]any{"runAsUser": int64(1000), "runAsGroup": int64(1000), "fsGroup": int64(1000)}
	spec["volumes"] = []any{map[string]any{"name": "loom-data", "emptyDir": map[string]any{}}}
	templates := spec["templates"].([]any)
	graph := templates[0].(map[string]any)["dag"].(map[string]any)["tasks"].([]any)
	tasks := plan.Tasks()
	byName := map[string]pipelineir.Task{}
	for _, task := range tasks {
		byName[task.TaskName] = task
	}
	s3 := func(key string) map[string]any {
		return map[string]any{"endpoint": policy.ArtifactEndpoint, "bucket": policy.ArtifactBucket, "key": key, "region": policy.ArtifactRegion, "insecure": policy.ArtifactInsecure,
			"accessKeySecret": map[string]any{"name": policy.ArtifactSecret, "key": "accessKey"}, "secretKeySecret": map[string]any{"name": policy.ArtifactSecret, "key": "secretKey"}}
	}
	for i, task := range tasks {
		rt := task.Runtime
		if rt == nil {
			return fmt.Errorf("missing Python runtime plan")
		}
		root := map[string]any{}
		for name, port := range rt.RootParameters {
			value, ok := policy.Parameters[name]
			if !ok && len(port.Default) > 0 {
				if json.Unmarshal(port.Default, &value) != nil {
					return fmt.Errorf("invalid parameter default")
				}
				ok = true
			}
			raw, _ := json.Marshal(value)
			if !ok || !pipelineir.ParameterValid(port.Type, raw) {
				return fmt.Errorf("missing or invalid native root parameter")
			}
			root[name] = value
		}
		for name := range policy.Parameters {
			if _, ok := rt.RootParameters[name]; !ok {
				return fmt.Errorf("unknown native root parameter")
			}
		}
		rootJSON, _ := json.Marshal(root)
		upstream := map[string]string{}
		needed := map[string]bool{}
		for _, b := range rt.Parameters {
			if b.Task != "" {
				needed[b.Task] = true
			}
		}
		for _, b := range rt.Artifacts {
			needed[b.Task] = true
		}
		names := []string{}
		for name := range needed {
			names = append(names, name)
		}
		sort.Strings(names)
		inputParams, argumentParams := []any{}, []any{}
		env := []any{map[string]any{"name": "HOME", "value": "/tmp/loom/home"}, map[string]any{"name": "LOOM_ROOT", "value": base64.StdEncoding.EncodeToString(rootJSON)}}
		for j, name := range names {
			key := fmt.Sprintf("u%d", j)
			variable := fmt.Sprintf("LOOM_UPSTREAM_%d", j)
			upstream[name] = variable
			inputParams = append(inputParams, map[string]any{"name": key})
			argumentParams = append(argumentParams, map[string]any{"name": key, "value": "{{tasks." + name + ".outputs.parameters.loom-bundle}}"})
			env = append(env, map[string]any{"name": variable, "value": "{{inputs.parameters." + key + "}}"})
		}
		runtimeJSON, _ := json.Marshal(map[string]any{"runtime": rt, "command": task.Command, "args": task.Args, "upstream": upstream})
		env = append(env, map[string]any{"name": "LOOM_TASK", "value": base64.StdEncoding.EncodeToString(runtimeJSON)})
		inputArtifacts, argumentArtifacts, outputArtifacts := []any{}, []any{}, []any{}
		for name, b := range rt.Artifacts {
			key := pipelineir.PortIndex(rt.Inputs.Artifacts, name)
			producer := byName[b.Task]
			outKey := pipelineir.PortIndex(producer.Runtime.Outputs.Artifacts, b.Output)
			inputArtifacts = append(inputArtifacts, map[string]any{"name": key, "path": "/tmp/loom/inputs/" + key + "/data"})
			argumentArtifacts = append(argumentArtifacts, map[string]any{"name": key, "from": "{{tasks." + b.Task + ".outputs.artifacts." + outKey + "}}"})
		}
		for name := range rt.Outputs.Artifacts {
			key := pipelineir.PortIndex(rt.Outputs.Artifacts, name)
			outputArtifacts = append(outputArtifacts, map[string]any{"name": key, "path": "/tmp/loom/outputs/" + key + "/data", "archive": map[string]any{"none": map[string]any{}}, "s3": s3(NativeArtifactKey(ref, task.TaskName, key))})
		}
		// Map iteration must not change the frozen spec hash on restart.
		sortArtifacts := func(rows []any) {
			sort.Slice(rows, func(i, j int) bool {
				return rows[i].(map[string]any)["name"].(string) < rows[j].(map[string]any)["name"].(string)
			})
		}
		sortArtifacts(inputArtifacts)
		sortArtifacts(argumentArtifacts)
		sortArtifacts(outputArtifacts)
		template := templates[i+1].(map[string]any)
		if policy.LogArchive != nil && plan.Compiler() == pipelineir.AdvancedCompilerVersion {
			p := policy.LogArchive
			u, err := url.Parse("https://" + p.Endpoint)
			if err != nil || u.Host != p.Endpoint || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !validator.IsArchivePath(p.Prefix) || !validator.IsArchiveSegment(p.Cluster) || !validator.IsDNS1123Subdomain(p.Bucket) || !validator.IsDNS1123Subdomain(p.Secret) {
				return fmt.Errorf("invalid native log archive policy")
			}
			template["archiveLocation"] = map[string]any{"archiveLogs": true, "s3": map[string]any{"endpoint": p.Endpoint, "bucket": p.Bucket, "region": p.Region, "insecure": p.Insecure, "key": strings.Join([]string{p.Prefix, p.Cluster, ref.Namespace, ref.InstanceID, "{{pod.name}}"}, "/"), "accessKeySecret": map[string]any{"name": p.Secret, "key": "accessKey"}, "secretKeySecret": map[string]any{"name": p.Secret, "key": "secretKey"}}}
		}
		if task.Retry != nil {
			r := task.Retry
			strategy := map[string]any{"limit": int64(r.Limit), "backoff": map[string]any{"duration": r.Duration, "factor": fmt.Sprint(r.Factor), "maxDuration": r.MaxDuration}}
			if r.Policy != "" {
				strategy["retryPolicy"] = r.Policy
			}
			template["retryStrategy"] = strategy
			for _, output := range outputArtifacts {
				artifact := output.(map[string]any)
				store := artifact["s3"].(map[string]any)
				store["key"] = store["key"].(string) + "/attempt-{{retries}}"
			}
		}
		container := template["container"].(map[string]any)
		container["command"] = []any{"python3", "-c", pipelineir.LauncherForCompiler(plan.Compiler())}
		container["args"] = []any{}
		container["env"] = env
		container["volumeMounts"] = []any{map[string]any{"name": "loom-data", "mountPath": "/tmp/loom"}}
		template["inputs"] = map[string]any{"parameters": inputParams, "artifacts": inputArtifacts}
		template["outputs"] = map[string]any{"parameters": []any{map[string]any{"name": "loom-bundle", "valueFrom": map[string]any{"path": "/tmp/loom/bundle"}}}, "artifacts": outputArtifacts}
		graph[i].(map[string]any)["arguments"] = map[string]any{"parameters": argumentParams, "artifacts": argumentArtifacts}
	}
	encoded, _ := json.Marshal(obj.Object)
	if len(encoded) > pipelineir.MaxIRBytes || strings.Contains(policy.ArtifactEndpoint, " ") {
		return fmt.Errorf("native Workflow exceeds size policy")
	}
	return nil
}
