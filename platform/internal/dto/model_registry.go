package dto

import (
	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
)

type CreateModelReq struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

type UpdateModelReq struct {
	Revision    uint64  `json:"revision"`
	DisplayName *string `json:"displayName,omitempty"`
	Description *string `json:"description,omitempty"`
}

type CreateModelVersionReq struct {
	Revision            uint64  `json:"revision"`
	Name                string  `json:"name"`
	Description         string  `json:"description"`
	SourcePipelineRunID *uint64 `json:"sourcePipelineRunId,omitempty"`
}

type UpdateModelVersionReq struct {
	Revision    uint64  `json:"revision"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
}

type CreateModelArtifactReq struct {
	Revision           uint64  `json:"revision"`
	Name               string  `json:"name"`
	URI                string  `json:"uri"`
	ModelFormat        string  `json:"modelFormat"`
	ModelFormatVersion string  `json:"modelFormatVersion,omitempty"`
	Digest             string  `json:"digest,omitempty"`
	SizeBytes          *uint64 `json:"sizeBytes,omitempty"`
	StorageType        string  `json:"storageType"`
}

type ArchiveModelObjectReq struct {
	Revision uint64 `json:"revision"`
}

// Only opaque run/artifact identities are accepted; storage authority is resolved
// from authorized published task outputs on the backend.
type ImportPipelineModelReq struct {
	Revision           uint64 `json:"revision"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	RunID              uint64 `json:"runId"`
	InstanceID         string `json:"instanceId"`
	ArtifactID         string `json:"artifactId"`
	ArtifactName       string `json:"artifactName"`
	FileName           string `json:"fileName"`
	ModelFormat        string `json:"modelFormat"`
	ModelFormatVersion string `json:"modelFormatVersion"`
}

func (r ImportPipelineModelReq) Validate() error {
	if r.Revision == 0 || r.RunID == 0 || !validator.IsCanonicalUUID(r.InstanceID) || !validator.IsMetadataID(r.ArtifactID) ||
		!validator.IsModelRegistryName(r.Name) || !validator.IsModelRegistryName(r.ArtifactName) || !validator.IsModelRegistryName(r.FileName) ||
		!validator.MaxTextLength(r.Description, 2000) || !validator.IsModelArtifactFormat(r.ModelFormat) || !validator.IsModelArtifactFormatVersion(r.ModelFormatVersion) {
		return modelRegistryInvalid("请提供模型 revision、有效的运行/产物标识、版本名、产物名、文件名和模型格式")
	}
	return nil
}

func modelRegistryInvalid(message string) error {
	return apperr.New(apperr.CodeInvalid, message)
}

func (r CreateModelReq) Validate() error {
	if !validator.IsModelRegistryName(r.Name) || !validator.MaxTextLength(r.DisplayName, 128) ||
		!validator.MaxTextLength(r.Description, 2000) {
		return modelRegistryInvalid("名称须为最长 63 字符的字母、数字、点、下划线或连字符，显示名/描述最长 128/2000 字")
	}
	return nil
}

func (r UpdateModelReq) Validate() error {
	if r.Revision == 0 || (r.DisplayName == nil && r.Description == nil) {
		return modelRegistryInvalid("必须提供 revision 和至少一个修改字段")
	}
	if (r.DisplayName != nil && !validator.MaxTextLength(*r.DisplayName, 128)) ||
		(r.Description != nil && !validator.MaxTextLength(*r.Description, 2000)) {
		return modelRegistryInvalid("显示名/描述最长 128/2000 字")
	}
	return nil
}

func (r CreateModelVersionReq) Validate() error {
	if r.Revision == 0 || !validator.IsModelRegistryName(r.Name) || !validator.MaxTextLength(r.Description, 2000) {
		return modelRegistryInvalid("必须提供模型 revision、有效版本名，描述最长 2000 字")
	}
	if r.SourcePipelineRunID != nil && *r.SourcePipelineRunID == 0 {
		return modelRegistryInvalid("sourcePipelineRunId 必须为有效 ID")
	}
	return nil
}

func (r UpdateModelVersionReq) Validate() error {
	if r.Revision == 0 || (r.Description == nil && r.Status == nil) {
		return modelRegistryInvalid("必须提供 revision 和至少一个修改字段")
	}
	if r.Description != nil && !validator.MaxTextLength(*r.Description, 2000) {
		return modelRegistryInvalid("版本描述最长 2000 字")
	}
	if r.Status != nil && *r.Status != "DRAFT" && *r.Status != "READY" {
		return modelRegistryInvalid("版本状态须为 DRAFT 或 READY")
	}
	return nil
}

func (r CreateModelArtifactReq) Validate() error {
	if r.Revision == 0 || !validator.IsModelRegistryName(r.Name) ||
		!validator.IsModelArtifactURI(r.URI, r.StorageType) ||
		!validator.IsModelArtifactFormat(r.ModelFormat) ||
		!validator.IsModelArtifactFormatVersion(r.ModelFormatVersion) ||
		(r.Digest != "" && !validator.IsSHA256(r.Digest)) {
		return modelRegistryInvalid("必须提供版本 revision；产物名称、存储地址、格式、格式版本或 SHA256 无效")
	}
	return nil
}

func (r ArchiveModelObjectReq) Validate() error {
	if r.Revision == 0 {
		return modelRegistryInvalid("必须提供 revision")
	}
	return nil
}
