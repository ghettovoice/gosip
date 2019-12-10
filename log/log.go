package log

// Logger interface used as base logger throughout the library.
type Logger interface {
	Print(args ...interface{})
	Printf(format string, args ...interface{})

	Trace(args ...interface{})
	Tracef(format string, args ...interface{})

	Debugf(format string, args ...interface{})
	Debug(args ...interface{})

	Info(args ...interface{})
	Infof(format string, args ...interface{})

	Warn(args ...interface{})
	Warnf(format string, args ...interface{})

	Error(args ...interface{})
	Errorf(format string, args ...interface{})

	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})

	Panic(args ...interface{})
	Panicf(format string, args ...interface{})

	WithFields(fields map[string]interface{}) Logger
	Fields() map[string]interface{}

	WithPrefix(prefix string) Logger
	Prefix() string
}

type Loggable interface {
	Log() Logger
}
