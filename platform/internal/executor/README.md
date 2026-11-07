# ML executor bridge integration

The bridge is implemented, but has not been compiled, run, migrated, or tested.
The HTTP bridge itself does not invoke KFP, Trainer, or KServe, or resolve/fetch
user references. A separate built-in `pipeline-worker` now calls official KFP
v2 APIs using the same durable claim/renew/report service contract in-process.
See [KFP integration](../../docs/pipeline-kfp.md). With `pipeline.enabled=true`,
Pipeline submit/stop claims are reserved for that worker; external certificate
peers must omit those actions. Trainer/KServe still require external adapters.
Do not mix old external Pipeline writers with the built-in adapter during rollout.

## Private routes (registered in internal/route/route.go)

```go
func NewExecutor(api *handler.API, cfg config.Executor) http.Handler {
    mux := http.NewServeMux()
    mux.HandleFunc("POST /internal/v1/ml-executions/claim", api.ClaimMLExecution)
    mux.HandleFunc("POST /internal/v1/ml-executions/{oid}/renew", api.RenewMLExecution)
    mux.HandleFunc("POST /internal/v1/ml-executions/{oid}/report", api.ReportMLExecution)
    mux.HandleFunc("POST /internal/v1/ml-executions/{oid}/observe", api.ObserveMLExecution)
    mux.HandleFunc("GET /internal/v1/ml-executions/{oid}", api.MLExecutionOperation)
    return executor.Authenticate(cfg, mux)
}
```

Do not add these to `route.New`, use `API.Wrap`, or apply user authentication.
No user JWT, cookies, forwarded certificate headers, or identity tokens are used.
Each handler independently requires the private authenticator's certificate
context, a completed TLS handshake, and `r.TLS.VerifiedChains`. The authenticator
revalidates the leaf against the configured client CA and leaf SHA256 allowlist.

## Dedicated listener (assembled in runAPI)

When `cfg.Executor.Enabled`, first call
`tlsConfig, err := executor.NewTLSConfig(cfg.Executor)` and fail startup on error.
Create a separate `http.Server` using `cfg.Executor.Addr`,
`route.NewExecutor(api, cfg.Executor)`, and `TLSConfig: tlsConfig`.
Use `ListenAndServeTLS("", "")`, never `ListenAndServe` on that server. The
certificate/key are already loaded. TLS 1.3, verified client CA chains, and a
SHA256 pin of the **complete DER leaf certificate** are required. Pins accept hex
with optional colons. Multiple pins support rotation. `VerifyConnection` must not
check `HandshakeComplete`; only the HTTP middleware/handlers do that.

Configure read-header/read/write/idle timeouts and a bounded header size. Keep
the listener private at the network layer, with end-to-end TLS (no TLS-terminating
user gateway). Root config must contain `Executor Executor` with YAML key
`executor`; the type is supplied by `internal/config/executor.go`.

Run both servers under a cancellable child context. Set both servers' BaseContext
to that context, use an error channel large enough for both servers, and cancel
on parent cancellation or **either** listener failure. Shut down **both** servers
on every exit path, even a bind/TLS failure on the other listener. Use a fresh
background shutdown context with the configured timeout, not the canceled run
context. If Shutdown times out, call Close. Wait for listener goroutines to exit
before closing the DB. Ignore only `http.ErrServerClosed`. Do not log bodies,
lease-owner values, full commands, or adapter errors containing external data.

Migration `000011_ml_execution.sql` adds only the execution receipt table. It is
automatically embedded by the existing migration loader. Install it explicitly
after `000010_ml_workloads.sql`; never AutoMigrate or run migration on API startup.
`000013_ml_observation.sql` additionally installs space-identity fences and the
independent ongoing-observation receipt. It follows the integration-owned 000012.

## Final Go interfaces

```go
executor.NewTLSConfig(config.Executor) (*tls.Config, error)
executor.Authenticate(config.Executor, http.Handler) http.Handler
executor.PeerSHA256(*http.Request) (string, bool)

(*service.Service).ClaimMLExecution(context.Context, string, dto.ClaimMLExecutionReq) (*service.MLExecutionClaim, error)
(*service.Service).RenewMLExecution(context.Context, string, string, dto.MLExecutionLeaseReq) (*service.MLExecutionView, error)
(*service.Service).ReportMLExecution(context.Context, string, string, dto.ReportMLExecutionReq) (*service.MLExecutionView, error)
(*service.Service).MLExecutionOperation(context.Context, string, string) (*service.MLExecutionView, error)
(*service.Service).ObserveMLExecution(context.Context, string, string, dto.ObserveMLExecutionReq) (*service.MLExecutionView, error)
```

