package pipelineir

import (
	"bytes"
	"encoding/json"
	"math"

	"github.com/githubtiger123/loom/platform/internal/thirdparty/kfp/pipelinespec"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	"google.golang.org/protobuf/encoding/protojson"
)

// Never reinterpret v1/v2 attempts with new artifact or retry semantics.
const AdvancedCompilerVersion = "loom-native-kfp-v3"
const AdvancedOutputProtocol = "loom-python-output-v2"
const MaxTaskRetries = 10

// RetryStrategy follows KFP's argocompiler: the executor template retries, not
// the DAG/driver. Values are frozen after validating protobuf durations.
type RetryPolicy struct {
	Limit       int32   `json:"limit"`
	Duration    string  `json:"duration"`
	Factor      float64 `json:"factor"`
	MaxDuration string  `json:"maxDuration"`
	Policy      string  `json:"policy,omitempty"`
}

func OutputProtocol(compiler string) string {
	if compiler == AdvancedCompilerVersion {
		return AdvancedOutputProtocol
	}
	return PythonOutputProtocol
}

func AnalyzePython(raw json.RawMessage) (*Plan, *Report) {
	bad := &Report{CompilerVersion: AdvancedCompilerVersion, SchemaVersion: SchemaVersion,
		ValidationMode: "KFP_V3_PREFLIGHT", ExecutionReason: "OPERATOR_ENABLEMENT_REQUIRED", Issues: []Issue{}}
	if !validator.IsBoundedJSONObject(raw, MaxIRBytes, 32, 60000) || !validator.HasLosslessJSONIntegerLiterals(raw) {
		bad.add("INVALID_OR_UNBOUNDED_IR", "/")
		return nil, bad
	}
	spec := &pipelinespec.PipelineSpec{}
	if protojson.Unmarshal(raw, spec) != nil {
		bad.add("KFP_PIPELINE_SPEC_INVALID", "/")
		return nil, bad
	}
	deploymentJSON, err := protojson.Marshal(spec.GetDeploymentSpec())
	if err != nil {
		bad.add("KFP_DEPLOYMENT_SPEC_INVALID", "/")
		return nil, bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(deploymentJSON, &fields) != nil || fields == nil {
		bad.add("KFP_DEPLOYMENT_SPEC_INVALID", "/")
		return nil, bad
	}
	delete(fields, "@type")
	deploymentJSON, _ = json.Marshal(fields)
	deploy := &pipelinespec.PipelineDeploymentConfig{}
	if protojson.Unmarshal(deploymentJSON, deploy) != nil || len(deploy.GetExecutors()) > MaxTasks || len(spec.GetComponents()) > MaxTasks {
		bad.add("KFP_DEPLOYMENT_SPEC_INVALID", "/")
		return nil, bad
	}
	var canonical any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	_ = d.Decode(&canonical)
	encoded, _ := json.Marshal(canonical)
	bad.IRDigest = hash(encoded)
	return lowerAdvanced(spec, deploy, bad)
}

func lowerRetry(p *pipelinespec.PipelineTaskSpec_RetryPolicy, path string, bad *Report) *RetryPolicy {
	if p != nil {
		officialFields(p, path, bad, "maxRetryCount", "backoffDuration", "backoffFactor", "backoffMaxDuration", "policy")
		r := &RetryPolicy{Limit: p.GetMaxRetryCount(), Duration: "0", Factor: p.GetBackoffFactor(), MaxDuration: "3600"}
		if r.Factor == 0 {
			r.Factor = 2
		}
		if r.Limit < 0 || r.Limit > MaxTaskRetries || math.IsNaN(r.Factor) || math.IsInf(r.Factor, 0) || r.Factor < 1 || r.Factor > 100 {
			bad.add("RETRY_POLICY_INVALID", path)
		}
		if d := p.GetBackoffDuration(); d != nil {
			if d.CheckValid() != nil || d.Seconds < 0 || d.Seconds > 86400 || d.Nanos != 0 {
				bad.add("RETRY_DURATION_INVALID", path)
			}
			r.Duration = jsonNumber(d.Seconds)
		}
		if d := p.GetBackoffMaxDuration(); d != nil {
			if d.CheckValid() != nil || d.Seconds < 1 || d.Seconds > 86400 || d.Nanos != 0 {
				bad.add("RETRY_DURATION_INVALID", path)
			}
			r.MaxDuration = jsonNumber(d.Seconds)
		}
		switch p.GetPolicy() {
		case pipelinespec.PipelineTaskSpec_RetryPolicy_POLICY_UNSPECIFIED:
		case pipelinespec.PipelineTaskSpec_RetryPolicy_POLICY_ALWAYS:
			r.Policy = "Always"
		case pipelinespec.PipelineTaskSpec_RetryPolicy_POLICY_ON_FAILURE:
			r.Policy = "OnFailure"
		case pipelinespec.PipelineTaskSpec_RetryPolicy_POLICY_ON_ERROR:
			r.Policy = "OnError"
		case pipelinespec.PipelineTaskSpec_RetryPolicy_POLICY_ON_TRANSIENT_ERROR:
			r.Policy = "OnTransientError"
		default:
			bad.add("RETRY_POLICY_INVALID", path)
		}
		return r
	}
	return nil
}

func jsonNumber(n int64) string { data, _ := json.Marshal(n); return string(data) }
