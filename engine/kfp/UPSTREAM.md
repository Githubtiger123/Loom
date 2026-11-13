# Official KFP engine provenance

Upstream: https://github.com/kubeflow/pipelines (Apache-2.0).
Pinned revision: `555bfa60a70da88c594960bdacfd78fa7c061043`.
Go module pseudo-version: `v0.0.0-20261027233236-555bfa60a70d`.
Inspected upstream requires Go 1.27.0 / toolchain 1.27.3.

## Direct packages versus retained source

Direct imports: official argocompiler, generated PipelineSpec/MLMD protobufs,
expression helpers, objectstore, plugin/cache interfaces and other common packages.
The API and kubernetes_platform nested modules are explicitly pinned because a
dependency's replace directives do not propagate.

Driver, launcher and metadata are **not simply imported unchanged**. Upstream's
driver accepts concrete metadata clients, and Pipeline/Execution have private
context fields. The minimum relevant official files are retained locally so the
runtime can inject scoped Loom transport instead of MLMD/KFP RPC:

- Eight non-test driver files; metadata interface injection and explicit ROOT_DAG
  storage policy. No second lowering/expression/iteration implementation.
- Three component files; local metadata/client-manager imports, NoOpDispatcher,
  early returns after failed DAG/context lookups, committed-publication replay
  guard to avoid rerunning user code.
- Three metadata files retaining official types/conversion; no runtime RPC client
  construction. The upstream NewTestClient helper is removed.
- Official driver/launcher CLI source with flags/output/copy contracts preserved;
  inject Runtime API client manager and reject unsupported importer/platform.
- Extracted official cache key/fingerprint methods, config-to-protobuf conversion,
  and DAG indexing/aggregation, avoiding RPC persistence constructors.

Local additions include metadata constructors/oneof codecs, runtime transport,
scoped metadata/cache clients and trusted compiler lifecycle policy.
Payload debug dumps have been removed in selected paths; remaining upstream
errors/logging are not certified secret-free and still require release review.

## Traceability

`UPSTREAM-SOURCES.json` lists each full upstream input hash, destination,
extracted methods and local modifications. SHA-256 inputs are UTF-8 with CRLF
normalized to LF, no other whitespace/newline changes. Original notices are
retained and changed files have a Loom notice; LICENSE contains Apache-2.0.

Review the manifest and upstream diff on every bump. There is no absolute-path
replace or build/runtime dependency on a sibling checkout. Upstream source hashes
are not Go module checksums; neither replaces sumdb verification.

## Release status

Source adapters, runtime routes/CLI/image definitions, frozen submission, official
record projection, terminal evidence, verified retry epochs/replay, scoped cache,
offline logs, directory downloads and model import are wired in source. Explicit
native execution/observer configuration registers the concrete backend. All
runtime/native/cache defaults remain disabled. Ready/runnable reflect local
configuration/dependency checks, not live runtime health or verified SDK parity.

Cache retains the official fingerprint algorithm, but its storage/authorization
protocol is Loom-specific: candidate-to-visible only after Workflow success,
creator/pipeline/namespace-incarnation isolation, conditional inventory copying,
seven-day retention and frozen hit/miss on task retry. Executor logs are excluded
from cache reuse. No MLMD or KFP backend is instantiated by this runtime.

Dependency-only go mod tidy completed in both engine/kfp and platform with checksum
verification enabled. No project build, test, migration, deployment or runtime
verification has been performed under the repository rules. Admission/default
compatibility, storage immutability/retention assumptions and recovery still need
release validation. See integration document section 16 for authoritative status.
