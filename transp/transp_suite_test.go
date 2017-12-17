package transp_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transp"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	localAddr1 = fmt.Sprintf("%v:%v", transp.DefaultHost, transp.DefaultTcpPort)
	localAddr2 = fmt.Sprintf("%v:%v", transp.DefaultHost, transp.DefaultTcpPort+1)
	localAddr3 = fmt.Sprintf("%v:%v", transp.DefaultHost, transp.DefaultTcpPort+2)
)

func TestTransp(t *testing.T) {
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

	// setup Ginkgo
	RegisterFailHandler(Fail)
	RegisterTestingT(t)
	RunSpecs(t, "Transp Suite")
}
