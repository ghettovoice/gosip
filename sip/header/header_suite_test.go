package header_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
)

func TestHeader(t *testing.T) {
	format.MaxLength = 0

	RegisterFailHandler(Fail)
	RunSpecs(t, "Header Suite")
}
