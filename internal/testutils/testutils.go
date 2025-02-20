package testutils

import (
	"os"
	"path/filepath"
	"strings"
)

var ProjectRoot string

func init() {
	ProjectRoot = findRoot()
}

func findRoot() string {
	cwd, err := os.Getwd()
	cwdOrig := cwd
	if err != nil {
		panic(err)
	}
	sep := string(filepath.Separator)
	for {
		if strings.HasSuffix(cwd, sep+"gosip") {
			return cwd
		}
		lastSlashIndex := strings.LastIndex(cwd, sep)
		if lastSlashIndex == -1 {
			panic(cwdOrig + ` did not contain "gosip"`)
		}
		cwd = cwd[0:lastSlashIndex]
	}
}
