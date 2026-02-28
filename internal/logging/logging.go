package logging

import (
	"log/slog"
	"os"
)

// Setup initializes structured JSON logging and sets it as the default logger.
func Setup() {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))
}
