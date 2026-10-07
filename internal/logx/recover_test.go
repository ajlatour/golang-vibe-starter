package logx_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
	"github.com/ajlatour/golang-vibe-starter/internal/logx"
)

// A panic in a background task is logged, with its stack, and ends only the
// task: the test process (standing in for the service) carries on.
func TestGoRecoversAPanic(t *testing.T) {
	var logs logtest.Buffer
	done := make(chan struct{})
	logx.Go(logs.Logger(slog.LevelInfo), "keys reload", func() {
		defer close(done)
		panic("a bug in the task")
	})
	<-done
	var rec map[string]any
	for deadline := time.Now().Add(2 * time.Second); rec == nil && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		for _, r := range logs.Records(t) {
			if r["msg"] == "panic in background task" {
				rec = r
			}
		}
	}
	if rec == nil {
		t.Fatalf("no panic in background task line:\n%s", logs.String())
	}
	if rec["level"] != "ERROR" || rec[logx.KeyTask] != "keys reload" ||
		rec[logx.KeyPanic] != "a bug in the task" || !strings.Contains(rec[logx.KeyStack].(string), "recover_test.go") {
		t.Errorf("logged %v", rec)
	}
}

func TestGoRunsTheTask(t *testing.T) {
	ran := make(chan struct{})
	var logs logtest.Buffer
	logx.Go(logs.Logger(slog.LevelInfo), "task", func() { close(ran) })
	<-ran
	if logs.Len() != 0 {
		t.Errorf("a task that didn't panic logged:\n%s", logs.String())
	}
}
