package k8s

import (
	"fmt"
)

type Pending struct{ Step, Reason string }

func (e *Pending) Error() string { return e.Reason }

type Permanent struct{ Reason string }

func (e *Permanent) Error() string { return e.Reason }
func conflict(format string, args ...any) error {
	return &Permanent{Reason: fmt.Sprintf(format, args...)}
}
func waiting(step, reason string) error { return &Pending{Step: step, Reason: reason} }

const (
	labelGroupID       = "loom.ml/group-id"
	labelUserID        = "loom.ml/user-id"
	annotationInstance = "loom.ml/instance-id"
	annotationVersion  = "loom.ml/access-version"
)

type SpaceRef struct {
	ID                       uint64
	InstanceID, Name, Owner  string
	ProfileUID, NamespaceUID string
}

type ObjectRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	UID  string `json:"uid"`
	Role string `json:"role"`
}

type SpaceObservation struct {
	ProfileUID   string            `json:"profileUid"`
	NamespaceUID string            `json:"namespaceUid"`
	Quota        map[string]string `json:"quota"`
}
