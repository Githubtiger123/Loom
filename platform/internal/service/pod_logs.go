package service

import (
	"context"
	"errors"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
)

// PodLogSession 不持有数据库事务，也不绑定具体业务模块。
// Check 由业务模块注入授权/归属检查，HTTP 层负责消费和关闭流。
type PodLogSession struct {
	Stream *podlog.Stream
	Check  func(context.Context) error
	Cancel context.CancelFunc
	Ctx    context.Context
}

func (s *PodLogSession) Close() {
	s.Cancel()
	_ = s.Stream.Close()
}

func podLogReadError(err error) error {
	var permanent *k8s.Permanent
	if errors.As(err, &permanent) {
		return apperr.New(apperr.CodeConflict, permanent.Reason)
	}
	var app *apperr.Error
	if errors.As(err, &app) {
		return err
	}
	return apperr.Wrap(apperr.CodeUnavailable, "日志执行空间暂不可验证", err)
}
