package transaction_test

import (
	"os"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/log"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestTransaction(t *testing.T) {
	// setup logger
	lvl := log.ErrorLevel
	forceColor := true
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--test.v") || strings.HasPrefix(arg, "--ginkgo.v") {
			lvl = log.DebugLevel
		} else if strings.HasPrefix(arg, "--ginkgo.noColor") {
			forceColor = false
		}
	}
	log.SetLevel(lvl)
	log.SetFormatter(log.NewFormatter(true, forceColor))

	RegisterFailHandler(Fail)
	RegisterTestingT(t)
	RunSpecs(t, "Transaction Suite")
}