Service string arguments are verified peer leaf SHA256, then operation UUID where
applicable. Handler methods have the same five names and standard
`func(http.ResponseWriter, *http.Request)` signatures, not user Endpoint signatures.
Handler JSON responses are `{ "data": ... }`; claim returns 204 when empty.

## Adapter protocol and obligations

Claim body: `{ "cluster": "default", "actions": ["trainer.create"] }`.
Actions must be a nonempty, duplicate-free subset of `service.MLExecutorActions()`.
The response contains a typed `service.MLCommand`, random `leaseOwner`,
`leaseUntil`, `instanceId`, `revision`, `targetFence`, `sequence`, and
`idempotencyKey`. A claim only marks the operation RUNNING; it cannot fabricate a
RUNNING resource. Adapter reference resolution, grants, policy, and code/package
validation must complete before side effects. No URLs are fetched by this bridge.

Renew body contains `leaseOwner`, `instanceId`, `revision`, `targetFence`.
Leases last 60 seconds using the DB clock; renew well before expiration (for
example every 20 seconds). The lease is bound to its claiming leaf certificate.
An expired lease cannot renew, report new data, or become valid again. Claiming
again produces a new owner; rotating certificates requires a new claim.

Report extends the renew body with:

```json
{
  "reportId": "adapter-report-001",
  "sequence": 1,
  "outcome": "OBSERVED",
  "externalId": "opaque-run-id-or-kubernetes-uid",
  "observedState": "SUBMITTED",
  "resourceAbsent": false,
  "reason": ""
}
```

`sequence` must strictly increase for the instance+revision, including across
lease takeovers and retries. Assign sequence numbers to fresh observations in
observation order, not arrival order, and discard stale watch events locally.
After takeover start above the returned sequence and reconcile external state;
never replay a stale observation with a freshly allocated higher sequence.
External IDs are opaque 1-64 character ASCII catalog identifiers (letters,
digits, dot, dash, underscore), not URLs. Once bound they cannot change or be
cleared, even merely because a new revision was requested. Always echo the known
external ID, including error/deletion reports.

`OBSERVED` carries actual lifecycle evidence: UNKNOWN, SUBMITTED, PENDING,
RUNNING (jobs only), SERVING (KServe only), SUCCEEDED (jobs only), FAILED,
STOPPED, or DELETED. SUBMITTED is preserved in the machine receipt but maps to
PENDING in the domain. Successful submission and RUNNING observations do not
complete long-running training/pipeline operations. Continue renewing/observing
until actual job SUCCEEDED/FAILED or a superseding stop/delete invalidates the
lease. KServe SERVING confirms that the requested configuration is actually ready.
The adapter must verify object UID, instance ownership, and requested revision
before reporting it; merely finding an older object with the same name is not
proof that the command was applied.

DELETED requires the delete intent and `resourceAbsent: true`, after an actual
external lookup confirms the targeted resource no longer exists. A delete API
returning 202/acknowledged is insufficient. Pipeline/Trainer STOPPED may use
`resourceAbsent: true` for an explicit stop intent. **KServe STOPPED must not use
absence**: see the retained-UID contract below. Other observations cannot assert
absence. Existing terminal job states stay terminal while deletion is pending.
New revisions may redeploy KServe with its existing native UID; they cannot
replace that UID or restart a finished job under the old instance.

### KServe stop/deploy contract: retain the native UID

This implementation deliberately supports only a reversible, retained-native-UID
KServe stop. The adapter must stop serving while preserving the InferenceService
and its UID, then report STOPPED with `resourceAbsent: false` and the verified
nonempty original externalId. It must verify actual non-serving state, not merely
the acceptance of a scale/suspend request. If the deployed runtime cannot safely
stop serving without deleting the InferenceService, that stop mechanism is not
supported by this contract; report a canonical EXTERNAL_REJECTED failure instead
of claiming STOPPED. No particular unsupported native pause field is assumed.

