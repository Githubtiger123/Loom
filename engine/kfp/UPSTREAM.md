# Official KFP engine dependency

Upstream: `github.com/kubeflow/pipelines`, Apache-2.0.
Pinned revision: `555bfa60a70da88c594960bdacfd78fa7c061043`.

This module imports the real upstream `argocompiler`, `component`, `metadata`,
`cacheutils`, `client_manager`, and generated PipelineSpec packages. It does not
copy/reimplement semantic compilation, Python launching, or CEL-to-Argo rules.
The API and kubernetes_platform nested modules are pinned explicitly because
replace directives in an upstream dependency's go.mod do not propagate.

The inspected checkout requires Go 1.27.0/toolchain 1.27.3. Module versions,
checksums, and the transitive build graph must be resolved before a verified
build. Dependency-only tidy was attempted: goproxy.cn returned a sumdb 404 for
the pinned nested modules, then proxy.golang.org timed out. Lockfiles remain
unresolved; checksum verification was not disabled. No build/runtime verification
has been performed. Local sibling `E:/kubeflow/pipelines` is not a runtime or
Docker dependency; no absolute-path replace directive is installed.

Current seams:

- `Compile`: calls official argocompiler with explicit runtime images and root.
- `ExecuteComponent`: calls official launcher with injected client interfaces.
- `Backend`: authenticated execution/persistence boundary, not yet implemented.

Remaining before execution: driver concrete-client interface extraction, metadata
constructors, scoped Runtime API/GORM adapters, workload auth, official node
projection, runtime image builds and release manifests. No unmodified official
binary is advertised as a Loom-connected runtime. Do not enable native submission
until the backend is installed and validated. No fake metadata/cache stubs exist.
