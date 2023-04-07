package log

type NoopLogger struct {
}

func (l *NoopLogger) WithFields(flds map[string]any) Logger { return l }

func (l *NoopLogger) Debug(msg string, flds map[string]any) {}

func (l *NoopLogger) Warn(msg string, flds map[string]any) {}

func (l *NoopLogger) Error(msg string, flds map[string]any) {}
