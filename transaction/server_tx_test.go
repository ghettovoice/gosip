package transaction_test

import (
	"fmt"
	"sync"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/testutils"
	"github.com/ghettovoice/gosip/transaction"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ServerTx", func() {
	var (
		tpl *testutils.MockTransportLayer
		txl transaction.Layer
	)

	// serverAddr := "localhost:8001"
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
	// TODO: think about how to test Tx state switches and deletion
	Context("when INVITE request arrives", func() {
		var err error
		var invite, trying, ok, notOk, ack, notOkAck sip.Message
		var inviteBranch string
		wg := new(sync.WaitGroup)

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

			wg.Add(1)
			go func() {
				defer wg.Done()
				By(fmt.Sprintf("UAC sends %s", invite.Short()))
				tpl.InMsgs <- invite
			}()
		})
		AfterEach(func(done Done) {
			wg.Wait()
			close(done)
		}, 3)

		It("should open server tx and pass up TxMessage", func() {
			_, err = transaction.MakeServerTxKey(invite)
			Expect(err).ToNot(HaveOccurred())

			By(fmt.Sprintf("UAS receives %s", invite.Short()))
			tx := <-txl.Requests()
			Expect(tx).ToNot(BeNil())
			Expect(tx.Origin().String()).To(Equal(invite.String()))
		})

		Context("when INVITE server tx created", func() {
			var tx sip.ServerTransaction
			mu := &sync.RWMutex{}
			BeforeEach(func() {
				mu.Lock()
				tx = <-txl.Requests()
				Expect(tx).ToNot(BeNil())
				mu.Unlock()
			})

			It("should send 100 Trying after Timer_1xx fired", func() {
				time.Sleep(transaction.Timer_1xx + time.Millisecond)
				By(fmt.Sprintf("UAC waits %s", trying.Short()))
				msg := <-tpl.OutMsgs
				Expect(msg).ToNot(BeNil())
				Expect(msg.String()).To(Equal(trying.String()))
			})

			It("should send in transaction", func(done Done) {
				go func() {
					defer close(done)
					By(fmt.Sprintf("UAC waits %s", ok.Short()))
					msg := <-tpl.OutMsgs
					Expect(msg).ToNot(BeNil())
					Expect(msg.String()).To(Equal(ok.String()))
				}()

				By(fmt.Sprintf("UAS sends %s", ok.Short()))
				_, err := txl.Respond(ok.(sip.Response))
				Expect(err).ToNot(HaveOccurred())
			})

			Context("after 2xx OK was sent", func() {
				wg2 := new(sync.WaitGroup)
				BeforeEach(func() {
					wg2.Add(2)
					go func() {
						defer wg2.Done()
						By(fmt.Sprintf("UAS sends %s", ok.Short()))
						_, err := txl.Respond(ok.(sip.Response))
						Expect(err).To(BeNil())
					}()
					go func() {
						defer wg2.Done()
						By(fmt.Sprintf("UAC waits %s", ok.Short()))
						msg := <-tpl.OutMsgs
						Expect(msg).ToNot(BeNil())
						Expect(msg.String()).To(Equal(ok.String()))

						time.Sleep(time.Millisecond)
						By(fmt.Sprintf("UAC sends %s", ack.Short()))
						tpl.InMsgs <- ack
					}()
				})
				AfterEach(func(done Done) {
					wg2.Wait()
					close(done)
				}, 3)

				It("should receive ACK in separate transaction", func(done Done) {
					_, err = transaction.MakeServerTxKey(ack)
					Expect(err).ToNot(HaveOccurred())

					By(fmt.Sprintf("UAS receives %s", ack.Short()))
					ackReq := <-txl.Requests()
					Expect(ackReq).ToNot(BeNil())
					Expect(ackReq.Origin().String()).To(Equal(ack.String()))

					close(done)
				})
			})

			Context("after 3xx was sent", func() {
				var tx sip.ServerTransaction
				wg := new(sync.WaitGroup)
				BeforeEach(func() {
					wg.Add(2)
					go func() {
						defer wg.Done()
						By(fmt.Sprintf("UAS sends %s", notOk.Short()))
						mu.Lock()
						tx, err = txl.Respond(notOk.(sip.Response))
						Expect(tx).ToNot(BeNil())
						Expect(err).To(BeNil())
						mu.Unlock()
					}()
					go func() {
						defer wg.Done()
						By(fmt.Sprintf("UAC waits %s", notOk.Short()))
						select {
						case <-time.After(time.Second):
							panic("wait freezed")
						case msg := <-tpl.OutMsgs:
							Expect(msg).ToNot(BeNil())
							Expect(msg.String()).To(Equal(notOk.String()))
						}

						time.Sleep(time.Millisecond)
						By(fmt.Sprintf("UAC sends %s", notOkAck.Short()))

						go func() {
							tpl.InMsgs <- notOkAck
						}()
					}()

					wg.Wait()
				})

				It("should get ACK inside INVITE tx", func(done Done) {
					mu.RLock()
					ack := <-tx.Acks()
					mu.RUnlock()
					Expect(ack).ToNot(BeNil())
					Expect(ack.Method()).To(Equal(sip.ACK))

					close(done)
				})
			})
		})
	})
})