`STOPPED + resourceAbsent: true` for KServe is rejected with INVALID_ARGUMENT
(HTTP 400), including when no external ID was ever bound or the current action is
an update of a stopped service. No receipt, observed state, applied revision, or
lease renewal is committed for that rejected report. A new KServe deploy revision
must retain and match the original UID. There is no automatic clear/rebind based
on STOPPED, a revision bump, RESOURCE_NOT_FOUND, or a changed external object UID.

If the native resource is absent (including a canceled create that never made an
InferenceService), do not fake a retained-UID stop. Use a canonical
RESOURCE_NOT_FOUND failure and explicitly delete the old business service using
the delete intent with verified `DELETED + resourceAbsent: true`; then create a
new business service/instance, whose first UID may be bound once. Echo any known
old UID in the deletion report. This also applies to historical absence-based
KServe stop receipts created before this restriction: exact receipt replays remain
readable, but do not grant a new-UID deploy. History and old receipts remain fixed.

Integration/Dirac handoff: `saveMLIntent` and `DeployKServe` must continue retaining
ExternalID; no modification or reset is required for this retained-UID option.
Do not clear it merely on a new revision. Supporting absence-based stop/redeploy
later would require explicit persisted absence evidence and an authorized,
transactional new-revision rebind contract; that functionality is not claimed
here. No new MLCommand field, route, or migration is introduced for this fix.

`RETRY` and `FAILED` require empty `observedState`, false `resourceAbsent`, and a
nonempty canonical reason. They preserve actual observed resource state. RETRY
releases the lease and schedules the operation after 15 seconds. FAILED ends the
operation and uses domain ERROR (not a fabricated resource failure) if the
resource was not already terminal. RETRY does not alter its lifecycle state.

Allowed reason values only: empty (OBSERVED only), REFERENCE_INVALID,
POLICY_DENIED, EXTERNAL_UNAVAILABLE, EXTERNAL_REJECTED, RESOURCE_FAILED,
RESOURCE_NOT_FOUND, TIMEOUT, INTERNAL_ERROR. There is no free-form error/message,
URL, stack, or log field; unknown JSON fields and unknown reason codes fail.

The operation, domain state, sanitized history/audit, and receipt commit in one
transaction. Domain/operation locks, lease ownership/expiry, pinned peer,
instance/revision/desired intent/last operation, immutable external ID, target
fence, and monotonic sequence are checked before accepting new observations.

If a response is lost, retry the **exact same** report or query
`GET /internal/v1/ml-executions/{oid}`. The latest receipt exposes reportId,
sequence, reportedAt, externalId, and status, but never a lease-owner credential
or command payload. Exact latest report replays perform no writes and remain
readable after completion, lease expiration, or supersession. Changed/stale
reports fail with 409; the query itself never grants authority to continue work.
If a claim response is lost, let its lease expire and reconcile on a new claim.

## Current authorization and namespace identity

Claim, renew, and every new execution report take a shared lock on the same
authorization sentinel used exclusively by role/member/user/lifecycle mutations.
Thus permission edits cannot interleave between the execution checks and commit.
Commands must have `requestedBy == operation.actorId`; no fallback to historical
creator ownership, anonymous machine actors, or a stale user token exists.
For create/run/update/deploy, the requester must still be ACTIVE and not deleted,
the space must be ACTIVE, and the current action and object-level ownership or
`*.read.any` permissions must still hold. Pipeline submit also checks the current
parent definition's ownership and pipeline.run permission. This uses the shared
`mlActionPermission` mapping. Updating a STOPPED KServe service is not exempt.

Only explicit stop/delete actions bypass requester revocation/ACTIVE-space checks
so already-authorized cleanup can progress in frozen spaces. Cleanup still
checks group existence, undeleted identity, cluster/namespace and immutable
Profile/namespace UIDs. This does not loosen public stop/delete intent APIs.
Claimed responses now include `space: {instanceId, namespaceUid, profileUid}`;
the adapter must verify these against external objects before all I/O. Receipt
space identity is bound on first claim and cannot change on subsequent claims,
renewals, reports, or observations. Missing identities fail closed. An auth-denied
claim ends only the operation (POLICY_DENIED), not the real resource. A denied
renew/report fails without extending the lease; the adapter must stop side
effects immediately and use a distinct authorized stop/delete intent for cleanup.

