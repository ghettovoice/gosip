package log

import (
	"fmt"
	"regexp"
	"runtime"

	"github.com/sirupsen/logrus"
)

const UndefStack = "???"
const (
	stackNumFrames = 20
	// constant value of stack offset from logrus.Logger.* fn call to current hook Fire call
	hookStackDelta = 5
)

// CallerHook is an hook for logrus logger that adds file, line, function info.
type CallerHook struct{}

// Fire is an callback that will be called by logrus for each log entry.
func (sh *CallerHook) Fire(entry *logrus.Entry) error {
	file, line, fn := GetStackInfo()
	entry.Data["file"] = file
	entry.Data["line"] = line
	entry.Data["func"] = fn

	return nil
}

// Levels returns CallerHook working levels.
func (sh *CallerHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func GetStackInfo() (string, string, string) {
	// Get information about the stack.
	// Try and find the first stack frame outside the logging package.
	// Only search up a few frames, it should never be very far.
	file := UndefStack
	line := UndefStack
	fn := UndefStack

	for depth := hookStackDelta; depth < stackNumFrames+hookStackDelta; depth++ {
		if pc, cfile, cline, ok := runtime.Caller(depth); ok {
			funcName := runtime.FuncForPC(pc).Name()

			// Go up another stack frame if this function is in the logging package.
			isLog, _ := regexp.MatchString(`(log\w*\..*)`, funcName)
			if isLog {
				continue
			}

			// Now generate the string
			file = cfile
			line = fmt.Sprintf("%d", cline)
			fn = funcName
			break
		}

		// If we get here, we failed to retrieve the stack information.
		// Just give up.
		break
	}

	return file, line, fn
}
