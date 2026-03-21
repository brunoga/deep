package deep

import (
	"log/slog"
	"sync/atomic"
)

var loggerPtr atomic.Pointer[slog.Logger]

func init() {
	loggerPtr.Store(slog.Default())
}

// Logger returns the slog.Logger used for OpLog operations and log conditions.
// It is safe to call concurrently with SetLogger. To redirect or silence output:
//
//	deep.SetLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
//	deep.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))) // silence
func Logger() *slog.Logger {
	return loggerPtr.Load()
}

// SetLogger replaces the logger used for OpLog operations and log conditions.
// Safe to call concurrently with Logger.
func SetLogger(l *slog.Logger) {
	loggerPtr.Store(l)
}
