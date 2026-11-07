package handler

import (
	"context"
	"net/http"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func modelPathIDs(r *http.Request, child ...string) ([]uint64, error) {
	keys := append([]string{"gid"}, child...)
	ids := make([]uint64, 0, len(keys))
	for _, key := range keys {
		id, err := pathID(r, key)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (a *API) Models(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r)
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.Models(ctx, u.ID, ids[0], q)
}

func (a *API) CreateModel(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r)
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.CreateModelReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.CreateModel(ctx, u.ID, ids[0], requestKey(r), in)
}

func (a *API) Model(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid")
	if err != nil {
		return nil, err
	}
	return a.service.Model(ctx, u.ID, ids[0], ids[1])
}

func (a *API) UpdateModel(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.UpdateModelReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.UpdateModel(ctx, u.ID, ids[0], ids[1], requestKey(r), in)
}

func (a *API) ArchiveModel(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.ArchiveModelObjectReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.ArchiveModel(ctx, u.ID, ids[0], ids[1], requestKey(r), in)
}

func (a *API) ModelVersions(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.ModelVersions(ctx, u.ID, ids[0], ids[1], q)
}

func (a *API) CreateModelVersion(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.CreateModelVersionReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.CreateModelVersion(ctx, u.ID, ids[0], ids[1], requestKey(r), in)
}

func (a *API) ModelVersion(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid", "vid")
	if err != nil {
		return nil, err
	}
	return a.service.ModelVersion(ctx, u.ID, ids[0], ids[1], ids[2])
}

func (a *API) UpdateModelVersion(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid", "vid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.UpdateModelVersionReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.UpdateModelVersion(ctx, u.ID, ids[0], ids[1], ids[2], requestKey(r), in)
}

func (a *API) ArchiveModelVersion(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid", "vid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.ArchiveModelObjectReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.ArchiveModelVersion(ctx, u.ID, ids[0], ids[1], ids[2], requestKey(r), in)
}

func (a *API) ModelArtifacts(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid", "vid")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.ModelArtifacts(ctx, u.ID, ids[0], ids[1], ids[2], q)
}

func (a *API) CreateModelArtifact(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid", "vid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.CreateModelArtifactReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.CreateModelArtifact(ctx, u.ID, ids[0], ids[1], ids[2], requestKey(r), in)
}

func (a *API) ModelArtifact(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid", "vid", "aid")
	if err != nil {
		return nil, err
	}
	return a.service.ModelArtifact(ctx, u.ID, ids[0], ids[1], ids[2], ids[3])
}

func (a *API) ArchiveModelArtifact(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	ids, err := modelPathIDs(r, "mid", "vid", "aid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.ArchiveModelObjectReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.ArchiveModelArtifact(ctx, u.ID, ids[0], ids[1], ids[2], ids[3], requestKey(r), in)
}
