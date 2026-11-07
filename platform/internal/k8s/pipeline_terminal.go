package k8s

import (
	"context"
	"io"

	"github.com/githubtiger123/loom/platform/internal/k8s/podexec"
)

func (c *Client) ResolvePodTerminal(ctx context.Context, target podexec.Target) (podexec.Target, error) {
	return c.exec.Resolve(ctx, target)
}

func (c *Client) RunPodTerminal(ctx context.Context, target podexec.Target, shell string, stdin io.Reader, stdout io.Writer, sizes podexec.SizeQueue) error {
	return c.exec.Run(ctx, target, shell, stdin, stdout, sizes)
}
