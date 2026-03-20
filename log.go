package deep

import "log/slog"

// Logger is the slog.Logger used for OpLog operations and log conditions.
// It defaults to slog.Default(). Replace it to redirect or silence deep's
// diagnostic output:
//
//	deep.SetLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
//	deep.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))) // silence
var Logger = slog.Default()

// SetLogger replaces the logger used for OpLog operations and log conditions.
func SetLogger(l *slog.Logger) {
	Logger = l
}
