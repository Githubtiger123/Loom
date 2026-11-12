package pipelineir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Like upstream cacheutils, the identity covers image+argv, typed output
// contract and resolved parameters/artifacts, never output storage URIs. This
// adapter has its own version: it does not claim byte-identical KFP cache keys.
func CacheIdentity(task Task) string {
	raw, _ := json.Marshal(struct {
		Protocol        string
		Image           string
		Command, Args   []string
		Inputs, Outputs Ports
	}{AdvancedCompilerVersion, task.Image, task.Command, task.Args, task.Runtime.Inputs, task.Runtime.Outputs})
	return hash(raw)
}
func CacheFingerprint(identity, values, artifacts string) string {
	sum := sha256.Sum256([]byte(identity + ":" + values + ":" + artifacts))
	return hex.EncodeToString(sum[:])
}
