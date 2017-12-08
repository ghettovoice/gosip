package log

import (
	"fmt"
	"path/filepath"

	"github.com/ghettovoice/logrus"
	"github.com/ghettovoice/logrus-prefixed-formatter"
)

const PrefixFormat = "%s (%s:%s)"

// Default log entry formatter.
// Uses logrus-prefixed-formatter as base formatter.
// "prefix" field used to add caller info to log.
type Formatter struct {
	*prefixed.TextFormatter
	ShortNames bool
}

func NewFormatter(shortNames bool, forceColors bool) *Formatter {
	return &Formatter{
		TextFormatter: &prefixed.TextFormatter{
			ForceFormatting: true,
			ForceColors:     forceColors,
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
	fields := entry.Fields()

	if val, ok := fields["func"].(string); ok {
		if f.ShortNames {
			val = filepath.Base(val)
		}
		args = append(args, val)
	} else {
		args = append(args, undef)
	}
	if val, ok := fields["file"].(string); ok {
		if f.ShortNames {
			val = filepath.Base(val)
		}
		args = append(args, val)
	} else {
		args = append(args, undef)
	}
	if val, ok := fields["line"].(string); ok {
		args = append(args, val)
	} else {
		args = append(args, undef)
	}

	entry.SetField("prefix", fmt.Sprintf(prefixFormat, args...))

	return f.TextFormatter.Format(entry)
}
