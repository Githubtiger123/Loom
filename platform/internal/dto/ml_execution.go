package dto

// These requests are machine-only. No request field can choose an actor/user.
type ClaimMLExecutionReq struct {
	Cluster string   `json:"cluster"`
	Actions []string `json:"actions"`
}

type MLExecutionLeaseReq struct {
	LeaseOwner  string `json:"leaseOwner"`
	InstanceID  string `json:"instanceId"`
	Revision    uint64 `json:"revision"`
	TargetFence string `json:"targetFence"`
}

// Outcome is OBSERVED, RETRY, or FAILED. Only OBSERVED carries an observation.
// Reason is a fixed service-defined code, never free-form logs/errors/URLs.
// Sequence increases across lease takeovers, and ReportID identifies a response
// whose delivery may have been lost. Neither field is an authentication token.
type ReportMLExecutionReq struct {
	MLExecutionLeaseReq
	ReportID       string `json:"reportId"`
	Sequence       uint64 `json:"sequence"`
	Outcome        string `json:"outcome"`
	ExternalID     string `json:"externalId"`
	ObservedState  string `json:"observedState"`
	ResourceAbsent bool   `json:"resourceAbsent"`
	Reason         string `json:"reason"`
}

// ObserveMLExecutionReq is read-only runtime evidence for an already successful
// KServe deployment, not an execution grant. Sequence is independent of report's
// execution sequence. No actor, intent, credentials, or replacement UID allowed.
type ObserveMLExecutionReq struct {
	InstanceID    string `json:"instanceId"`
	Revision      uint64 `json:"revision"`
	ExternalID    string `json:"externalId"`
	TargetFence   string `json:"targetFence"`
	Sequence      uint64 `json:"sequence"`
	ReportID      string `json:"reportId"`
	ObservedState string `json:"observedState"`
	Reason        string `json:"reason"`
}
