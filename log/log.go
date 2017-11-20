package log

import (
	"io"

	"github.com/sirupsen/logrus"
)

const (
	// PanicLevel level, highest level of severity. Logs and then calls panic with the
	// message passed to Debug, Info, ...
	PanicLevel = logrus.PanicLevel
	// FatalLevel level. Logs and then calls `os.Exit(1)`. It will exit even if the
	// logging level is set to Panic.
	FatalLevel = logrus.FatalLevel
	// ErrorLevel level. Logs. Used for errors that should definitely be noted.
	// Commonly used for hooks to send errors to an error tracking service.
	ErrorLevel = logrus.ErrorLevel
	// WarnLevel level. Non-critical entries that deserve eyes.
	WarnLevel = logrus.WarnLevel
	// InfoLevel level. General operational entries about what's going on inside the
	// application.
	InfoLevel = logrus.InfoLevel
	// DebugLevel level. Usually only enabled when debugging. Very verbose logging.
	DebugLevel = logrus.DebugLevel
)

func init() {
	logrus.AddHook(&CallInfoHook{})
	logrus.SetFormatter(NewFormatter(true))
}

// Logger interface used as base logger throughout the library.
// It's actually extends logrus.FieldLogger interface.
type Logger interface {
	logrus.FieldLogger
}

// WithLogger introduces types with local context scoped logger.
type WithLogger interface {
	// Log returns Logger instance
	Log() Logger
	SetLog(logger Logger)
}

func StandardLogger() logrus.FieldLogger {
	return logrus.StandardLogger()
}

func SetOutput(out io.Writer) {
	logrus.SetOutput(out)
}

func SetLevel(level logrus.Level) {
	logrus.SetLevel(level)
}

func GetLevel() logrus.Level {
	return logrus.GetLevel()
}

// WithError creates an entry from the standard logger and adds an error to it, using the value defined in ErrorKey as key.
func WithError(err error) Logger {
	return logrus.WithField(logrus.ErrorKey, err)
}

// WithField creates an entry from the standard logger and adds a field to
// it. If you want multiple fields, use `WithFields`.
//
// Note that it doesn't log until you call Debug, Print, Info, Warn, Fatal
// or Panic on the Entry it returns.
func WithField(key string, value interface{}) Logger {
	return logrus.WithField(key, value)
}

// WithFields creates an entry from the standard logger and adds multiple
// fields to it. This is simply a helper for `WithField`, invoking it
// once for each field.
//
// Note that it doesn't log until you call Debug, Print, Info, Warn, Fatal
// or Panic on the Entry it returns.
func WithFields(fields map[string]interface{}) Logger {
	return logrus.WithFields(fields)
}

// Debug logs a message at level Debug on the standard logger.
func Debug(msg string, args ...interface{}) {
	Debugf(msg, args...)
}

// Print logs a message at level Info on the standard logger.
func Print(msg string, args ...interface{}) {
	Printf(msg, args...)
}

// Info logs a message at level Info on the standard logger.
func Info(msg string, args ...interface{}) {
	Infof(msg, args...)
}

// Warn logs a message at level Warn on the standard logger.
func Warn(msg string, args ...interface{}) {
	Warnf(msg, args...)
}

// Warning logs a message at level Warn on the standard logger.
func Warning(msg string, args ...interface{}) {
	Warning(msg, args...)
}

// Error logs a message at level Error on the standard logger.
func Error(msg string, args ...interface{}) {
	Errorf(msg, args...)
}

// Panic logs a message at level Panic on the standard logger.
func Panic(msg string, args ...interface{}) {
	Panicf(msg, args...)
}

// Fatal logs a message at level Fatal on the standard logger.
func Fatal(msg string, args ...interface{}) {
	Fatalf(msg, args...)
}

// Debugf logs a message at level Debug on the standard logger.
func Debugf(format string, args ...interface{}) {
	logrus.Debugf(format, args...)
}

// Printf logs a message at level Info on the standard logger.
func Printf(format string, args ...interface{}) {
	logrus.Printf(format, args...)
}

// Infof logs a message at level Info on the standard logger.
func Infof(format string, args ...interface{}) {
	logrus.Infof(format, args...)
}

// Warnf logs a message at level Warn on the standard logger.
func Warnf(format string, args ...interface{}) {
	logrus.Warnf(format, args...)
}

// Warningf logs a message at level Warn on the standard logger.
func Warningf(format string, args ...interface{}) {
	logrus.Warningf(format, args...)
}

// Errorf logs a message at level Error on the standard logger.
func Errorf(format string, args ...interface{}) {
	logrus.Errorf(format, args...)
}

// Panicf logs a message at level Panic on the standard logger.
func Panicf(format string, args ...interface{}) {
	logrus.Panicf(format, args...)
}

// Fatalf logs a message at level Fatal on the standard logger.
func Fatalf(format string, args ...interface{}) {
	logrus.Fatalf(format, args...)
}

// Debugln logs a message at level Debug on the standard logger.
func Debugln(args ...interface{}) {
	logrus.Debugln(args...)
}

// Println logs a message at level Info on the standard logger.
func Println(args ...interface{}) {
	logrus.Println(args...)
}

// Infoln logs a message at level Info on the standard logger.
func Infoln(args ...interface{}) {
	logrus.Infoln(args...)
}

// Warnln logs a message at level Warn on the standard logger.
func Warnln(args ...interface{}) {
	logrus.Warnln(args...)
}

// Warningln logs a message at level Warn on the standard logger.
func Warningln(args ...interface{}) {
	logrus.Warningln(args...)
}

// Errorln logs a message at level Error on the standard logger.
func Errorln(args ...interface{}) {
	logrus.Errorln(args...)
}

// Panicln logs a message at level Panic on the standard logger.
func Panicln(args ...interface{}) {
	logrus.Panicln(args...)
}

// Fatalln logs a message at level Fatal on the standard logger.
func Fatalln(args ...interface{}) {
	logrus.Fatalln(args...)
}
