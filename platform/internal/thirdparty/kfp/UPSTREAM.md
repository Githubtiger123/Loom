# Kubeflow Pipelines source provenance

- Project: https://github.com/kubeflow/pipelines
- Revision: `555bfa60a70da88c594960bdacfd78fa7c061043`
- License: Apache-2.0; the full license is included in `LICENSE`.
- Copied reference files were unmodified in the local upstream checkout.

## Included source and modifications

| Loom path | Upstream path | Modifications |
| --- | --- | --- |
| `pipelinespec/pipeline_spec.pb.go` | `api/v2alpha1/go/pipelinespec/pipeline_spec.pb.go` | None; generated code is copied, never hand-edited. |
| `pipelinespec/pipeline_spec.proto` | `api/v2alpha1/pipeline_spec.proto` | None; retained as the schema source. |
| `compiler/visitor.go` | `backend/src/v2/compiler/visitor.go` | Local import path; extracted `AcceptSpec` entry point with argument checks, avoiding a redundant PipelineJob/Struct round trip. Traversal logic is retained. |
| `expression/expression.go` | `backend/src/v2/expression/expression.go`; `backend/src/v2/metadata/converter.go` | Official CEL environments/evaluator retained; local schema import, extracted `PbValueToText`, and explicit environment initialization error handling remove the MLMD dependency. |
| `expression/compile.go` | Loom adapter using the above environments | Exposes checked ASTs for bounded CEL-to-Argo expression lowering; this is not upstream source. |
| `../../infra/pipelineir/upstream/output_parameters.py` | `sdk/python/kfp/local/executor_output_utils.py` | Extracted `special_dsl_outputpath_read` and `cast_protobuf_numbers`; protobuf enums/specs replaced with IR string/dict arguments; UTF-8 file reads; reject bool/fraction coercion into integer outputs. |

The protobuf-generated Go file imports `google.golang.org/genproto/googleapis/rpc`.
Its module version and checksum entries are copied from this same upstream
revision's `go.mod`/`go.sum`; dependencies have not been downloaded or compiled
as part of this source-only change.

## What this does not vendor

KFP's full `argocompiler`, driver, launcher, MLMD integration, cache and API server
are **not** included. Their interfaces depend on KFP execution/metadata services.
The restricted native Argo lowering pass, frozen submission/UID fencing,
parameter resolution, file transport/storage policy and SQL publication remain
Loom adaptations. The component command still invokes the SDK's real
`kfp.dsl.executor_main`; Loom does not reimplement Python function invocation.

The visitor has an upstream TODO for component-reference cycle detection. Loom
enforces flat, leaf-only components before calling it. Do not remove that guard
when adding nested DAG support without adding cycle handling first.

The v3 adapter uses a recursion-stack-checked traversal of the official
protobuf DAG for nested components. The v2 visitor and its flat guard remain
unchanged for historical replay. CEL evaluation environments are reused, but
only a checked subset is lowered to Argo expr. Unsupported expressions fail
preflight instead of being passed to a shell or silently reinterpreted.

Retry mapping and cache identity fields were compared with upstream
`backend/src/v2/compiler/argocompiler` and `backend/src/v2/cacheutils/cache.go`.
Cache RPC/MLMD code is not vendored. Loom uses a distinct fingerprint protocol,
its existing task/artifact identities, SQL cache entries and Argo memoization.
It is not wire-compatible with KFP cache keys or its cache database.

Argo's implementation was inspected at tag `v4.1.6`, notably
`workflow/controller/operator.go`, `workflow/controller/dag.go`,
`workflow/controller/cache/configmap_cache.go` and the Workflow API types.
The projection, retry/loop node matcher and shutdown adapter implement those
public protocols; no Argo controller source is copied into Loom.

## Updating

Update this revision and the file mapping together. Replace generated schema
files from one upstream revision as a pair; do not patch generated Go types.
Review upstream visitor and output utility differences separately from Loom's
policy. Changes to normalized IR meaning, launcher bytes, or emitted Workflow
must use a new persisted compiler version; do not change legacy-v1 replay.
