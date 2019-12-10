package testutils

import (
	"github.com/sirupsen/logrus"
	prefixed "github.com/x-cray/logrus-prefixed-formatter"

	"github.com/ghettovoice/gosip/log"
)

type Logger struct {
	logrus.Ext1FieldLogger
}

func NewLogger(prefix string, fields log.Fields) *Logger {
	slog := logrus.New()
	slog.SetLevel(logrus.TraceLevel)
	slog.Formatter = &prefixed.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05.000",
	}

	entry := slog.
		WithFields(logrus.Fields(fields)).
		WithField("prefix", prefix)

	return &Logger{
		Ext1FieldLogger: entry,
	}
}

func NewDefaultLogger() *Logger {
	return NewLogger("", nil)
}

func (l *Logger) WithFields(fields log.Fields) log.Logger {
	newFields := make(log.Fields)

	for k, v := range l.Fields() {
		newFields[k] = v
	}

	for k, v := range fields {
		newFields[k] = v
	}

	return NewLogger(l.Prefix(), newFields)
}

func (l *Logger) Fields() log.Fields {
	return log.Fields(l.Ext1FieldLogger.(*logrus.Entry).Data)
}

func (l *Logger) WithPrefix(prefix string) log.Logger {
	return NewLogger(prefix, l.Fields())
}

func (l *Logger) Prefix() string {
	if val, ok := l.Fields()["prefix"]; ok {
		return val.(string)
	}

	return ""
}
