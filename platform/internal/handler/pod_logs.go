package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/k8s/podlog"
	"github.com/githubtiger123/loom/platform/internal/model"
	"github.com/githubtiger123/loom/platform/internal/service"
)

// 日志消费与业务目标解析解耦；以后其他模块也可返回同一种响应。
type podLogResponse struct {
	session *service.PodLogSession
	release func()
	actor   uint64
}

type logStreamLimiter struct {
	mu    sync.Mutex
	total int
	users map[uint64]int
}

func (l *logStreamLimiter) reserve(actor uint64) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.users == nil {
		l.users = map[uint64]int{}
	}
	if l.total >= 128 || l.users[actor] >= 4 {
		return nil, apperr.New(apperr.CodeRateLimited, "日志连接已达上限，请关闭其他日志窗口后重试")
	}
	l.total++
	l.users[actor]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			l.users[actor]--
			if l.users[actor] == 0 {
				delete(l.users, actor)
			}
		})
	}, nil
}

func parsePodLogOptions(values url.Values) (podlog.Options, error) {
	o := podlog.Options{Timestamps: true}
	invalid := func() (podlog.Options, error) {
		return o, apperr.New(apperr.CodeInvalid, "日志参数无效、重复或不受支持")
	}
	for key, items := range values {
		if len(items) != 1 || items[0] == "" {
			return invalid()
		}
		switch key {
		case "container":
			o.Container = items[0]
		case "tailLines", "limitBytes", "sinceSeconds":
			n, err := strconv.ParseInt(items[0], 10, 64)
			if err != nil || n <= 0 {
				return invalid()
			}
			switch key {
			case "tailLines":
				o.TailLines = n
			case "limitBytes":
				o.LimitBytes = n
			case "sinceSeconds":
				o.SinceSeconds = n
			}
		case "follow", "previous", "timestamps":
			b, err := strconv.ParseBool(items[0])
			if err != nil {
				return invalid()
			}
			switch key {
			case "follow":
				o.Follow = b
			case "previous":
				o.Previous = b
			case "timestamps":
				o.Timestamps = b
			}
		case "sinceTime":
			stamp, err := time.Parse(time.RFC3339Nano, items[0])
			if err != nil {
				return invalid()
			}
			o.SinceTime = &stamp
		default:
			return invalid()
		}
	}
	if err := o.Validate(); err != nil {
		return o, err
	}
	return o, nil
}

func (a *API) PipelineTaskLogs(ctx context.Context, u *model.User, r *http.Request) (any, error) {
	groupID, id, err := mlIDs(r, "rid")
	if err != nil {
		return nil, err
	}
	o, err := parsePodLogOptions(r.URL.Query())
	if err != nil {
		return nil, err
	}
	release, err := a.logStreams.reserve(u.ID)
	if err != nil {
		return nil, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			release()
		}
	}()
	session, err := a.service.OpenPipelineTaskLogs(ctx, u.ID, groupID, id, r.PathValue("taskId"), o)
	if err != nil {
		return nil, err
	}
	if o.Follow {
		handedOff = true
		return &podLogResponse{session: session, release: release, actor: u.ID}, nil
	}
	defer session.Close()
	snapshot, err := podlog.ReadSnapshot(session.Ctx, session.Stream)
	if err != nil {
		return nil, err
	}
	if err := session.Check(session.Ctx); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// 持续响应使用纯文本 HTTP 流；出错后只结束连接/写 trailer，不向日志正文混入 JSON。
func (a *API) writePodLogStream(w http.ResponseWriter, r *http.Request, response *podLogResponse) {
	session := response.session
	defer response.release()
	defer session.Close()
	check := func() error {
		ctx, cancel := context.WithTimeout(session.Ctx, 5*time.Second)
		defer cancel()
		if a.auth != nil {
			p, err := a.bearer(r.WithContext(ctx))
			if err != nil {
				return err
			}
			if p.User.ID != response.actor || p.PasswordChangeRequired {
				return apperr.New(apperr.CodeUnauthorized, "日志会话身份已失效")
			}
		}
		return session.Check(ctx)
	}
	if err := check(); err != nil {
		a.error(w, err)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
		a.error(w, apperr.New(apperr.CodeUnavailable, "当前 HTTP 服务不支持受限日志流"))
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second)) }()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Loom-Pod-UID", session.Stream.Target.PodUID)
	w.Header().Set("X-Loom-Log-Container", session.Stream.Target.Container)
	w.Header().Set("Trailer", "X-Loom-Log-End")
	w.WriteHeader(http.StatusOK)
	if err := controller.Flush(); err != nil {
		return
	}
	type chunk struct {
		data []byte
		err  error
	}
	chunks := make(chan chunk, 1)
	go func() {
		for {
			buffer := make([]byte, 16<<10)
			n, err := session.Stream.Body.Read(buffer)
			select {
			case chunks <- chunk{data: buffer[:n], err: err}:
			case <-session.Ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	remaining := session.Stream.Options.LimitBytes
	lastCheck := time.Now()
	for {
		select {
		case <-session.Ctx.Done():
			w.Header().Set("X-Loom-Log-End", "deadline-or-disconnect")
			return
		case <-ticker.C:
			if err := check(); err != nil {
				w.Header().Set("X-Loom-Log-End", "access-or-identity-changed")
				return
			}
			lastCheck = time.Now()
			_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
		case item := <-chunks:
			if time.Since(lastCheck) >= 10*time.Second {
				if err := check(); err != nil {
					w.Header().Set("X-Loom-Log-End", "access-or-identity-changed")
					return
				}
				lastCheck = time.Now()
			}
			if int64(len(item.data)) > remaining {
				item.data = item.data[:remaining]
			}
			if len(item.data) != 0 {
				_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
				if _, err := w.Write(item.data); err != nil {
					return
				}
				if err := controller.Flush(); err != nil {
					return
				}
				remaining -= int64(len(item.data))
			}
			if remaining == 0 {
				w.Header().Set("X-Loom-Log-End", "byte-limit")
				return
			}
			if item.err != nil {
				end := "source-error"
				if errors.Is(item.err, io.EOF) {
					end = "eof"
				}
				w.Header().Set("X-Loom-Log-End", end)
				return
			}
		}
	}
}
