package logging

import (
	"log/slog"
	"os"
	"path/filepath"
)

type Logger struct {
	*slog.Logger
}

func New() *Logger {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "cluster-installer", "logs")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return &Logger{slog.New(slog.NewJSONHandler(os.Stderr, nil))}
	}
	return &Logger{slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))}
}
