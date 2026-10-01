package stages

import (
	"context"
	"time"
)

// RunSecretctlExecForTest swaps classify-jev's secretctl runner for a fake,
// returning the previous one so stages_test can restore it on cleanup.
func RunSecretctlExecForTest(fn func(ctx context.Context, timeout time.Duration, script string) ([]byte, error)) func(ctx context.Context, timeout time.Duration, script string) ([]byte, error) {
	prev := runSecretctlExec
	runSecretctlExec = fn
	return prev
}
