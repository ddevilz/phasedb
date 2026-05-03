package output

import (
	"log/slog"
	"os"
)

func SetupLogger(format string) {
	var h slog.Handler
	switch format {
	case "json":
		h = slog.NewJSONHandler(os.Stdout, nil)
	default:
		h = slog.NewTextHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(h))
}