No DB lock can cancel I/O already in flight at revocation time. These checks are
point-in-time grants, not an atomic transaction with KFP/Kubernetes. Renew before
I/O and fence/drain stale external side effects as described below.

## Ongoing KServe observation after deploy completion

The integration owner adds the private `POST .../{oid}/observe` route above.
It uses the same mTLS authenticator, **not** user auth and not a lease owner.
Body (`sequence` belongs to a separate stream, not execution report.sequence):

```json
{
  "instanceId": "resource-instance-uuid",
  "revision": 1,
  "externalId": "immutable-kubernetes-uid",
  "targetFence": "returned-target-fence",
  "sequence": 1,
  "reportId": "health-001",
  "observedState": "SERVING",
  "reason": ""
}
```

This only accepts the latest successful KServe SERVING-intent operation with
the current instance, revision, external ID, applied revision, target fence,
last-operation pointer, and original space UID identities still matching. It
cannot bind/replace identity, create, deploy, delete, change desired state, renew
leases, or reopen the SUCCEEDED operation. Frozen/revoked requesters do not stop
**evidence-only** health observation; observations do not restore their access.

Allowed runtime states: SERVING, PENDING, UNKNOWN, ERROR, FAILED. ERROR and FAILED
require a canonical reason from the existing allowlist. ERROR denotes a transient
observation/runtime problem and maps domain observedState to UNKNOWN/status ERROR;
SERVING/PENDING/UNKNOWN/ERROR may change with current health. FAILED is a confirmed
terminal resource state and cannot revert within the same revision. Do not use
FAILED for temporary readiness loss; that requires ERROR/PENDING. Explicit
stop/delete/redeploy immediately fences the old observe stream. Missing resources
may be reported UNKNOWN or ERROR with RESOURCE_NOT_FOUND, never inferred DELETED.

Domain observedAt and runtime status, append-only history, and the independent
watch receipt update in one transaction with identity and sequence CAS. GET
operation and observe responses include
`observation: {sequence, reportId, observedState, observedAt}`. The execution
receipt (`status`, top-level `sequence`, `observedState`, `reportedAt`) remains the
original completion evidence. Repeat the exact latest observation to recover a
lost response without refreshing observedAt. Changed or older sequences fail 409.
Use fresh evidence and serialize sequence allocation across watcher replicas;
query observation.sequence after restart and discard old watch events. Any pinned
trusted adapter may observe, so certificate rotation does not require a new deploy.

Adapters must schedule the ongoing polls/watches; this API is a bridge, not a
background KServe client. Consumers must interpret domain observedAt freshness
before treating a stored SERVING value as current readiness; no new user access
endpoint or freshness projection was added in this bridge-owned change. Without
incoming observations timestamps intentionally age rather than being fabricated.

Upgrade behavior: pre-000013 leases with no stored space identity cannot renew or
report; reclaims bind identity only after their old lease expires. Already
SUCCEEDED pre-000013 receipts cannot be blindly upgraded from the current group
UIDs because they do not prove the original namespace identity. Explicitly
reconcile/redeploy a new revision before using ongoing observation for those
receipts. No migration was run and no historical identity backfill was guessed.

## Cross-system limitation: not an atomic external transaction

The stable idempotency key is the lowercase SHA256 hex digest of compact JSON:
`["loom-ml-v1", cluster, targetType, instanceId, revision]`. Use the **returned**
key rather than reimplementing serialization. It is unchanged on lease takeover
or operation retry. The opaque targetFence additionally binds the operation,
resource/group identity, namespace, name, desired state, and action.

DB fencing prevents late bridge writes, not late KFP/Kubernetes side effects.
The trusted adapter MUST persist deduplication by this stable key, serialize
side effects per cluster/target/instance, fence out old revisions/owners locally,
and check the live lease/target immediately before and after external I/O. Stop
side effects immediately when renewal fails. Use deterministic names/labels,
UID/resourceVersion preconditions and durable ownership mappings, and reconcile
an uncertain create result before any retry. A KFP submit endpoint without an
atomic idempotency facility needs adapter-side durable reconciliation; this
bridge cannot guarantee exactly-once submission. If an old in-flight create can
complete after a superseding stop/delete, the adapter must find and stop/delete
that late object, and must not confirm absence until it has fenced/drained such
creations. Do not treat a transaction, a lease, or a pre-I/O renewal as closing
that cross-system race.
