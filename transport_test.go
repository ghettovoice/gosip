package gosip_test

import (
	"sync"

	"github.com/ghettovoice/gosip"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Transport", func() {
	var (
		tp gosip.Transport
		wg *sync.WaitGroup
	)
	hostAddr := "192.168.0.1:5060"

	BeforeEach(func() {
		wg = new(sync.WaitGroup)
		tp = gosip.NewTransport(hostAddr)
	})
	AfterEach(func(done Done) {
		wg.Wait()
		tp.Cancel()
		<-tp.Done()
		close(done)
	}, 3)

	Context("just initialized", func() {
		It("should has correct host address", func() {
			Expect(tp.HostAddr()).To(Equal(hostAddr))
		})
	})
})
