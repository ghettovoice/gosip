package sip_test

import (
	"fmt"
	"testing"

	"github.com/ghettovoice/gosip/sip"
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
var port5060 sip.Port = 5060
var port6060 sip.Port = 6060
var noParams = sip.NewParams()

func TestMessage_String(t *testing.T) {
	callId := sip.CallID("call-1234567890")

	doTests([]stringTest{
		{
			"Basic request test",
			sip.NewRequest(
				"",
				"INVITE",
				&sip.SipUri{
					FUser:      sip.String{"bob"},
					FHost:      "far-far-away.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				"SIP/2.0",
				[]sip.Header{
					&sip.ToHeader{
						DisplayName: sip.String{"bob"},
						Address: &sip.SipUri{
							FUser:      sip.String{"bob"},
							FHost:      "far-far-away.com",
							FUriParams: noParams,
							FHeaders:   noParams,
						},
						Params: noParams,
					},
					&sip.FromHeader{
						DisplayName: sip.String{"alice"},
						Address: &sip.SipUri{
							FUser:      sip.String{"alice"},
							FHost:      "wonderland.com",
							FUriParams: noParams,
							FHeaders:   noParams,
						},
						Params: sip.NewParams().Add("tag", sip.String{"qwerty"}),
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
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com",
		},
		{
			"SIP URI with no user",
			&sip.SipUri{
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:wonderland.com",
		},
		{
			"SIP URI with password",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FPassword:  sip.String{"hunter2"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice:hunter2@wonderland.com",
		},
		{
			"SIP URI with explicit port 5060",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FPort:      &port5060,
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com:5060",
		},
		{
			"SIP URI with other port",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FPort:      &port6060,
				FUriParams: noParams,
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com:6060",
		},
		{
			"Basic SIPS URI",
			&sip.SipUri{
				FIsEncrypted: true,
				FUser:        sip.String{"alice"},
				FHost:        "wonderland.com",
				FUriParams:   noParams,
				FHeaders:     noParams,
			},
			"sips:alice@wonderland.com",
		},
		{
			"SIP URI with one parameter",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: sip.NewParams().Add("food", sip.String{"cake"}),
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com;food=cake",
		},
		{
			"SIP URI with one no-value parameter",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: sip.NewParams().Add("something", nil),
				FHeaders:   noParams,
			},
			"sip:alice@wonderland.com;something",
		},
		{
			"SIP URI with three parameters",
			&sip.SipUri{
				FUser: sip.String{"alice"},
				FHost: "wonderland.com",
				FUriParams: sip.NewParams().Add("food", sip.String{"cake"}).
					Add("something", nil).
					Add("drink", sip.String{"tea"}),
				FHeaders: noParams,
			},
			"sip:alice@wonderland.com;food=cake;something;drink=tea",
		},
		{
			"SIP URI with one header",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders:   sip.NewParams().Add("CakeLocation", sip.String{"Tea Party"}),
			},
			"sip:alice@wonderland.com?CakeLocation=\"Tea Party\"",
		},
		{
			"SIP URI with three headers",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: noParams,
				FHeaders: sip.NewParams().Add("CakeLocation", sip.String{"Tea Party"}).
					Add("Identity", sip.String{"Mad Hatter"}).
					Add("OtherHeader", sip.String{"Some value"})},
			"sip:alice@wonderland.com?CakeLocation=\"Tea Party\"&Identity=\"Mad Hatter\"&OtherHeader=\"Some value\"",
		},
		{
			"SIP URI with parameter and header",
			&sip.SipUri{
				FUser:      sip.String{"alice"},
				FHost:      "wonderland.com",
				FUriParams: sip.NewParams().Add("food", sip.String{"cake"}),
				FHeaders:   sip.NewParams().Add("CakeLocation", sip.String{"Tea Party"}),
			},
			"sip:alice@wonderland.com;food=cake?CakeLocation=\"Tea Party\"",
		},
		{
			"Wildcard URI",
			&sip.WildcardUri{},
			"*",
		},
	}, t)
}

func TestHeaders_String(t *testing.T) {
	doTests([]stringTest{
		// To Headers.
		{
			"Basic To Header",
			&sip.ToHeader{
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
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
			&sip.ToHeader{
				DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
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
			&sip.ToHeader{
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: sip.NewParams().Add("food", sip.String{"cake"}),
			},
			"To: <sip:alice@wonderland.com>;food=cake",
		},

		// From Headers.
		{
			"Basic From Header",
			&sip.FromHeader{
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
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
			&sip.FromHeader{
				DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
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
			&sip.FromHeader{
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: sip.NewParams().Add("food", sip.String{"cake"}),
			},
			"From: <sip:alice@wonderland.com>;food=cake",
		},

		// Contact Headers
		{
			"Basic Contact Header",
			&sip.ContactHeader{
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
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
			&sip.ContactHeader{
				DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
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
			&sip.ContactHeader{
				Address: &sip.SipUri{
					FUser:      sip.String{"alice"},
					FHost:      "wonderland.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				Params: sip.NewParams().Add("food", sip.String{"cake"}),
			},
			"Contact: <sip:alice@wonderland.com>;food=cake",
		},
		{
			"Contact Header with Wildcard URI",
			&sip.ContactHeader{
				Address: &sip.WildcardUri{},
				Params:  noParams,
			},
			"Contact: *",
		},
		{
			"Contact Header with display name and Wildcard URI",
			&sip.ContactHeader{
				DisplayName: sip.String{"Mad Hatter"},
				Address:     &sip.WildcardUri{},
				Params:      noParams,
			},
			"Contact: \"Mad Hatter\" *",
		},
		{
			"Contact Header with Wildcard URI and parameters",
			&sip.ContactHeader{
				Address: &sip.WildcardUri{},
				Params:  sip.NewParams().Add("food", sip.String{"cake"}),
			},
			"Contact: *;food=cake",
		},

		// Via Headers.
		{
			"Basic Via Header",
			sip.ViaHeader{
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Params:          sip.NewParams(),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com",
		},
		{
			"Via Header with port",
			sip.ViaHeader{
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Port:            &port6060,
					Params:          sip.NewParams(),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com:6060",
		},
		{
			"Via Header with params",
			sip.ViaHeader{
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Port:            &port6060,
					Params: sip.NewParams().Add("food", sip.String{"cake"}).
						Add("delicious", nil),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com:6060;food=cake;delicious",
		},
		{
			"Via Header with 3 simple hops",
			sip.ViaHeader{
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Params:          sip.NewParams(),
				},
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "TCP",
					Host:            "looking-glass.net",
					Params:          sip.NewParams(),
				},
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "oxford.co.uk",
					Params:          sip.NewParams(),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com, SIP/2.0/TCP looking-glass.net, SIP/2.0/UDP oxford.co.uk",
		},
		{
			"Via Header with 3 complex hops",
			sip.ViaHeader{
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "wonderland.com",
					Port:            &port5060,
					Params:          sip.NewParams(),
				},
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "TCP",
					Host:            "looking-glass.net",
					Port:            &port6060,
					Params:          sip.NewParams().Add("food", sip.String{"cake"}),
				},
				&sip.ViaHop{
					ProtocolName:    "SIP",
					ProtocolVersion: "2.0",
					Transport:       "UDP",
					Host:            "oxford.co.uk",
					Params:          sip.NewParams().Add("delicious", nil),
				},
			},
			"Via: SIP/2.0/UDP wonderland.com:5060, SIP/2.0/TCP looking-glass.net:6060;food=cake, SIP/2.0/UDP oxford.co.uk;delicious",
		},

		// Require Headers.
		{
			"Require Header (empty)",
			&sip.RequireHeader{[]string{}},
			"Require: ",
		},
		{
			"Require Header (one option)",
			&sip.RequireHeader{[]string{"NewFeature1"}},
			"Require: NewFeature1",
		},
		{
			"Require Header (three options)",
			&sip.RequireHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Require: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Supported Headers.
		{
			"Supported Header (empty)",
			&sip.SupportedHeader{[]string{}},
			"Supported: ",
		},
		{
			"Supported Header (one option)",
			&sip.SupportedHeader{[]string{"NewFeature1"}},
			"Supported: NewFeature1",
		},
		{
			"Supported Header (three options)",
			&sip.SupportedHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Supported: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Proxy-Require Headers.
		{
			"Proxy-Require Header (empty)",
			&sip.ProxyRequireHeader{[]string{}},
			"Proxy-Require: ",
		},
		{
			"Proxy-Require Header (one option)",
			&sip.ProxyRequireHeader{[]string{"NewFeature1"}},
			"Proxy-Require: NewFeature1",
		},
		{
			"Proxy-Require Header (three options)",
			&sip.ProxyRequireHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Proxy-Require: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Unsupported Headers.
		{
			"Unsupported Header (empty)",
			&sip.UnsupportedHeader{[]string{}},
			"Unsupported: ",
		},
		{
			"Unsupported Header (one option)",
			&sip.UnsupportedHeader{[]string{"NewFeature1"}},
			"Unsupported: NewFeature1",
		},
		{
			"Unsupported Header (three options)",
			&sip.UnsupportedHeader{[]string{"NewFeature1", "FunkyExtension", "UnnecessaryAddition"}},
			"Unsupported: NewFeature1, FunkyExtension, UnnecessaryAddition",
		},

		// Various simple headers.
		{
			"Call-ID Header",
			sip.CallID("Call-ID-1"),
			"Call-ID: Call-ID-1",
		},
		{
			"CSeq Header",
			&sip.CSeq{1234, "INVITE"},
			"CSeq: 1234 INVITE",
		},
		{
			"Max Forwards Header",
			sip.MaxForwards(70),
			"Max-Forwards: 70",
		},
		{
			"Content Length Header",
			sip.ContentLength(70),
			"Content-Length: 70",
		},
	}, t)
}
