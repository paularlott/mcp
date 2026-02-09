package openai

import (
	"context"
	"time"
)

const (
	// DefaultRequestTimeout is the default timeout for AI completion requests.
	// This timeout is applied via a detached context so that parent context
	// cancellation (e.g. from script timeouts) does not kill long-running
	// AI operations. Set to 0 to use the caller's context directly.
	DefaultRequestTimeout = 10 * time.Minute
)

// detachedContext preserves the parent's context values (tool providers,
// user info, tool handlers, etc.) but ignores the parent's cancellation
// signal. It has its own deadline/done channel from a background-rooted context.
//
// This allows AI completion requests to survive parent context cancellation
// (e.g. scriptling script timeouts) while still accessing context-stored
// values like MCP tool providers.
type detachedContext struct {
	parent context.Context // for Value() lookups
	inner  context.Context // context.WithTimeout(context.Background(), timeout)
}

func (d *detachedContext) Deadline() (time.Time, bool) { return d.inner.Deadline() }
func (d *detachedContext) Done() <-chan struct{}        { return d.inner.Done() }
func (d *detachedContext) Err() error                  { return d.inner.Err() }
func (d *detachedContext) Value(key any) any            { return d.parent.Value(key) }

// NewDetachedContext creates a context that preserves the parent's values
// but has an independent cancellation rooted at context.Background().
// The returned cancel function must be called to release resources.
func NewDetachedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	inner, cancel := context.WithTimeout(context.Background(), timeout)
	return &detachedContext{parent: parent, inner: inner}, cancel
}
