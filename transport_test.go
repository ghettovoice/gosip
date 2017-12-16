package gosip_test

import (
	"sync"

	"github.com/ghettovoice/gosip"
	"github.com/ghettovoice/gosip/core"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Transport", func() {
	var (
		output chan core.Message
		errs   chan error
		cancel chan struct{}
		tp     gosip.Transport
		wg     *sync.WaitGroup
	)

	BeforeEach(func() {
		output = make(chan core.Message)
		errs = make(chan error)
		cancel = make(chan struct{})
	})
	AfterEach(func(done Done) {
		wg.Wait()
		select {
		case <-cancel:
		default:
			close(cancel)
		}
		<-tp.Done()
		close(output)
		close(errs)
		close(done)
	}, 3)

	Context("just initialized", func() {

	})
})
