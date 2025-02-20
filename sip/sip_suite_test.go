package sip_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	. "github.com/onsi/gomega/gleak"
)

func TestSip(t *testing.T) {
	format.MaxLength = 0

	RegisterFailHandler(Fail)
	RunSpecs(t, "SIP Suite")
}

var _ = BeforeSuite(func() {
	IgnoreGinkgoParallelClient()
})
