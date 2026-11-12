package dto

import (
	"encoding/json"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type KServePredictReq struct {
	Revision   uint64          `json:"revision"`
	InstanceID string          `json:"instanceId"`
	Payload    json.RawMessage `json:"payload"`
}

func (r KServePredictReq) Validate() error {
	if r.Revision == 0 || !validator.IsCanonicalUUID(r.InstanceID) || len(r.Payload) == 0 || len(r.Payload) > 256<<10 || !json.Valid(r.Payload) {
		return apperr.New(apperr.CodeInvalid, "需要服务 revision、instanceId 和有效推理 JSON（最长 256 KiB）")
	}
	return nil
}
