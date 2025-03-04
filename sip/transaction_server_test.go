package sip_test

import (
	"testing"

	"github.com/ghettovoice/gosip/sip"
)

func TestServerTransactionKey_RoundTripBinary(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		key  sip.ServerTransactionKey
	}{
		{
			name: "rfc3261",
			key: sip.ServerTransactionKey{
				Branch: "z9hG4bK-123",
				SentBy: "Example.com:5060",
				Method: "INVITE",
			},
		},
		{
			name: "rfc2543",
			key: sip.ServerTransactionKey{
				Method:  "INVITE",
				URI:     "sip:user@example.com",
				FromTag: "from",
				ToTag:   "to",
				CallID:  "call",
				CSeqNum: 42,
				Via:     "SIP/2.0/UDP example.com:5060",
			},
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			original := c.key
			data, err := original.MarshalBinary()
			if err != nil {
				t.Fatalf("key.MarshalBinary() error = %v", err)
			}
			if len(data) == 0 {
				t.Fatalf("key.MarshalBinary() = %v, want non-empty", data)
			}

			t.Logf("hash: %x", data)

			var restored sip.ServerTransactionKey
			if err := restored.UnmarshalBinary(data); err != nil {
				t.Fatalf("new.UnmarshalBinary(data) error = %v, want nil", err)
			}

			if !original.Equal(&restored) {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", restored, original)
			}
		})
	}
}

func TestServerTransactionKey_UnmarshalBinaryInvalid(t *testing.T) {
	t.Parallel()

	var key sip.ServerTransactionKey
	if err := key.UnmarshalBinary(nil); err == nil {
		t.Fatalf("key.UnmarshalBinary(nil) = nil, want error")
	}

	if err := key.UnmarshalBinary([]byte{0x03}); err == nil {
		t.Fatalf("key.UnmarshalBinary([]byte{0x03}) = nil, want error")
	}
}
