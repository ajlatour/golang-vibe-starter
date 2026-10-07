package logx

import (
	"log/slog"
	"runtime/debug"
)

// Go runs fn in a goroutine of its own, as a background task named task:
// one that runs beside the requests, such as reloading the API keys. A
// panic in a goroutine no one recovers ends the whole process, every
// request in flight with it, unanswered; this one is logged instead, at
// ERROR as "panic in background task" with its stack, and ends only fn.
// The service carries on without the task, so the line is worth alerting
// on.
func Go(logger *slog.Logger, task string, fn func()) {
	go func() {
		defer func() {
			if v := recover(); v != nil {
				logger.Error("panic in background task",
					KeyTask, task,
					KeyPanic, v,
					KeyStack, string(debug.Stack()))
			}
		}()
		fn()
	}()
}
