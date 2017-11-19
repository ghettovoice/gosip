package log

import (
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"github.com/x-cray/logrus-prefixed-formatter"
)

const PrefixFormat = "%s (%s:%s)"

// Default log entry formatter.
// Uses logrus-prefixed-formatter as base formatter.
// "prefix" field used to add caller info to log.
type Formatter struct {
	*prefixed.TextFormatter
	ShortNames bool
}

func NewFormatter(shortNames bool) *Formatter {
	return &Formatter{
		TextFormatter: &prefixed.TextFormatter{
			ForceFormatting: true,
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05.000",
		},
		ShortNames: shortNames,
	}
}

func (f *Formatter) Format(entry *logrus.Entry) ([]byte, error) {
	// merge "file", "line", "func" entry field to prefix for pretty format
	undef := "???"
	prefixFormat := PrefixFormat
	args := make([]interface{}, 0)

	if val, ok := entry.Data["func"].(string); ok {
		if f.ShortNames {
			val = filepath.Base(val)
		}
		delete(entry.Data, "func")
		args = append(args, val)
	} else {
		args = append(args, undef)
	}
	if val, ok := entry.Data["file"].(string); ok {
		if f.ShortNames {
			val = filepath.Base(val)
		}
		delete(entry.Data, "file")
		args = append(args, val)
	} else {
		args = append(args, undef)
	}
	if val, ok := entry.Data["line"].(string); ok {
		delete(entry.Data, "line")
		args = append(args, val)
	} else {
		args = append(args, undef)
	}

	entry.Data["prefix"] = fmt.Sprintf(prefixFormat, args...)

	return f.TextFormatter.Format(entry)
}
