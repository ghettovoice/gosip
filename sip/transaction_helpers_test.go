package sip_test

import (
	"context"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
)

func registerTransactionCleanup(tb testing.TB, tx sip.Transaction) {
	tb.Helper()
	tb.Cleanup(func() {
		if tx.State() == sip.TransactionStateTerminated {
			return
		}
		if err := tx.Terminate(context.Background(), nil); err != nil {
			tb.Errorf("tx.Terminate() cleanup error = %v, want nil", errors.Wrap(err))
		}
	})
}

type stubTransactionDefaults struct{}

func (stubTransactionDefaults) MatchMessage(sip.Message) bool            { return true }
func (stubTransactionDefaults) LastError() error                         { return nil }
func (stubTransactionDefaults) BindErrorHandler(sip.ErrorHandler) func() { return func() {} }
func (stubTransactionDefaults) Terminate(context.Context, error) error   { return nil }

type stubClientTransactionDefaults struct{}

func (stubClientTransactionDefaults) Request() *sip.RequestEnvelope { return nil }
func (stubClientTransactionDefaults) LastResponse() *sip.ResponseEnvelope {
	return nil
}
func (stubClientTransactionDefaults) Transport() sip.ClientTransport { return nil }
func (stubClientTransactionDefaults) RecvResponse(context.Context, *sip.ResponseEnvelope) error {
	return nil
}

func (stubClientTransactionDefaults) BindResponseHandler(sip.InboundResponseHandler) func() {
	return func() {}
}

type stubServerTransactionDefaults struct{}

func (stubServerTransactionDefaults) Request() *sip.RequestEnvelope { return nil }
func (stubServerTransactionDefaults) LastResponse() *sip.ResponseEnvelope {
	return nil
}
func (stubServerTransactionDefaults) Transport() sip.ServerTransport { return nil }
func (stubServerTransactionDefaults) RecvRequest(context.Context, *sip.RequestEnvelope) error {
	return nil
}

func (stubServerTransactionDefaults) SendResponse(
	context.Context,
	*sip.ResponseEnvelope,
	...sip.SendResponseOptions,
) error {
	return nil
}

func (stubServerTransactionDefaults) Respond(context.Context, sip.ResponseStatus, ...sip.RespondOptions) error {
	return nil
}
