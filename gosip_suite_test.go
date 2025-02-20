package gosip_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gleak"
)

func TestGoSIP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GoSIP Suite")
}

var _ = BeforeSuite(func() {
	IgnoreGinkgoParallelClient()
})
