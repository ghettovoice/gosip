package transaction_test

import (
	"sync"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transaction"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ClientTx", func() {
	var (
		tpl *testutils.MockTransportLayer
		txl transaction.Layer
	)

	clientAddr := "localhost:9001"

	BeforeEach(func() {
		tpl = testutils.NewMockTransportLayer()
		txl = transaction.NewLayer(tpl)
	})
	AfterEach(func(done Done) {
		txl.Cancel()
		<-txl.Done()
		close(done)
	}, 3)

	Context("just initialized", func() {
		It("should has transport layer", func() {
			Expect(txl.Transport()).To(Equal(tpl))
		})
	})

	Context("sends INVITE request", func() {
		var inviteTxKey, ackTxKey transaction.TxKey
		var err error
		var invite, trying, ok, notOk, ack, notOkAck sip.Message
		var inviteBranch string
		var invTx transaction.Tx

		BeforeEach(func() {
			inviteBranch = sip.GenerateBranch()
			invite = testutils.Request([]string{
				"INVITE sip:bob@example.com SIP/2.0",
				"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + inviteBranch,
				"CSeq: 1 INVITE",
				"",
				"",
			})
			trying = testutils.Response([]string{
				"SIP/2.0 100 Trying",
				"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + inviteBranch,
				"CSeq: 1 INVITE",
				"",
				"",
			})
			ok = testutils.Response([]string{
				"SIP/2.0 200 OK",
				"CSeq: 1 INVITE",
				"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + inviteBranch,
				"",
				"",
			})
			notOk = testutils.Response([]string{
				"SIP/2.0 400 Bad Request",
				"CSeq: 1 INVITE",
				"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + inviteBranch,
				"",
				"",
			})
			ack = testutils.Request([]string{
				"ACK sip:bob@example.com SIP/2.0",
				"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + sip.GenerateBranch(),
				"CSeq: 1 ACK",
				"",
				"",
			})
			notOkAck = testutils.Request([]string{
				"ACK sip:bob@example.com SIP/2.0",
				"Via: SIP/2.0/UDP " + clientAddr + ";branch=" + inviteBranch,
				"CSeq: 1 ACK",
				"",
				"",
			})
		})

		It("should send INVITE request", func(done Done) {
			go func() {
				defer close(done)
				msg := <-tpl.OutMsgs
				Expect(msg).ToNot(BeNil())
				Expect(msg.String()).To(Equal(invite.String()))
			}()

			inviteTxKey, err = transaction.MakeClientTxKey(invite)
			Expect(err).ToNot(HaveOccurred())
			ackTxKey, err = transaction.MakeClientTxKey(ack)
			Expect(err).ToNot(HaveOccurred())

			tx, err := txl.Send(invite)
			Expect(err).ToNot(HaveOccurred())
			Expect(tx.Key()).To(Equal(inviteTxKey))
		})

		Context("receives 200 OK on INVITE", func() {
			wg := new(sync.WaitGroup)
			BeforeEach(func() {
				wg.Add(1)
				go func() {
					defer wg.Done()
					msg := <-tpl.OutMsgs
					Expect(msg).ToNot(BeNil())
					Expect(msg.String()).To(Equal(invite.String()))

					time.Sleep(200 * time.Millisecond)
					tpl.InMsgs <- trying

					time.Sleep(200 * time.Millisecond)
					tpl.InMsgs <- ok
				}()

				invTx, err = txl.Send(invite)
				Expect(err).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				wg.Wait()
			})

			It("should receive responses in INVITE tx", func() {
				var msg transaction.TxMessage
				msg = <-txl.Messages()
				Expect(msg).ToNot(BeNil())
				Expect(msg.String()).To(Equal(trying.String()))
				Expect(msg.Tx()).To(Equal(invTx))

				msg = <-txl.Messages()
				Expect(msg).ToNot(BeNil())
				Expect(msg.String()).To(Equal(ok.String()))
				Expect(msg.Tx()).To(Equal(invTx))
			})
		})

		Context("receives 400 Bad Request on INVITE", func() {
			wg := new(sync.WaitGroup)

			BeforeEach(func() {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var msg sip.Message
					msg = <-tpl.OutMsgs
					Expect(msg).ToNot(BeNil())
					Expect(msg.String()).To(Equal(invite.String()))

					time.Sleep(200 * time.Millisecond)
					tpl.InMsgs <- trying

					time.Sleep(200 * time.Millisecond)
					tpl.InMsgs <- notOk

					msg = <-tpl.OutMsgs
					Expect(msg).ToNot(BeNil())
					req, ok := msg.(sip.Request)
					Expect(ok).To(BeTrue())
					Expect(string(req.Method())).To(Equal("ACK"))
				}()

				invTx, err = txl.Send(invite)
				Expect(err).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				wg.Wait()
			})

			It("should receive responses in INVITE tx and send ACK", func() {
				var msg transaction.TxMessage
				msg = <-txl.Messages()
				Expect(msg).ToNot(BeNil())
				Expect(msg.String()).To(Equal(trying.String()))
				Expect(msg.Tx()).To(Equal(invTx))

				msg = <-txl.Messages()
				Expect(msg).ToNot(BeNil())
				Expect(msg.String()).To(Equal(notOk.String()))
				Expect(msg.Tx()).To(Equal(invTx))
			})
		})
	})
})
