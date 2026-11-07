// Package podexec provides business-independent, UID-checked Kubernetes terminals.
// Callers must authorize the target before using this component.
package podexec

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/githubtiger123/loom/platform/internal/apperr"
	"github.com/githubtiger123/loom/platform/internal/utils/validator"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/streaming/pkg/httpstream"
)

const MaxDuration = 15 * time.Minute

type Target struct {
	Namespace, Pod, PodUID, Container string
	ContainerID                       string // Pin the running container, not only the reusable Pod name.
}

type Size = remotecommand.TerminalSize
type SizeQueue = remotecommand.TerminalSizeQueue

type Reader struct {
	client kubernetes.Interface
	config *rest.Config
}

type openingSizes struct {
	queue SizeQueue
	once  sync.Once
	stop  func()
}

func (q *openingSizes) Next() *Size {
	q.once.Do(q.stop) // Called once the upgraded connection has created its streams.
	return q.queue.Next()
}

func New(client kubernetes.Interface, cfg *rest.Config) *Reader {
	copy := rest.CopyConfig(cfg)
	copy.Timeout = 0 // Session context bounds long-lived streaming, not the normal API timeout.
	return &Reader{client: client, config: copy}
}

func ValidateShell(shell string) error {
	if !validator.IsTerminalShell(shell) {
		return apperr.New(apperr.CodeInvalid, "shell 只支持 /bin/sh 或 /bin/bash")
	}
	return nil
}

// Resolve verifies a regular running container and captures its runtime identity.
func (r *Reader) Resolve(ctx context.Context, t Target) (Target, error) {
	if !validator.IsDNS1123Label(t.Namespace) || !validator.IsDNS1123Subdomain(t.Pod) ||
		t.PodUID == "" || !validator.IsDNS1123Label(t.Container) {
		return Target{}, apperr.New(apperr.CodeInvalid, "终端目标无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pod, err := r.client.CoreV1().Pods(t.Namespace).Get(ctx, t.Pod, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Target{}, apperr.New(apperr.CodeNotFound, "Pod 已清理，不能打开终端")
	}
	if err != nil {
		return Target{}, apperr.Wrap(apperr.CodeUnavailable, "无法读取终端目标", err)
	}
	if string(pod.UID) != t.PodUID || pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return Target{}, apperr.New(apperr.CodeConflict, "Pod 已变化、退出或正在删除")
	}
	regular := false
	for _, container := range pod.Spec.Containers {
		if container.Name == t.Container {
			regular = true
			break
		}
	}
	if regular {
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == t.Container && status.State.Running != nil && status.ContainerID != "" {
				if t.ContainerID != "" && t.ContainerID != status.ContainerID {
					return Target{}, apperr.New(apperr.CodeConflict, "容器已重启，请重新建立终端")
				}
				t.ContainerID = status.ContainerID
				return t, nil
			}
		}
	}
	return Target{}, apperr.New(apperr.CodeConflict, "目标容器未运行；不支持已退出、init 或临时容器")
}

// Run executes exactly one interactive shell. It never retries a started command.
// TTY merges stderr into stdout. Disconnecting does not guarantee killing child processes.
func (r *Reader) Run(ctx context.Context, t Target, shell string, stdin io.Reader, stdout io.Writer, sizes SizeQueue) error {
	if err := ValidateShell(shell); err != nil {
		return err
	}
	if sizes == nil {
		return apperr.New(apperr.CodeInvalid, "终端需要可取消的窗口尺寸队列")
	}
	if _, err := r.Resolve(ctx, t); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	openingTimer := time.AfterFunc(15*time.Second, cancel)
	defer openingTimer.Stop()
	openingQueue := &openingSizes{queue: sizes, stop: func() { openingTimer.Stop() }}
	request := r.client.CoreV1().RESTClient().Post().Namespace(t.Namespace).Resource("pods").Name(t.Pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: t.Container, Command: []string{shell},
			Stdin: true, Stdout: true, Stderr: false, TTY: true}, scheme.ParameterCodec)
	primary, err := remotecommand.NewWebSocketExecutor(r.config, http.MethodGet, request.URL().String())
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "无法初始化 Kubernetes 终端", err)
	}
	transport, upgrader, err := spdy.RoundTripperFor(r.config)
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "无法初始化终端兼容传输", err)
	}
	secondary, err := remotecommand.NewSPDYExecutorRejectRedirects(transport, upgrader, http.MethodPost, request.URL())
	if err != nil {
		return apperr.Wrap(apperr.CodeUnavailable, "无法初始化终端兼容连接", err)
	}
	executor, err := remotecommand.NewFallbackExecutor(primary, secondary, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: stdout, Tty: true, TerminalSizeQueue: openingQueue})
}
