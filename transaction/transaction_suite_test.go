package transaction_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/parser"
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

func message(rawMsg []string) sip.Message {
	msg, err := parser.ParseMessage([]byte(strings.Join(rawMsg, "\r\n")), log.StandardLogger())
	Expect(err).ToNot(HaveOccurred())
	return msg
}

func request(rawMsg []string) sip.Request {
	msg := message(rawMsg)
	switch msg := msg.(type) {
	case sip.Request:
		return msg
	case sip.Response:
		Fail(fmt.Sprintf("%s is not a request", msg.Short()))
	default:
		Fail(fmt.Sprintf("%s is not a request", msg.Short()))
	}
	return nil
}

func response(rawMsg []string) sip.Response {
	msg := message(rawMsg)
	switch msg := msg.(type) {
	case sip.Response:
		return msg
	case sip.Request:
		Fail(fmt.Sprintf("%s is not a response", msg.Short()))
	default:
		Fail(fmt.Sprintf("%s is not a response", msg.Short()))
	}
	return nil
}
