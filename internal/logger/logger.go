package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

type LogObserver interface {
	Observe(context.Context, slog.Record)
}

type Option func(*config)

type config struct {
	writer   io.Writer
	observer LogObserver
}

func WithWriter(writer io.Writer) Option {
	return func(cfg *config) {
		if writer != nil {
			cfg.writer = writer
		}
	}
}

func WithObserver(observer LogObserver) Option {
	return func(cfg *config) {
		cfg.observer = observer
	}
}

func New(level string, opts ...Option) (*slog.Logger, error) {
	parsed, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	cfg := config{
		writer: os.Stdout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	var handler slog.Handler = slog.NewJSONHandler(cfg.writer, &slog.HandlerOptions{
		Level: parsed,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.LevelKey {
				attr.Value = slog.StringValue(strings.ToUpper(attr.Value.String()))
			}
			return attr
		},
	})
	if cfg.observer != nil {
		handler = &observedHandler{
			next:     handler,
			observer: cfg.observer,
		}
	}
	return slog.New(handler), nil
}

type observedHandler struct {
	next     slog.Handler
	observer LogObserver
}

func (h *observedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *observedHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.observer != nil {
		h.observer.Observe(ctx, record.Clone())
	}
	return h.next.Handle(ctx, record)
}

func (h *observedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &observedHandler{
		next:     h.next.WithAttrs(attrs),
		observer: h.observer,
	}
}

func (h *observedHandler) WithGroup(name string) slog.Handler {
	return &observedHandler{
		next:     h.next.WithGroup(name),
		observer: h.observer,
	}
}

func parseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", level)
	}
}
