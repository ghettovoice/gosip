package transaction_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestTransaction(t *testing.T) {
	RegisterFailHandler(Fail)
	RegisterTestingT(t)
	RunSpecs(t, "Transaction Suite")
}
