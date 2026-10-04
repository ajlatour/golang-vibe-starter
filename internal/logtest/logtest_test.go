package logtest_test

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/ajlatour/golang-vibe-starter/internal/logtest"
)

func TestBuffer(t *testing.T) {
	var buf logtest.Buffer
	logger := buf.Logger(slog.LevelInfo)
	logger.Debug("hidden below the level")
	logger.Info("first", "k", "v")
	logger.Warn("second")

	recs := buf.Records(t)
	if got, want := logtest.Msgs(recs), []string{"first", "second"}; !slices.Equal(got, want) {
		t.Errorf("Msgs = %q, want %q", got, want)
	}
	if got, want := logtest.LevelMsgs(recs), []string{"INFO first", "WARN second"}; !slices.Equal(got, want) {
		t.Errorf("LevelMsgs = %q, want %q", got, want)
	}
	if recs[0]["k"] != "v" {
		t.Errorf("first record = %v, want k=v", recs[0])
	}
	if len(buf.Lines()) != 2 || buf.Len() == 0 {
		t.Errorf("Lines = %d, Len = %d, want 2 lines and some bytes", len(buf.Lines()), buf.Len())
	}

	buf.Reset()
	if buf.Len() != 0 || len(buf.Records(t)) != 0 {
		t.Error("Reset left output behind")
	}
}
