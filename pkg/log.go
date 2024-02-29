package pkg

import (
	"os"

	"golang.org/x/exp/slog"
)

// LogOptions represents the logging options available.
type LogOptions struct {
	// LogUseJSON, if true, will output JSON-formatted logs. Otherwise, if false, text-formatted logs are outputted.
	LogUseJSON bool
	// LogVerbose, if true, sets the log level to Debug, increasing verbosity. Otherwise, if false, the log level is
	// set to Info.
	LogVerbose bool
	// LogShowTime, if true, will add timestamps to log messages. Otherwise, if false, timestamps will be omitted.
	LogShowTime bool
}

var (
	// DefaultLogOptions will use text logs with timestamps shown.
	DefaultLogOptions = LogOptions{
		LogUseJSON:  false,
		LogVerbose:  false,
		LogShowTime: true,
	}
	// LogKeyError is used to set the standard key that should be used when providing an error in a log.
	LogKeyError = "err"
)

// NewLogger creates a new logger. It should be an inexpensive call. If no LogOptions are provided, then
// DefaultLogOptions are used. Only the first LogOptions provided to the function will be recognized, the rest
// will be ignored.
func NewLogger(_lo ...*LogOptions) *slog.Logger {
	lo := &DefaultLogOptions

	if len(_lo) >= 1 {
		lo = _lo[0]
	}

	level := new(slog.LevelVar)
	if lo.LogVerbose {
		level.Set(slog.LevelDebug)
	}

	removeTime := func(_ []string, a slog.Attr) slog.Attr {
		switch a.Key {
		case slog.KindTime.String():
			a.Key = ""
		default:
		}

		return a
	}

	if lo.LogShowTime {
		removeTime = nil
	}

	handlerOpts := slog.HandlerOptions{
		AddSource:   true,
		Level:       level,
		ReplaceAttr: removeTime,
	}

	var handler slog.Handler
	if lo.LogUseJSON {
		handler = slog.NewJSONHandler(os.Stderr, &handlerOpts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, &handlerOpts)
	}

	logger := slog.New(handler)

	return logger
}
