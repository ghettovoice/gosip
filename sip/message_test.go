package sip

import (
	"fmt"
	"testing"
)

// Generic test for testing anything with a String() method.
type stringTest struct {
	description string
	input       fmt.Stringer
	expected    string
}

func doTests(tests []stringTest, t *testing.T) {
	passed := 0
	for _, test := range tests {
		if test.input.String() != test.expected {
			t.Errorf("[FAIL] %v: Expected: \"%v\", Got: \"%v\"",
				test.description,
				test.expected,
				test.input.String(),
			)
		} else {
			passed++
		}
	}
	t.Logf("Passed %v/%v tests", passed, len(tests))
}

// Some global ports to use since port is still a pointer.
var port5060 Port = 5060
var port6060 Port = 6060
var noParams = NewParams()

func TestMessage_String(t *testing.T) {
	callId := CallID("call-1234567890")

	doTests([]stringTest{
		{
			"Basic request test",
			NewRequest(
				"",
				"INVITE",
				&SipUri{
					FUser:      String{"bob"},
					FHost:      "far-far-away.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				"SIP/2.0",
				[]Header{
					&ToHeader{
						DisplayName: String{"bob"},
						Address: &SipUri{
							FUser:      String{"bob"},
							FHost:      "far-far-away.com",
							FUriParams: noParams,
							FHeaders:   noParams,
						},
						Params: noParams,
					},
					&FromHeader{
						DisplayName: String{"alice"},
						Address: &SipUri{
							FUser:      String{"alice"},
							FHost:      "wonderland.com",
							FUriParams: noParams,
							FHeaders:   noParams,
						},
						Params: NewParams().Add("tag", String{"qwerty"}),
					},
					&callId,
				},
				"",
				nil,
			),
			"INVITE sip:bob@far-far-away.com SIP/2.0\r\n" +
				"To: \"bob\" <sip:bob@far-far-away.com>\r\n" +
				"From: \"alice\" <sip:alice@wonderland.com>;tag=qwerty\r\n" +
				"Call-ID: call-1234567890\r\n" +
				"\r\n",
		},
	}, t)
}

func TestSipUri_String(t *testing.T) {
	doTests([]stringTest{
		{
			"Basic SIP URI",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com",
		},
		{
			"SIP URI with no user",
			&SipUri{
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:wonderland.com",
		},
		{
			"SIP URI with password",
			&SipUri{
				FUser:      String{"alice"},
				FPassword:  String{"hunter2"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice:hunter2@wonderland.com",
		},
		{
			"SIP URI with explicit port 5060",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FPort:      &port5060,
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com:5060",
		},
		{
			"SIP URI with other port",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FPort:      &port6060,
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com:6060",
		},
		{
			"Basic SIPS URI",
			&SipUri{
				FIsEncrypted: true,
				FUser:        String{"alice"},
				FHost:        "wonderland.com",
				FUriParams:   noParams,
				FHeaders:     noParams,
			},
			"sips:alice@wonderland.com",
		},
		{
			"SIP URI with one parameter",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: NewParams().Add("food", String{"cake"}),
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com;food=cake",
		},
		{
			"SIP URI with one no-value parameter",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: NewParams().Add("something", nil),
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com;something",
		},
		{
			"SIP URI with three parameters",
			&SipUri{
				FUser: String{"alice"},
				FHost: "wonderland.com",
				FUriParams: NewParams().Add("food", String{"cake"}).
					Add("something", nil).
					Add("drink", String{"tea"}),
				FHeaders: noParams,
			},
			"sip:alice@wonderland.com;food=cake;something;drink=tea",
		},
		{
			"SIP URI with one header",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   NewParams().Add("CakeLocation", String{"Tea Party"}),
			},
			"sip:alice@wonderland.com?CakeLocation=\"Tea Party\"",
		},
		{
			"SIP URI with three headers",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders: NewParams().Add("CakeLocation", String{"Tea Party"}).
					Add("Identity", String{"Mad Hatter"}).
					Add("OtherHeader", String{"Some value"})},
			"sip:alice@wonderland.com?CakeLocation=\"Tea Party\"&Identity=\"Mad Hatter\"&OtherHeader=\"Some value\"",
		},
		{
			"SIP URI with parameter and header",
			&SipUri{
				FUser:      String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: NewParams().Add("food", String{"cake"}),
				FHeaders:   NewParams().Add("CakeLocation", String{"Tea Party"}),
			},
			"sip:alice@wonderland.com;food=cake?CakeLocation=\"Tea Party\"",
		},
		{
			"Wildcard URI",
			&WildcardUri{},
			"*",
		},
	}, t)
}

func TestHeaders_String(t *testing.T) {
	doTests([]stringTest{
		// To Headers.
		{
			"Basic To Header",
			&ToHeader{
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: noParams,
			},
			"To: <sip:alice@wonderland.com>",
		},
		{
			"To Header with display name",
			&ToHeader{
				DisplayName: String{"Alice Liddell"},
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: noParams,
			},
			"To: \"Alice Liddell\" <sip:alice@wonderland.com>",
		},
		{
			"To Header with parameters",
			&ToHeader{
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: NewParams().Add("food", String{"cake"}),
			},
			"To: <sip:alice@wonderland.com>;food=cake",
		},

		// From Headers.
		{
			"Basic From Header",
			&FromHeader{
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: noParams,
			},
			"From: <sip:alice@wonderland.com>",
		},
		{
			"From Header with display name",
			&FromHeader{
				DisplayName: String{"Alice Liddell"},
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: noParams,
			},
			"From: \"Alice Liddell\" <sip:alice@wonderland.com>",
		},
		{
			"From Header with parameters",
			&FromHeader{
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: NewParams().Add("food", String{"cake"}),
			},
			"From: <sip:alice@wonderland.com>;food=cake",
		},

		// Contact Headers
		{
			"Basic Contact Header",
			&ContactHeader{
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: noParams,
			},
			"Contact: <sip:alice@wonderland.com>",
		},
		{
			"Contact Header with display name",
			&ContactHeader{
				DisplayName: String{"Alice Liddell"},
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: noParams,
			},
			"Contact: \"Alice Liddell\" <sip:alice@wonderland.com>",
		},
		{
			"Contact Header with parameters",
			&ContactHeader{
				Address: &SipUri{
					FUser:      String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: NewParams().Add("food", String{"cake"}),
			},
			"Contact: <sip:alice@wonderland.com>;food=cake",
		},
		{
			"Contact Header with Wildcard URI",
			&ContactHeader{
				Address: &WildcardUri{},
				Params:  noParams,
			},
			"Contact: *",
		},
		{
			"Contact Header with display name and Wildcard URI",
			&ContactHeader{
				DisplayName: String{"Mad Hatter"},
				Address:     &WildcardUri{},
				Params:      noParams,
			},
			"Contact: \"Mad Hatter\" *",
		},
		{
			"Contact Header with Wildcard URI and parameters",
			&ContactHeader{
				Address: &WildcardUri{},
				Params:  NewParams().Add("food", String{"cake"}),
			},
			"Contact: *;food=cake",
		},

		// Via Headers.
		{
			"Basic Via Header",
			ViaHeader{
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Params:          NewParams(),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com",
		},
		{
			"Via Header with port",
			ViaHeader{
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Port:            &port6060,
					Params:          NewParams(),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com:6060",
		},
		{
			"Via Header with params",
			ViaHeader{
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Port:            &port6060,
					Params: NewParams().Add("food", String{"cake"}).
						Add("delicious", nil),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com:6060;food=cake;delicious",
		},
		{
			"Via Header with 3 simple hops",
			ViaHeader{
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Params:          NewParams(),
				},
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "TCP",
					Host:            "looking-glass.net",
					Params:          NewParams(),
				},
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "oxford.co.uk",
					Params:          NewParams(),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com, SIP/2.0/TCP looking-glass.net, SIP/2.0/UDP oxford.co.uk",
		},
		{
			"Via Header with 3 complex hops",
			ViaHeader{
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Port:            &port5060,
					Params:          NewParams(),
				},
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "TCP",
					Host:            "looking-glass.net",
					Port:            &port6060,
					Params:          NewParams().Add("food", String{"cake"}),
				},
				&ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "oxford.co.uk",
					Params:          NewParams().Add("delicious", nil),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com:5060, SIP/2.0/TCP looking-glass.net:6060;food=cake, SIP/2.0/UDP oxford.co.uk;delicious",
		},

		// Require Headers.
		{
			"Require Header (empty)",
			&RequireHeader{[]string{}},
			"Require: ",
		},
		{
			"Require Header (one option)",
			&RequireHeader{[]string{"NewFeature1"}},
			"Require: NewFeature1",
		},
		{
			"Require Header (three options)",
			&RequireHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Require: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Supported Headers.
		{
			"Supported Header (empty)",
			&SupportedHeader{[]string{}},
			"Supported: ",
		},
		{
			"Supported Header (one option)",
			&SupportedHeader{[]string{"NewFeature1"}},
			"Supported: NewFeature1",
		},
		{
			"Supported Header (three options)",
			&SupportedHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Supported: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Proxy-Require Headers.
		{
			"Proxy-Require Header (empty)",
			&ProxyRequireHeader{[]string{}},
			"Proxy-Require: ",
		},
		{
			"Proxy-Require Header (one option)",
			&ProxyRequireHeader{[]string{"NewFeature1"}},
			"Proxy-Require: NewFeature1",
		},
		{
			"Proxy-Require Header (three options)",
			&ProxyRequireHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Proxy-Require: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Unsupported Headers.
		{
			"Unsupported Header (empty)",
			&UnsupportedHeader{[]string{}},
			"Unsupported: ",
		},
		{
			"Unsupported Header (one option)",
			&UnsupportedHeader{[]string{"NewFeature1"}},
			"Unsupported: NewFeature1",
		},
		{
			"Unsupported Header (three options)",
			&UnsupportedHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Unsupported: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Various simple headers.
		{
			"Call-ID Header",
			CallID("Call-ID-1"),
			"Call-ID: Call-ID-1",
		},
		{
			"CSeq Header",
			&CSeq{1234, "INVITE"},
			"CSeq: 1234 INVITE",
		},
		{
			"Max Forwards Header",
			MaxForwards(70),
			"Max-Forwards: 70",
		},
		{
			"Content Length Header",
			ContentLength(70),
			"Content-Length: 70",
		},
	}, t)
}
