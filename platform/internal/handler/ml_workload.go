package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

// Route path variable contract: gid; pid (definition); vid (version);
// rid (pipeline run); jid (trainer job); sid (KServe service). No registration here.
func mlIDs(r *http.Request, name string) (uint64, uint64, error) {
	groupID, err := pathID(r, "gid")
	if err != nil {
		return 0, 0, err
	}
	if name == "" {
		return groupID, 0, nil
	}
	id, err := pathID(r, name)
	return groupID, id, err
}

func mlQuery(r *http.Request) (dto.MLListQuery, error) {
	after, limit, err := page(r)
	if err != nil {
		return dto.MLListQuery{}, err
	}
	values := r.URL.Query()
	q := dto.MLListQuery{After: after, Limit: limit, Scope: values.Get("scope")}
	if values.Has("includeDeleted") {
		q.IncludeDeleted, err = strconv.ParseBool(values.Get("includeDeleted"))
		if err != nil {
			return q, apperr.New(apperr.CodeInvalid, "includeDeleted 必须为布尔值")
		}
	}
	return q, q.Validate()
}

func mlWrite[T any](ctx context.Context, u *model.User, r *http.Request, path string,
	fn func(context.Context, uint64, uint64, uint64, string, T) (*model.Operation, error)) (any, error) {
	groupID, id, err := mlIDs(r, path)
	if err != nil {
		return nil, err
	}
	in, err := decode[T](r)
	if err != nil {
		return nil, err
	}
	return fn(ctx, u.ID, groupID, id, requestKey(r), in)
}

func (a *API) Pipelines(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, _, err := mlIDs(r, "")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.Pipelines(ctx, u.ID, groupID, q)
}

func (a *API) CreatePipeline(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "", func(ctx context.Context, actor, gid, _ uint64, key string, in dto.CreatePipelineReq) (*model.Operation, error) {
		return a.service.CreatePipeline(ctx, actor, gid, key, in)
	})
}

func (a *API) Pipeline(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "pid")
	if err != nil {
		return nil, err
	}
	return a.service.Pipeline(ctx, u.ID, groupID, id)
}

func (a *API) PipelineVersions(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "pid")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.PipelineVersions(ctx, u.ID, groupID, id, q)
}

func (a *API) PipelineVersion(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "pid")
	if err != nil {
		return nil, err
	}
	versionID, err := pathID(r, "vid")
	if err != nil {
		return nil, err
	}
	return a.service.PipelineVersion(ctx, u.ID, groupID, id, versionID)
}

func (a *API) CreatePipelineVersion(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "pid", a.service.CreatePipelineVersion)
}

func (a *API) UpdatePipelineVersion(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, pid, err := mlIDs(r, "pid")
	if err != nil {
		return nil, err
	}
	vid, err := pathID(r, "vid")
	if err != nil {
		return nil, err
	}
	in, err := decode[dto.UpdatePipelineVersionReq](r)
	if err != nil {
		return nil, err
	}
	return a.service.UpdatePipelineVersion(ctx, u.ID, gid, pid, vid, requestKey(r), in)
}

func (a *API) PipelineVersionRevisions(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, pid, err := mlIDs(r, "pid")
	if err != nil {
		return nil, err
	}
	vid, err := pathID(r, "vid")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.PipelineVersionRevisions(ctx, u.ID, gid, pid, vid, q)
}

func (a *API) UpdatePipelineRun(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "rid", a.service.UpdatePipelineRun)
}

func (a *API) StartPipelineRun(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "rid", a.service.StartPipelineRun)
}

func (a *API) PipelineRunAttempts(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	gid, id, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.PipelineRunAttempts(ctx, u.ID, gid, id, q)
}

func (a *API) PipelineRuns(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, _, err := mlIDs(r, "")
	if err != nil {
		return nil, err
	}
	var pipelineID uint64
	if r.PathValue("pid") != "" {
		pipelineID, err = pathID(r, "pid")
		if err != nil {
			return nil, err
		}
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.PipelineRuns(ctx, u.ID, groupID, pipelineID, q)
}

func (a *API) PipelineRunRuntime(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	return a.service.PipelineRunRuntime(ctx, u.ID, groupID, id)
}

func (a *API) SubmitPipelineRun(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "pid", a.service.SubmitPipelineRun)
}

func (a *API) PipelineRun(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	return a.service.PipelineRun(ctx, u.ID, groupID, id)
}

func (a *API) StopPipelineRun(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "rid", a.service.StopPipelineRun)
}

func (a *API) DeletePipeline(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "pid", a.service.DeletePipeline)
}

func (a *API) DeletePipelineRun(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "rid", a.service.DeletePipelineRun)
}

func (a *API) TrainerJobs(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, _, err := mlIDs(r, "")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.TrainerJobs(ctx, u.ID, groupID, q)
}

func (a *API) CreateTrainerJob(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "", func(ctx context.Context, actor, gid, _ uint64, key string, in dto.CreateTrainerJobReq) (*model.Operation, error) {
		return a.service.CreateTrainerJob(ctx, actor, gid, key, in)
	})
}

func (a *API) TrainerJob(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "jid")
	if err != nil {
		return nil, err
	}
	return a.service.TrainerJob(ctx, u.ID, groupID, id)
}

func (a *API) StopTrainerJob(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "jid", a.service.StopTrainerJob)
}

func (a *API) DeleteTrainerJob(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "jid", a.service.DeleteTrainerJob)
}

func (a *API) KServeServices(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, _, err := mlIDs(r, "")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.KServeServices(ctx, u.ID, groupID, q)
}

func (a *API) CreateKServe(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "", func(ctx context.Context, actor, gid, _ uint64, key string, in dto.CreateKServeReq) (*model.Operation, error) {
		return a.service.CreateKServe(ctx, actor, gid, key, in)
	})
}

func (a *API) KServeService(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "sid")
	if err != nil {
		return nil, err
	}
	return a.service.KServeService(ctx, u.ID, groupID, id)
}

func (a *API) UpdateKServe(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "sid", a.service.UpdateKServe)
}

func (a *API) DeployKServe(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "sid", a.service.DeployKServe)
}

func (a *API) StopKServe(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "sid", a.service.StopKServe)
}

func (a *API) DeleteKServe(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return mlWrite(ctx, u, r, "sid", a.service.DeleteKServe)
}

func (a *API) KServeRevisions(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "sid")
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.KServeRevisions(ctx, u.ID, groupID, id, q)
}

func (a *API) mlHistory(ctx context.Context, u *model.User, r *http.Request, path, target string) (any, error) {
	groupID, id, err := mlIDs(r, path)
	if err != nil {
		return nil, err
	}
	q, err := mlQuery(r)
	if err != nil {
		return nil, err
	}
	return a.service.MLHistory(ctx, u.ID, groupID, id, target, q)
}

func (a *API) PipelineRunHistory(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.mlHistory(ctx, u, r, "rid", "pipeline_run")
}

func (a *API) TrainerJobHistory(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.mlHistory(ctx, u, r, "jid", "trainer_job")
}

func (a *API) KServeHistory(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	return a.mlHistory(ctx, u, r, "sid", "kserve_service")
}
