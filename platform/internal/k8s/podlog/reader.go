// Package podlog 提供与业务模块、数据库和 HTTP 无关的 Pod 日志读取。
// 调用者必须先完成业务授权和资源归属校验，不能直接使用用户传入的 Target。
package podlog

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const MaxStreamDuration = 5 * time.Minute

type Target struct {
	Namespace string
	Pod       string
	PodUID    string
	Container string
}

type Options struct {
	Container    string
	Follow       bool
	TailLines    int64
	LimitBytes   int64
	SinceSeconds int64
	SinceTime    *time.Time
	Previous     bool
	Timestamps   bool
}

func (o *Options) Validate() error {
	if o.TailLines == 0 {
		o.TailLines = 200
	}
	if o.LimitBytes == 0 {
		o.LimitBytes = 256 << 10
	}
	if o.SinceSeconds == 0 && o.SinceTime == nil {
		o.SinceSeconds = 3600
	}
	maximum := int64(1 << 20)
	if o.Follow {
		maximum = 16 << 20
	}
	if o.TailLines < 1 || o.TailLines > 1000 || o.LimitBytes < 1 || o.LimitBytes > maximum ||
		o.SinceSeconds < 0 || o.SinceSeconds > 86400 || (o.SinceTime != nil && o.SinceSeconds != 0) ||
		(o.SinceTime != nil && o.SinceTime.After(time.Now().Add(15*time.Second))) || (o.Previous && o.Follow) {
		return apperr.New(apperr.CodeInvalid, "日志参数范围无效；sinceTime/sinceSeconds 二选一，previous 不支持 follow")
	}
	if o.Container != "" && !validator.IsDNS1123Label(o.Container) {
		return apperr.New(apperr.CodeInvalid, "容器名称无效")
	}
	return nil
}

type Snapshot struct {
	Pod        string    `json:"pod"`
	PodUID     string    `json:"podUID"`
	Container  string    `json:"container"`
	Text       string    `json:"text"`
	Truncated  bool      `json:"truncated"`
	ObservedAt time.Time `json:"observedAt"`
}

type Reader struct{ client kubernetes.Interface }

// client 的 HTTP 总超时应为零，整个读取生命周期由调用者的 context 限制。
func New(client kubernetes.Interface) *Reader { return &Reader{client: client} }

type Stream struct {
	Body    io.ReadCloser
	Target  Target
	Options Options
	reader  *Reader
	cancel  context.CancelFunc
	once    sync.Once
}

func (s *Stream) Close() (err error) {
	s.once.Do(func() {
		s.cancel()
		err = s.Body.Close()
	})
	return err
}

// Check 不代替业务授权；用于打开前后和持续读取期间复核 Pod UID 与容器。
func (s *Stream) Check(ctx context.Context) error { return s.reader.check(ctx, s.Target) }

func (r *Reader) check(ctx context.Context, t Target) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pod, err := r.client.CoreV1().Pods(t.Namespace).Get(ctx, t.Pod, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return apperr.New(apperr.CodeNotFound, "Pod 已不存在，在线日志不可读；离线日志需要事先归档")
	}
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "目标 Pod 暂不可读，在线日志不等于归档日志", err)
	}
	if pod.Namespace != t.Namespace || pod.Name != t.Pod || string(pod.UID) != t.PodUID {
		return apperr.New(apperr.CodeConflict, "Pod UID 已变化，请重新查询执行节点")
	}
	for _, c := range pod.Spec.Containers {
		if c.Name == t.Container {
			return nil
		}
	}
	for _, c := range pod.Spec.InitContainers {
		if c.Name == t.Container {
			return nil
		}
	}
	return apperr.New(apperr.CodeInvalid, "目标 Pod 不存在指定的业务或初始化容器")
}

func (r *Reader) Open(ctx context.Context, t Target, o Options) (*Stream, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if !validator.IsDNS1123Label(t.Namespace) || !validator.IsSecretName(t.Pod) ||
		!validator.IsDNS1123Label(t.Container) || len(t.PodUID) > 64 || !validator.IsIdempotencyKey(t.PodUID) ||
		(o.Container != "" && o.Container != t.Container) {
		return nil, apperr.New(apperr.CodeInvalid, "日志目标必须包含已核实的 namespace、Pod UID 和容器")
	}
	if err := r.check(ctx, t); err != nil {
		return nil, err
	}
	options := &corev1.PodLogOptions{Container: t.Container, Follow: o.Follow, TailLines: &o.TailLines,
		Previous: o.Previous, Timestamps: o.Timestamps}
	if !o.Follow {
		options.LimitBytes = &o.LimitBytes
	}
	if o.SinceTime != nil {
		stamp := metav1.NewTime(*o.SinceTime)
		options.SinceTime = &stamp
	} else {
		options.SinceSeconds = &o.SinceSeconds
	}
	duration := 15 * time.Second
	if o.Follow {
		duration = MaxStreamDuration
	}
	streamCtx, cancel := context.WithTimeout(ctx, duration)
	// 只限制建立流的时间，不用固定 HTTP 总超时截断已经建立的 follow。
	headerTimer := time.AfterFunc(10*time.Second, cancel)
	body, err := r.client.CoreV1().Pods(t.Namespace).GetLogs(t.Pod, options).Stream(streamCtx)
	headerTimer.Stop()
	if err != nil {
		cancel()
		return nil, apperr.Wrap(apperr.CodeUnavailable, "无法打开 Pod 日志流", err)
	}
	s := &Stream{Body: body, Target: t, Options: o, reader: r, cancel: cancel}
	// pods/log 没有 UID 前置条件；打开后、交付首字节前再次读取 UID，尽量缩小同名替换窗口。
	if err := s.Check(streamCtx); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// ReadSnapshot 不返回未经读取后 UID 检查的内容；调用方仍需在交付前复核业务授权。
func ReadSnapshot(ctx context.Context, s *Stream) (*Snapshot, error) {
	if s.Options.Follow {
		return nil, apperr.New(apperr.CodeInvalid, "持续日志不能作为快照读取")
	}
	data, err := io.ReadAll(io.LimitReader(s.Body, s.Options.LimitBytes+1))
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeUnavailable, "读取 Pod 日志失败", err)
	}
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	truncated := int64(len(data)) >= s.Options.LimitBytes
	if int64(len(data)) > s.Options.LimitBytes {
		data = data[:s.Options.LimitBytes]
	}
	return &Snapshot{Pod: s.Target.Pod, PodUID: s.Target.PodUID, Container: s.Target.Container,
		Text: strings.ToValidUTF8(string(data), "�"), Truncated: truncated, ObservedAt: time.Now().UTC()}, nil
}
