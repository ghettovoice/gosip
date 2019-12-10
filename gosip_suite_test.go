package gosip_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestGosip(t *testing.T) {
	RegisterFailHandler(Fail)
	RegisterTestingT(t)
	RunSpecs(t, "GoSip Suite")
}
