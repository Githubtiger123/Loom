package dto

import (
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type LifecycleReq struct {
	Revision    uint64 `json:"revision"`
	ConfirmName string `json:"confirmName"`
	Reason      string `json:"reason"`
}

func (r LifecycleReq) Validate() error {
	if r.Revision == 0 || !validator.RequiredText(r.Reason, 1000) || !validator.MaxTextLength(r.ConfirmName, 63) {
		return apperr.New(apperr.CodeInvalid, "必须提供 revision 和处置原因，原因最长 1000 字符")
	}
	return nil
}

type TransferResourceReq struct {
	Revision uint64 `json:"revision"`
	UserID   uint64 `json:"userId"`
	Reason   string `json:"reason"`
}

func (r TransferResourceReq) Validate() error {
	if r.Revision == 0 || r.UserID == 0 || !validator.RequiredText(r.Reason, 1000) {
		return apperr.New(apperr.CodeInvalid, "必须提供 revision、目标用户和转移原因")
	}
	return nil
}
