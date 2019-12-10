package transport_test

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/transport"
)

var (
	localAddr1 = fmt.Sprintf("%v:%v", transport.DefaultHost, transport.DefaultTcpPort)
	localAddr2 = fmt.Sprintf("%v:%v", transport.DefaultHost, transport.DefaultTcpPort+1)
	localAddr3 = fmt.Sprintf("%v:%v", transport.DefaultHost, transport.DefaultTcpPort+2)
)

func TestTransport(t *testing.T) {
	// setup Ginkgo
	RegisterFailHandler(Fail)
	RegisterTestingT(t)
	RunSpecs(t, "Transport Suite")
}
