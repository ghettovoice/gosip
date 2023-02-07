package gosip_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGosip(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GoSip Suite")
}
