package dto

import (
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type ResourceBudget struct {
	CPUMilli    int64 `json:"cpuMilli"`
	MemoryBytes int64 `json:"memoryBytes"`
	GPU         int64 `json:"gpu"`
}

func (r ResourceBudget) Valid() bool { return r.CPUMilli >= 0 && r.MemoryBytes >= 0 && r.GPU >= 0 }

type UpdateGroupQuotaReq struct {
	Revision uint64 `json:"revision"`
	Quota    Quota  `json:"quota"`
	Reason   string `json:"reason"`
}

func (r UpdateGroupQuotaReq) Validate() error {
	_, cpu := validator.QuantityUnits(r.Quota.CPU, 1000)
	_, memory := validator.QuantityUnits(r.Quota.Memory, 1)
	if r.Revision == 0 || !cpu || !memory || r.Quota.GPU < 0 || !validator.RequiredText(r.Reason, 1000) {
		return apperr.New(apperr.CodeInvalid, "额度需提供 revision、正 CPU/内存、非负 GPU 和原因；不能清空额度约束")
	}
	return nil
}
