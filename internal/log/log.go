// Package log is gosf's leveled activity logger. It writes human-readable,
// colorized log lines to stderr (keeping stdout reserved for machine/result
// output) using the standard library's log/slog with a custom handler.
//
// Verbosity is a count controlled by the root -v/--verbose flag:
//
//	default   INFO   high-level activity ("scanning remote 12/50", "pushed x v2")
//	-v        DEBUG  per-item detail and timings
//	-vv       TRACE  HTTP request traces (adds timestamp + source location)
//	-vvv      TRACE2 maximum detail
//	--quiet          errors only (overrides verbosity)
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/BU-Neuromics/datapin/internal/output"
)

func defaultNow() time.Time { return time.Now() }

// Custom levels extend slog's built-ins (Debug=-4, Info=0, Warn=4, Error=8);
// lower is more verbose.
const (
	LevelTrace  = slog.Level(-8)  // -vv: HTTP request traces
	LevelTrace2 = slog.Level(-12) // -vvv: maximum detail
)

// resolveLevel maps a verbosity count and the quiet flag to the minimum level
// the logger will emit. Pure, so it is table-tested.
func resolveLevel(verbosity int, quiet bool) slog.Level {
	if quiet {
		return slog.LevelError
	}
	switch {
	case verbosity <= 0:
		return slog.LevelInfo
	case verbosity == 1:
		return slog.LevelDebug
	case verbosity == 2:
		return LevelTrace
	default:
		return LevelTrace2
	}
}

// humanHandler renders slog records as clean, colorized human lines:
//
//	INFO  message key=value
//
// At verbosity >= 2 (showMeta) it prefixes a timestamp and appends the source
// location, matching the documented -vv behavior.
type humanHandler struct {
	mu       *sync.Mutex
	w        io.Writer
	level    slog.Level
	showMeta bool
	attrs    []slog.Attr
}

func newHumanHandler(w io.Writer, level slog.Level, showMeta bool) *humanHandler {
	return &humanHandler{mu: &sync.Mutex{}, w: w, level: level, showMeta: showMeta}
}

func (h *humanHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *humanHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if h.showMeta && !r.Time.IsZero() {
		b.WriteString(output.Dim(r.Time.Format("15:04:05.000")))
		b.WriteByte(' ')
	}
	b.WriteString(levelTag(r.Level))
	b.WriteString("  ")
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		writeAttr(&b, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, a)
		return true
	})
	if h.showMeta && r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		if f.File != "" {
			b.WriteByte(' ')
			b.WriteString(output.Dim(fmt.Sprintf("(%s:%d)", filepath.Base(f.File), f.Line)))
		}
	}
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *humanHandler) WithAttrs(as []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return &nh
}

func (h *humanHandler) WithGroup(string) slog.Handler { return h }

func writeAttr(b *strings.Builder, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	b.WriteByte(' ')
	b.WriteString(output.Dim(a.Key + "=" + a.Value.String()))
}

// levelTag returns the 5-char, colorized tag for a level.
func levelTag(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return output.Red("ERROR")
	case l >= slog.LevelWarn:
		return output.Yellow("WARN ")
	case l >= slog.LevelInfo:
		return output.Green("INFO ")
	case l >= slog.LevelDebug:
		return output.Dim("DEBUG")
	default:
		return output.Dim("TRACE")
	}
}

var (
	mu  sync.Mutex
	std *slog.Logger
)

// Init points the package logger at stderr for the given verbosity and quiet
// flag. Called once at startup from the root command's PersistentPreRunE.
func Init(verbosity int, quiet bool) { SetWriter(os.Stderr, verbosity, quiet) }

// SetWriter points the package logger at an arbitrary writer. Used by tests.
func SetWriter(w io.Writer, verbosity int, quiet bool) {
	mu.Lock()
	defer mu.Unlock()
	std = slog.New(newHumanHandler(w, resolveLevel(verbosity, quiet), verbosity >= 2))
}

func logger() *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	if std == nil {
		std = slog.New(newHumanHandler(os.Stderr, slog.LevelInfo, false))
	}
	return std
}

func logf(level slog.Level, format string, a ...any) {
	l := logger()
	ctx := context.Background()
	if !l.Enabled(ctx, level) {
		return
	}
	// Skip [runtime.Callers, this frame, the exported wrapper] so source
	// locations at -vv point at the real call site.
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(nowFunc(), level, fmt.Sprintf(format, a...), pcs[0])
	_ = l.Handler().Handle(ctx, r)
}

// nowFunc is overridable so tests can assert timestamp presence deterministically.
var nowFunc = defaultNow

// Errorf logs at ERROR (always shown unless output is fully suppressed).
func Errorf(format string, a ...any) { logf(slog.LevelError, format, a...) }

// Warnf logs at WARN.
func Warnf(format string, a ...any) { logf(slog.LevelWarn, format, a...) }

// Infof logs high-level activity at INFO (the default level).
func Infof(format string, a ...any) { logf(slog.LevelInfo, format, a...) }

// Debugf logs per-item detail at DEBUG (-v).
func Debugf(format string, a ...any) { logf(slog.LevelDebug, format, a...) }

// Tracef logs HTTP-level traces at TRACE (-vv).
func Tracef(format string, a ...any) { logf(LevelTrace, format, a...) }

// Trace2f logs maximum detail at TRACE2 (-vvv).
func Trace2f(format string, a ...any) { logf(LevelTrace2, format, a...) }

// Enabled reports whether the given level would currently be emitted.
func Enabled(level slog.Level) bool { return logger().Enabled(context.Background(), level) }
