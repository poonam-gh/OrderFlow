package logger

import (
	"context"
	"io"
	"log/slog"

	"orderflow/pkg/requestid"
)

func New(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, nil))
}

// Ctx returns base with a request_id attribute attached if ctx carries one
// (set by routers/middleware.RequestID, or propagated into a worker job's
// context from an outbox payload) — so one request's log lines are
// traceable across both the server and worker processes.
func Ctx(ctx context.Context, base *slog.Logger) *slog.Logger {
	if id, ok := requestid.FromContext(ctx); ok {
		return base.With("request_id", id)
	}
	return base
}
