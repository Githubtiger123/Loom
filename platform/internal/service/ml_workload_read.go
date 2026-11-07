package service

import (
	"context"

	"github.com/githubtiger123/loom/platform/internal/dto"
	"github.com/githubtiger123/loom/platform/internal/model"
)

func mlPage[T any](rows []T, limit int, id func(T) uint64) *dto.MLPage[T] {
	p := &dto.MLPage[T]{Items: rows}
	if len(rows) > limit {
		next := id(rows[limit-1])
		p.NextAfter, p.Items = &next, rows[:limit]
	}
	return p
}

func (s *Service) mlListAllowed(ctx context.Context, actor, groupID uint64, module string, q *dto.MLListQuery) error {
	if err := q.Validate(); err != nil {
		return err
	}
	if err := s.Require(ctx, actor, groupID, module+".read"); err != nil {
		return err
	}
	if q.Scope != "mine" {
		if err := s.Require(ctx, actor, groupID, module+".read.any"); err != nil {
			return err
		}
	}
	_, err := s.Store.Group(ctx, groupID, false)
	return domainError(err)
}

func (s *Service) Pipelines(ctx context.Context, actor, groupID uint64, q dto.MLListQuery) (*dto.MLPage[model.Pipeline], error) {
	if err := s.mlListAllowed(ctx, actor, groupID, "pipeline", &q); err != nil {
		return nil, err
	}
	rows := []model.Pipeline{}
	if err := s.Store.MLList(ctx, &model.Pipeline{}, groupID, actor, q).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.Pipeline) uint64 { return n.ID }), nil
}

func (s *Service) Pipeline(ctx context.Context, actor, groupID, id uint64) (*model.Pipeline, error) {
	if err := pipelineGuard(ctx, actor, groupID, id, "pipeline.read")(s.Store); err != nil {
		return nil, domainError(err)
	}
	n, err := s.Store.Pipeline(ctx, groupID, id, false)
	return n, domainError(err)
}

func (s *Service) PipelineVersions(ctx context.Context, actor, groupID, id uint64, q dto.MLListQuery) (*dto.MLPage[model.PipelineVersion], error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.Pipeline(ctx, actor, groupID, id); err != nil {
		return nil, err
	}
	// Versions are children of the authorized definition, not independent objects.
	rows := []model.PipelineVersion{}
	err := s.DB.WithContext(ctx).Where("group_id = ? AND pipeline_id = ? AND id > ?", groupID, id, q.After).Order("id").Limit(q.Limit + 1).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.PipelineVersion) uint64 { return n.ID }), nil
}

func (s *Service) PipelineVersion(ctx context.Context, actor, groupID, pipelineID, id uint64) (*model.PipelineVersion, error) {
	if _, err := s.Pipeline(ctx, actor, groupID, pipelineID); err != nil {
		return nil, err
	}
	n, err := s.Store.PipelineVersion(ctx, groupID, pipelineID, id)
	return n, domainError(err)
}

// PipelineRuns is group-scoped. Optional pipelineID filters run history without
// granting access to runs owned by somebody else merely via definition ownership.
func (s *Service) PipelineRuns(ctx context.Context, actor, groupID, pipelineID uint64, q dto.MLListQuery) (*dto.MLPage[model.PipelineRun], error) {
	if err := s.mlListAllowed(ctx, actor, groupID, "pipeline", &q); err != nil {
		return nil, err
	}
	rows := []model.PipelineRun{}
	db := s.Store.MLList(ctx, &model.PipelineRun{}, groupID, actor, q)
	if pipelineID != 0 {
		db = db.Where("pipeline_id = ?", pipelineID)
	}
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.PipelineRun) uint64 { return n.ID }), nil
}

func (s *Service) readML(ctx context.Context, actor, groupID, id uint64, target string) (*mlObject, error) {
	if err := s.Require(ctx, actor, groupID, mlModule(target)+".read"); err != nil {
		return nil, err
	}
	n, err := loadML(ctx, s.Store, target, groupID, id, false)
	if err != nil {
		return nil, domainError(err)
	}
	if err := mlAllowed(ctx, s.Store, actor, n.identity, mlModule(target), mlModule(target)+".read"); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Service) PipelineRun(ctx context.Context, actor, groupID, id uint64) (*model.PipelineRun, error) {
	n, err := s.readML(ctx, actor, groupID, id, "pipeline_run")
	if err != nil {
		return nil, err
	}
	return n.row.(*model.PipelineRun), nil
}

func (s *Service) TrainerJobs(ctx context.Context, actor, groupID uint64, q dto.MLListQuery) (*dto.MLPage[model.TrainerJob], error) {
	if err := s.mlListAllowed(ctx, actor, groupID, "trainer", &q); err != nil {
		return nil, err
	}
	rows := []model.TrainerJob{}
	if err := s.Store.MLList(ctx, &model.TrainerJob{}, groupID, actor, q).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.TrainerJob) uint64 { return n.ID }), nil
}

func (s *Service) TrainerJob(ctx context.Context, actor, groupID, id uint64) (*model.TrainerJob, error) {
	n, err := s.readML(ctx, actor, groupID, id, "trainer_job")
	if err != nil {
		return nil, err
	}
	return n.row.(*model.TrainerJob), nil
}

func (s *Service) KServeServices(ctx context.Context, actor, groupID uint64, q dto.MLListQuery) (*dto.MLPage[model.KServeService], error) {
	if err := s.mlListAllowed(ctx, actor, groupID, "kserve", &q); err != nil {
		return nil, err
	}
	rows := []model.KServeService{}
	if err := s.Store.MLList(ctx, &model.KServeService{}, groupID, actor, q).Find(&rows).Error; err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.KServeService) uint64 { return n.ID }), nil
}

func (s *Service) KServeService(ctx context.Context, actor, groupID, id uint64) (*model.KServeService, error) {
	n, err := s.readML(ctx, actor, groupID, id, "kserve_service")
	if err != nil {
		return nil, err
	}
	return n.row.(*model.KServeService), nil
}

func (s *Service) KServeRevisions(ctx context.Context, actor, groupID, id uint64, q dto.MLListQuery) (*dto.MLPage[model.KServeRevision], error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.KServeService(ctx, actor, groupID, id); err != nil {
		return nil, err
	}
	rows := []model.KServeRevision{}
	err := s.DB.WithContext(ctx).Where("group_id = ? AND service_id = ? AND id > ?", groupID, id, q.After).Order("id").Limit(q.Limit + 1).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.KServeRevision) uint64 { return n.ID }), nil
}

func (s *Service) MLHistory(ctx context.Context, actor, groupID, id uint64, target string, q dto.MLListQuery) (*dto.MLPage[model.MLWorkloadHistory], error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.readML(ctx, actor, groupID, id, target); err != nil {
		return nil, err
	}
	rows, err := s.Store.MLHistory(ctx, groupID, target, id, q.After, q.Limit)
	if err != nil {
		return nil, err
	}
	return mlPage(rows, q.Limit, func(n model.MLWorkloadHistory) uint64 { return n.ID }), nil
}
