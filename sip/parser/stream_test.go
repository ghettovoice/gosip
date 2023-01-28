// Forked from github.com/StefanKopieczek/gossip by @StefanKopieczek
package parser_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/parser"
	"github.com/ghettovoice/gosip/testutils"
)

// Level of logs output during testing.
var testsRun int
var testsPassed int

type input interface {
	String() string
	evaluate() result
}
type result interface {
	// Slight unpleasantness: equals is asymmetrical and should be called on an
	// expected value with the true result as the target.
	// This is necessary in order for the reason strings to come out right.
	equals(other result) (equal bool, reason string)
}
type test struct {
	args     input
	expected result
}

func doTests(tests []test, t *testing.T) {
	for _, test := range tests {
		t.Logf("Running test with input: %v", test.args.String())
		testsRun++
		output := test.args.evaluate()
		pass, reason := test.expected.equals(output)
		if !pass {
			t.Errorf("Failure on input \"%s\" : %s", test.args.String(), reason)
		} else {
			testsPassed++
		}
	}
}

// Pass and fail placeholders
var fail = fmt.Errorf("a bad thing happened")
var pass error = nil

// Need to define immutable variables in order to pointer to them.
var port5060 sip.Port = 5060
var port5 sip.Port = 5
var port9 sip.Port = 9
var noParams = sip.NewParams()

func TestAAAASetup(t *testing.T) {
}

func TestParams(t *testing.T) {
	doTests([]test{
		// TEST: ParseParams
		{
			&paramInput{";foo=bar", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 8},
		},
		{
			&paramInput{";foo=", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{""}), 5},
		},
		{
			&paramInput{";foo", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", nil), 4},
		},
		{
			&paramInput{";foo=bar!hello", ';', ';', '!', false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 8},
		},
		{
			&paramInput{";foo!hello", ';', ';', '!', false, true},
			&paramResult{pass, sip.NewParams().Add("foo", nil), 4},
		},
		{
			&paramInput{";foo=!hello", ';', ';', '!', false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{""}), 5},
		},
		{
			&paramInput{";foo=bar!h;l!o", ';', ';', '!', false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 8},
		},
		{
			&paramInput{";foo!h;l!o", ';', ';', '!', false, true},
			&paramResult{pass, sip.NewParams().Add("foo", nil), 4},
		},
		{
			&paramInput{"foo!h;l!o", ';', ';', '!', false, true},
			&paramResult{fail, sip.NewParams(), 0},
		},
		{
			&paramInput{"foo;h;l!o", ';', ';', '!', false, true},
			&paramResult{fail, sip.NewParams(), 0},
		},
		{
			&paramInput{";foo=bar;baz=boop", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}), 17},
		},
		{
			&paramInput{";foo=bar;baz=boop!lol", ';', ';', '!', false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}), 17},
		},
		{
			&paramInput{";foo=bar;baz", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", nil), 12},
		},
		{
			&paramInput{";foo;baz=boop", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", nil).Add("baz", sip.String{"boop"}), 13},
		},
		{
			&paramInput{";foo=bar;baz=boop;a=b", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}).Add("a", sip.String{"b"}), 21},
		},
		{
			&paramInput{";foo;baz=boop;a=b", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", nil).Add("baz", sip.String{"boop"}).Add("a", sip.String{"b"}), 17},
		},
		{
			&paramInput{";foo=bar;baz;a=b", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", nil).Add("a", sip.String{"b"}), 16},
		},
		{
			&paramInput{";foo=bar;baz=boop;a", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}).Add("a", nil), 19},
		},
		{
			&paramInput{";foo=bar;baz=;a", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{""}).Add("a", nil), 15},
		},
		{
			&paramInput{";foo=;baz=bob;a", ';', ';', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{""}).Add("baz", sip.String{"bob"}).Add("a", nil), 15},
		},
		{
			&paramInput{"foo=bar", ';', ';', 0, false, true},
			&paramResult{fail, sip.NewParams(), 0}},
		{
			&paramInput{"$foo=bar", '$', ',', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 8},
		},
		{
			&paramInput{"$foo", '$', ',', 0, false, true},
			&paramResult{pass, sip.NewParams().Add("foo", nil), 4},
		},
		{
			&paramInput{"$foo=bar!hello", '$', ',', '!', false, true},
			&paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 8},
		},
		{&paramInput{"$foo#hello", '$', ',', '#', false, true}, &paramResult{pass, sip.NewParams().Add("foo", nil), 4}},
		{&paramInput{"$foo=bar!h;,!o", '$', ',', '!', false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 8}},
		{&paramInput{"$foo!h;l!,", '$', ',', '!', false, true}, &paramResult{pass, sip.NewParams().Add("foo", nil), 4}},
		{&paramInput{"foo!h;l!o", '$', ',', '!', false, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{"foo,h,l!o", '$', ',', '!', false, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{"$foo=bar,baz=boop", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}), 17}},
		{&paramInput{"$foo=bar;baz", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar;baz"}), 12}},
		{&paramInput{"$foo=bar,baz=boop!lol", '$', ',', '!', false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}), 17}},
		{&paramInput{"$foo=bar,baz", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", nil), 12}},
		{&paramInput{"$foo=,baz", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{""}).Add("baz", nil), 9}},
		{&paramInput{"$foo,baz=boop", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", nil).Add("baz", sip.String{"boop"}), 13}},
		{&paramInput{"$foo=bar,baz=boop,a=b", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}).Add("a", sip.String{"b"}), 21}},
		{&paramInput{"$foo,baz=boop,a=b", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", nil).Add("baz", sip.String{"boop"}).Add("a", sip.String{"b"}), 17}},
		{&paramInput{"$foo=bar,baz,a=b", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", nil).Add("a", sip.String{"b"}), 16}},
		{&paramInput{"$foo=bar,baz=boop,a", '$', ',', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}).Add("a", nil), 19}},
		{&paramInput{";foo", ';', ';', 0, false, false}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo=", ';', ';', 0, false, false}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{""}), 5}},
		{&paramInput{";foo=bar;baz=boop", ';', ';', 0, false, false}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{"boop"}), 17}},
		{&paramInput{";foo=bar;baz", ';', ';', 0, false, false}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo;bar=baz", ';', ';', 0, false, false}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo=;baz=boop", ';', ';', 0, false, false}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{""}).Add("baz", sip.String{"boop"}), 14}},
		{&paramInput{";foo=bar;baz=", ';', ';', 0, false, false}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{""}), 13}},
		{&paramInput{"$foo=bar,baz=,a=b", '$', ',', 0, false, true}, &paramResult{pass,
			sip.NewParams().Add("foo", sip.String{"bar"}).Add("baz", sip.String{""}).Add("a", sip.String{"b"}), 17}},
		{&paramInput{"$foo=bar,baz,a=b", '$', ',', 0, false, false}, &paramResult{fail, sip.NewParams(), 17}},
		{&paramInput{";foo=\"bar\"", ';', ';', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"\"bar\""}), 10}},
		{&paramInput{";foo=\"bar", ';', ';', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"\"bar"}), 9}},
		{&paramInput{";foo=bar\"", ';', ';', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar\""}), 9}},
		{&paramInput{";\"foo\"=bar", ';', ';', 0, false, true}, &paramResult{pass, sip.NewParams().Add("\"foo\"", sip.String{"bar"}), 10}},
		{&paramInput{";foo\"=bar", ';', ';', 0, false, true}, &paramResult{pass, sip.NewParams().Add("foo\"", sip.String{"bar"}), 9}},
		{&paramInput{";\"foo=bar", ';', ';', 0, false, true}, &paramResult{pass, sip.NewParams().Add("\"foo", sip.String{"bar"}), 9}},
		{&paramInput{";foo=\"bar\"", ';', ';', 0, true, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 10}},
		{&paramInput{";foo=\"ba\"r", ';', ';', 0, true, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo=ba\"r", ';', ';', 0, true, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo=bar\"", ';', ';', 0, true, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo=\"bar", ';', ';', 0, true, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";\"foo\"=bar", ';', ';', 0, true, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";\"foo=bar", ';', ';', 0, true, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo\"=bar", ';', ';', 0, true, true}, &paramResult{fail, sip.NewParams(), 0}},
		{&paramInput{";foo=\"bar;baz\"", ';', ';', 0, true, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar;baz"}), 14}},
		{&paramInput{";foo=\"bar;baz\";a=b", ';', ';', 0, true, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar;baz"}).Add("a", sip.String{"b"}), 18}},
		{&paramInput{";foo=\"bar;baz\";a", ';', ';', 0, true, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar;baz"}).Add("a", nil), 16}},
		{&paramInput{";foo=bar", ';', ';', 0, true, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{"bar"}), 8}},
		{&paramInput{";foo=", ';', ';', 0, true, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{""}), 5}},
		{&paramInput{";foo=\"\"", ';', ';', 0, true, true}, &paramResult{pass, sip.NewParams().Add("foo", sip.String{""}), 7}},
	}, t)
}

func TestSipUris(t *testing.T) {
	doTests([]test{
		{sipUriInput("sip:bob@example.com"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:bob@192.168.0.1"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "192.168.0.1", FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:bob:Hunter2@example.com"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: sip.String{"Hunter2"}, FHost: "example.com", FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sips:bob:Hunter2@example.com"), &sipUriResult{pass, sip.SipUri{FIsEncrypted: true, FUser: sip.String{"bob"}, FPassword: sip.String{"Hunter2"},
			FHost: "example.com", FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:%D0%B8%D0%B2%D0%B0%D0%BD:qwerty@%D0%BC%D0%B8%D1%80.%D1%80%D1%84"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"иван"}, FPassword: sip.String{"qwerty"}, FHost: "мир.рф", FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sips:bob@example.com"), &sipUriResult{pass, sip.SipUri{FIsEncrypted: true, FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:example.com"), &sipUriResult{pass, sip.SipUri{FUser: nil, FPassword: nil, FHost: "example.com", FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("example.com"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("bob@example.com"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:5060"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5060, FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:bob@88.88.88.88:5060"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "88.88.88.88", FPort: &port5060, FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:bob:Hunter2@example.com:5060"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: sip.String{"Hunter2"},
			FHost: "example.com", FPort: &port5060, FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com:5"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5, FUriParams: noParams, FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com;foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com",
			FUriParams: sip.NewParams().Add("foo", sip.String{"bar"}), FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com:5060;foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5060,
			FUriParams: sip.NewParams().Add("foo", sip.String{"bar"}), FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com:5;foo"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", nil), FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com:5;foo;baz=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", nil).Add("baz", sip.String{"bar"}), FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com:5;baz=bar;foo"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", nil).Add("baz", sip.String{"bar"}), FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com:5;foo;baz=bar;a=b"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", nil).Add("baz", sip.String{"bar"}).Add("a", sip.String{"b"}), FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com:5;baz=bar;foo;a=b"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", nil).Add("baz", sip.String{"bar"}).Add("a", sip.String{"b"}), FHeaders: noParams}}},
		{sipUriInput("sip:bob@example.com?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com",
			FUriParams: noParams, FHeaders: sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sip:bob@example.com?foo="), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com",
			FUriParams: noParams, FHeaders: sip.NewParams().Add("foo", sip.String{""})}}},
		{sipUriInput("sip:bob@example.com:5060?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5060,
			FUriParams: noParams, FHeaders: sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sip:bob@example.com:5?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: noParams, FHeaders: sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sips:bob@example.com:5?baz=bar&foo=&a=b"), &sipUriResult{pass, sip.SipUri{FIsEncrypted: true, FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: noParams, FHeaders: sip.NewParams().Add("baz", sip.String{"bar"}).Add("a", sip.String{"b"}).Add("foo", sip.String{""})}}},
		{sipUriInput("sip:bob@example.com:5?baz=bar&foo&a=b"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:5?foo"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:50?foo"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:50?foo=bar&baz"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com;foo?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com",
			FUriParams: sip.NewParams().Add("foo", nil),
			FHeaders:   sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sip:bob@example.com:5060;foo?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5060,
			FUriParams: sip.NewParams().Add("foo", nil),
			FHeaders:   sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sip:bob@example.com:5;foo?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", nil),
			FHeaders:   sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sips:bob@example.com:5;foo?baz=bar&a=b&foo="), &sipUriResult{pass, sip.SipUri{FIsEncrypted: true, FUser: sip.String{"bob"},
			FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", nil),
			FHeaders:   sip.NewParams().Add("baz", sip.String{"bar"}).Add("a", sip.String{"b"}).Add("foo", sip.String{""})}}},
		{sipUriInput("sip:bob@example.com:5;foo?baz=bar&foo&a=b"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:5;foo?foo"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:50;foo?foo"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:50;foo?foo=bar&baz"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com;foo=baz?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com",
			FUriParams: sip.NewParams().Add("foo", sip.String{"baz"}),
			FHeaders:   sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sip:bob@example.com:5060;foo=baz?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5060,
			FUriParams: sip.NewParams().Add("foo", sip.String{"baz"}),
			FHeaders:   sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sip:bob@example.com:5;foo=baz?foo=bar"), &sipUriResult{pass, sip.SipUri{FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", sip.String{"baz"}),
			FHeaders:   sip.NewParams().Add("foo", sip.String{"bar"})}}},
		{sipUriInput("sips:bob@example.com:5;foo=baz?baz=bar&a=b"), &sipUriResult{pass, sip.SipUri{FIsEncrypted: true, FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", sip.String{"baz"}),
			FHeaders:   sip.NewParams().Add("baz", sip.String{"bar"}).Add("a", sip.String{"b"})}}},
		{sipUriInput("sips:bob@example.com:5;foo=\"%D0%B8%D0%B2%D0%B0%D0%BD%26%D0%BC%D0%B0%D1%80%D1%8C%D1%8F 123\""), &sipUriResult{pass, sip.SipUri{FIsEncrypted: true, FUser: sip.String{"bob"}, FPassword: nil, FHost: "example.com", FPort: &port5,
			FUriParams: sip.NewParams().Add("foo", sip.String{"иван&марья 123"}),
			FHeaders:   noParams}}},
		{sipUriInput("sip:bob@example.com:5;foo=baz?baz=bar&foo&a=b"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:5;foo=baz?foo"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:50;foo=baz?foo"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip:bob@example.com:50;foo=baz?foo=bar&baz"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sip"), &sipUriResult{fail, sip.SipUri{}}},
		{sipUriInput("sips"), &sipUriResult{fail, sip.SipUri{}}},
	}, t)
}

func TestHostPort(t *testing.T) {
	doTests([]test{
		{hostPortInput("example.com"), &hostPortResult{pass, "example.com", nil}},
		{hostPortInput("192.168.0.1"), &hostPortResult{pass, "192.168.0.1", nil}},
		{hostPortInput("abc123"), &hostPortResult{pass, "abc123", nil}},
		{hostPortInput("example.com:5060"), &hostPortResult{pass, "example.com", &port5060}},
		{hostPortInput("example.com:9"), &hostPortResult{pass, "example.com", &port9}},
		{hostPortInput("192.168.0.1:5060"), &hostPortResult{pass, "192.168.0.1", &port5060}},
		{hostPortInput("192.168.0.1:9"), &hostPortResult{pass, "192.168.0.1", &port9}},
		{hostPortInput("abc123:5060"), &hostPortResult{pass, "abc123", &port5060}},
		{hostPortInput("abc123:9"), &hostPortResult{pass, "abc123", &port9}},
		// TODO IPV6, c.f. IPv6reference in RFC 3261 s25
	}, t)
}

/*
	func TestHeaderBlocks(t *testing.T) {
		doTests([]test{
			test{headerBlockInput([]string{"All on one line."}), &headerBlockResult{"All on one line.", 1}},
			test{headerBlockInput([]string{"Line one", "Line two."}), &headerBlockResult{"Line one", 1}},
			test{headerBlockInput([]string{"Line one", " then an indent"}), &headerBlockResult{"Line one then an indent", 2}},
			test{headerBlockInput([]string{"Line one", " then an indent", "then line two"}), &headerBlockResult{"Line one then an indent", 2}},
			test{headerBlockInput([]string{"Line one", "Line two", " then an indent"}), &headerBlockResult{"Line one", 1}},
			test{headerBlockInput([]string{"Line one", "\twith tab indent"}), &headerBlockResult{"Line one with tab indent", 2}},
			test{headerBlockInput([]string{"Line one", "      with a big indent"}), &headerBlockResult{"Line one with a big indent", 2}},
			test{headerBlockInput([]string{"Line one", " \twith space then tab"}), &headerBlockResult{"Line one with space then tab", 2}},
			test{headerBlockInput([]string{"Line one", "\t    with tab then spaces"}), &headerBlockResult{"Line one with tab then spaces", 2}},
			test{headerBlockInput([]string{""}), &headerBlockResult{"", 0}},
			test{headerBlockInput([]string{" "}), &headerBlockResult{" ", 1}},
			test{headerBlockInput([]string{}), &headerBlockResult{"", 0}},
			test{headerBlockInput([]string{" foo"}), &headerBlockResult{" foo", 1}},
		}, t)
	}
*/
func TestToHeaders(t *testing.T) {
	fooEqBar := sip.NewParams().Add("foo", sip.String{"bar"})
	fooSingleton := sip.NewParams().Add("foo", nil)
	doTests([]test{
		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To : \"Alice Liddell\" <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To  : \"Alice Liddell\" <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To\t: \"Alice Liddell\" <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To:\n  \"Alice Liddell\" \n\t<sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("t: Alice <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: Alice sip:alice@wonderland.com"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To:"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: "), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To:\t"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: foo"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: foo bar"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: \"Alice\" sip:alice@wonderland.com"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: \"<Alice>\" sip:alice@wonderland.com"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: \"sip:alice@wonderland.com\""), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: \"sip:alice@wonderland.com\"  <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"sip:alice@wonderland.com"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("T: \"<sip:alice@wonderland.com>\"  <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"<sip:alice@wonderland.com>"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: \"<sip: alice@wonderland.com>\"  <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"<sip: alice@wonderland.com>"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com>;foo=bar"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  fooEqBar}}},

		{
			toHeaderInput("To: sip:alice@wonderland.com;foo=bar"),
			&toHeaderResult{
				pass,
				&sip.ToHeader{
					DisplayName: nil,
					Address: &sip.SipUri{
						false,
						sip.String{"alice"},
						nil,
						"wonderland.com",
						nil,
						noParams,
						noParams,
					},
					Params: fooEqBar,
				},
			},
		},

		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com;foo=bar>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooEqBar, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com?foo=bar>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, fooEqBar},
				Params:  noParams}}},

		{toHeaderInput("to: \"Alice Liddell\" <sip:alice@wonderland.com>;foo"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  fooSingleton}}},

		{toHeaderInput("TO: \"Alice Liddell\" <sip:alice@wonderland.com;foo>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com?foo>"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com;foo?foo=bar>;foo=bar"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, fooEqBar},
				Params:  fooEqBar}}},

		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com;foo?foo=bar>;foo"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, fooEqBar},
				Params:  fooSingleton}}},

		{toHeaderInput("To: \"Alice Liddell\" <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: sip:alice@wonderland.com, sip:hatter@wonderland.com"), &toHeaderResult{fail,
			&sip.ToHeader{}}},

		{toHeaderInput("To: *"), &toHeaderResult{fail, &sip.ToHeader{}}},

		{toHeaderInput("To: <*>"), &toHeaderResult{fail, &sip.ToHeader{}}},

		{toHeaderInput("To: \"Alice Liddell\"<sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: Alice Liddell <sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: Alice Liddell<sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{toHeaderInput("To: Alice<sip:alice@wonderland.com>"), &toHeaderResult{pass,
			&sip.ToHeader{DisplayName: sip.String{"Alice"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},
	}, t)
}

func TestFromHeaders(t *testing.T) {
	// These are identical to the To: header tests, but there's no clean way to share them :(
	fooEqBar := sip.NewParams().Add("foo", sip.String{"bar"})
	fooSingleton := sip.NewParams().Add("foo", nil)
	doTests([]test{
		{fromHeaderInput("From: \"Alice Liddell\" <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From : \"Alice Liddell\" <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From   : \"Alice Liddell\" <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From\t: \"Alice Liddell\" <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From:\n  \"Alice Liddell\" \n\t<sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("f: Alice <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From: Alice sip:alice@wonderland.com"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From:"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: "), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From:\t"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: foo"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: foo bar"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: \"Alice\" sip:alice@wonderland.com"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: \"<Alice>\" sip:alice@wonderland.com"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: \"sip:alice@wonderland.com\""), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: \"sip:alice@wonderland.com\"  <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"sip:alice@wonderland.com"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From: \"<sip:alice@wonderland.com>\"  <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"<sip:alice@wonderland.com>"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From: \"<sip: alice@wonderland.com>\"  <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"<sip: alice@wonderland.com>"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("FrOm: \"Alice Liddell\" <sip:alice@wonderland.com>;foo=bar"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  fooEqBar}}},

		{fromHeaderInput("FrOm: sip:alice@wonderland.com;foo=bar"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: nil,
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  fooEqBar}}},

		{fromHeaderInput("from: \"Alice Liddell\" <sip:alice@wonderland.com;foo=bar>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooEqBar, noParams},
				Params:  noParams}}},

		{fromHeaderInput("F: \"Alice Liddell\" <sip:alice@wonderland.com?foo=bar>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, fooEqBar},
				Params:  noParams}}},

		{fromHeaderInput("From: \"Alice Liddell\" <sip:alice@wonderland.com>;foo"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  fooSingleton}}},

		{fromHeaderInput("From: \"Alice Liddell\" <sip:alice@wonderland.com;foo>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From: \"Alice Liddell\" <sip:alice@wonderland.com?foo>"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: \"Alice Liddell\" <sip:alice@wonderland.com;foo?foo=bar>;foo=bar"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, fooEqBar},
				Params:  fooEqBar}}},

		{fromHeaderInput("From: \"Alice Liddell\" <sip:alice@wonderland.com;foo?foo=bar>;foo"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, fooEqBar},
				Params:  fooSingleton}}},

		{fromHeaderInput("From: \"Alice Liddell\" <sip:alice@wonderland.com>"), &fromHeaderResult{pass,
			&sip.FromHeader{DisplayName: sip.String{"Alice Liddell"},
				Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
				Params:  noParams}}},

		{fromHeaderInput("From: sip:alice@wonderland.com, sip:hatter@wonderland.com"), &fromHeaderResult{fail,
			&sip.FromHeader{}}},

		{fromHeaderInput("From: *"), &fromHeaderResult{fail, &sip.FromHeader{}}},

		{fromHeaderInput("From: <*>"), &fromHeaderResult{fail, &sip.FromHeader{}}},
	}, t)
}

func TestContactHeaders(t *testing.T) {
	fooEqBar := sip.NewParams().Add("foo", sip.String{"bar"})
	fooSingleton := sip.NewParams().Add("foo", nil)
	doTests([]test{
		{contactHeaderInput("Contact: \"Alice Liddell\" <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact : \"Alice Liddell\" <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},
		{contactHeaderInput("Contact  : \"Alice Liddell\" <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},
		{contactHeaderInput("Contact\t: \"Alice Liddell\" <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},
		{contactHeaderInput("Contact:\n  \"Alice Liddell\" \n\t<sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("m: Alice <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: *"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: nil, Address: &sip.WildcardUri{}, Params: noParams}}}},

		{contactHeaderInput("Contact: \t  *"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: nil, Address: &sip.WildcardUri{}, Params: noParams}}}},

		{contactHeaderInput("M: *"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: nil, Address: &sip.WildcardUri{}, Params: noParams}}}},

		{contactHeaderInput("Contact: *"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: nil, Address: &sip.WildcardUri{}, Params: noParams}}}},

		{contactHeaderInput("Contact: \"John\" *"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{}}},

		{contactHeaderInput("Contact: \"John\" <*>"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{}}},

		{contactHeaderInput("Contact: *;foo=bar"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{}}},

		{contactHeaderInput("Contact: Alice sip:alice@wonderland.com"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact:"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: "), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact:\t"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: foo"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: foo bar"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: \"Alice\" sip:alice@wonderland.com"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: \"<Alice>\" sip:alice@wonderland.com"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: \"sip:alice@wonderland.com\""), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: \"sip:alice@wonderland.com\"  <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"sip:alice@wonderland.com"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"<sip:alice@wonderland.com>\"  <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"<sip:alice@wonderland.com>"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"<sip: alice@wonderland.com>\"  <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"<sip: alice@wonderland.com>"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("cOntACt: \"Alice Liddell\" <sip:alice@wonderland.com>;foo=bar"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  fooEqBar}}}},

		{contactHeaderInput("contact: \"Alice Liddell\" <sip:alice@wonderland.com;foo=bar>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooEqBar, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("M: \"Alice Liddell\" <sip:alice@wonderland.com?foo=bar>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, fooEqBar},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sip:alice@wonderland.com>;foo"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  fooSingleton}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sip:alice@wonderland.com;foo>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sip:alice@wonderland.com?foo>"), &contactHeaderResult{
			fail,
			[]*sip.ContactHeader{
				{}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sip:alice@wonderland.com;foo?foo=bar>;foo=bar"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, fooEqBar},
					Params:  fooEqBar}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sip:alice@wonderland.com;foo?foo=bar>;foo"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, fooSingleton, fooEqBar},
					Params:  fooSingleton}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sip:alice@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: sip:alice@wonderland.com, sip:hatter@wonderland.com"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: nil, Address: &sip.SipUri{false, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams}, Params: noParams},
				{DisplayName: nil, Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams}, Params: noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sips:alice@wonderland.com>, \"Madison Hatter\" <sip:hatter@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{true, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: sip.String{"Madison Hatter"},
					Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: <sips:alice@wonderland.com>, \"Madison Hatter\" <sip:hatter@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: nil,
					Address: &sip.SipUri{true, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: sip.String{"Madison Hatter"},
					Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sips:alice@wonderland.com>, <sip:hatter@wonderland.com>"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{true, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: nil,
					Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sips:alice@wonderland.com>, \"Madison Hatter\" <sip:hatter@wonderland.com>" +
			",    sip:kat@cheshire.gov.uk"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{true, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: sip.String{"Madison Hatter"},
					Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: nil,
					Address: &sip.SipUri{false, sip.String{"kat"}, nil, "cheshire.gov.uk", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sips:alice@wonderland.com>;foo=bar, \"Madison Hatter\" <sip:hatter@wonderland.com>" +
			",    sip:kat@cheshire.gov.uk"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{true, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  fooEqBar},
				{DisplayName: sip.String{"Madison Hatter"},
					Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: nil,
					Address: &sip.SipUri{false, sip.String{"kat"}, nil, "cheshire.gov.uk", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sips:alice@wonderland.com>, \"Madison Hatter\" <sip:hatter@wonderland.com>;foo=bar" +
			",    sip:kat@cheshire.gov.uk"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{true, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: sip.String{"Madison Hatter"},
					Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  fooEqBar},
				{DisplayName: nil,
					Address: &sip.SipUri{false, sip.String{"kat"}, nil, "cheshire.gov.uk", nil, noParams, noParams},
					Params:  noParams}}}},

		{contactHeaderInput("Contact: \"Alice Liddell\" <sips:alice@wonderland.com>, \"Madison Hatter\" <sip:hatter@wonderland.com>" +
			",    sip:kat@cheshire.gov.uk;foo=bar"), &contactHeaderResult{
			pass,
			[]*sip.ContactHeader{
				{DisplayName: sip.String{"Alice Liddell"},
					Address: &sip.SipUri{true, sip.String{"alice"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: sip.String{"Madison Hatter"},
					Address: &sip.SipUri{false, sip.String{"hatter"}, nil, "wonderland.com", nil, noParams, noParams},
					Params:  noParams},
				{DisplayName: nil,
					Address: &sip.SipUri{false, sip.String{"kat"}, nil, "cheshire.gov.uk", nil, noParams, noParams},
					Params:  fooEqBar}}}},
	}, t)
}

func TestSplitByWS(t *testing.T) {
	doTests([]test{
		{splitByWSInput("Hello world"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello\tworld"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello    world"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello  world"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello\t world"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello\t world"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello\t \tworld"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello\t\tworld"), splitByWSResult([]string{"Hello", "world"})},
		{splitByWSInput("Hello\twonderful\tworld"), splitByWSResult([]string{"Hello", "wonderful", "world"})},
		{splitByWSInput("Hello   wonderful\tworld"), splitByWSResult([]string{"Hello", "wonderful", "world"})},
		{splitByWSInput("Hello   wonderful  world"), splitByWSResult([]string{"Hello", "wonderful", "world"})},
	}, t)
}

func TestCSeqs(t *testing.T) {
	doTests([]test{
		{cSeqInput("CSeq: 1 INVITE"), &cSeqResult{pass, &sip.CSeq{1, "INVITE"}}},
		{cSeqInput("CSeq : 2 INVITE"), &cSeqResult{pass, &sip.CSeq{2, "INVITE"}}},
		{cSeqInput("CSeq  : 3 INVITE"), &cSeqResult{pass, &sip.CSeq{3, "INVITE"}}},
		{cSeqInput("CSeq\t: 4 INVITE"), &cSeqResult{pass, &sip.CSeq{4, "INVITE"}}},
		{cSeqInput("CSeq:\t5\t\tINVITE"), &cSeqResult{pass, &sip.CSeq{5, "INVITE"}}},
		{cSeqInput("CSeq:\t6 \tINVITE"), &cSeqResult{pass, &sip.CSeq{6, "INVITE"}}},
		{cSeqInput("CSeq:    7      INVITE"), &cSeqResult{pass, &sip.CSeq{7, "INVITE"}}},
		{cSeqInput("CSeq: 8  INVITE"), &cSeqResult{pass, &sip.CSeq{8, "INVITE"}}},
		{cSeqInput("CSeq: 0 register"), &cSeqResult{pass, &sip.CSeq{0, "register"}}},
		{cSeqInput("CSeq: 10 reGister"), &cSeqResult{pass, &sip.CSeq{10, "reGister"}}},
		{cSeqInput("CSeq: 17 FOOBAR"), &cSeqResult{pass, &sip.CSeq{17, "FOOBAR"}}},
		{cSeqInput("CSeq: 2147483647 NOTIFY"), &cSeqResult{pass, &sip.CSeq{2147483647, "NOTIFY"}}},
		{cSeqInput("CSeq: 2147483648 NOTIFY"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: -124 ACK"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: 1"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: ACK"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq:"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: FOO ACK"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: 9999999999999999999999999999999 SUBSCRIBE"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: 1 INVITE;foo=bar"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: 1 INVITE;foo"), &cSeqResult{fail, &sip.CSeq{}}},
		{cSeqInput("CSeq: 1 INVITE;foo=bar;baz"), &cSeqResult{fail, &sip.CSeq{}}},
	}, t)
}

func TestCallIds(t *testing.T) {
	doTests([]test{
		{callIdInput("Call-ID: fdlknfa32bse3yrbew23bf"), &callIdResult{pass, "fdlknfa32bse3yrbew23bf"}},
		{callIdInput("Call-ID : fdlknfa32bse3yrbew23bf"), &callIdResult{pass, "fdlknfa32bse3yrbew23bf"}},
		{callIdInput("Call-ID  : fdlknfa32bse3yrbew23bf"), &callIdResult{pass, "fdlknfa32bse3yrbew23bf"}},
		{callIdInput("Call-ID\t: fdlknfa32bse3yrbew23bf"), &callIdResult{pass, "fdlknfa32bse3yrbew23bf"}},
		{callIdInput("Call-ID: banana"), &callIdResult{pass, "banana"}},
		{callIdInput("Call-ID: banana"), &callIdResult{pass, "banana"}},
		{callIdInput("Call-ID: 1banana"), &callIdResult{pass, "1banana"}},
		{callIdInput("Call-ID:"), &callIdResult{fail, ""}},
		{callIdInput("Call-ID: banana spaghetti"), &callIdResult{fail, ""}},
		{callIdInput("Call-ID: banana\tspaghetti"), &callIdResult{fail, ""}},
		{callIdInput("Call-ID: banana;spaghetti"), &callIdResult{fail, ""}},
		{callIdInput("Call-ID: banana;spaghetti=tasty"), &callIdResult{fail, ""}},
	}, t)
}

func TestMaxForwards(t *testing.T) {
	doTests([]test{
		{maxForwardsInput("Max-Forwards: 9"), &maxForwardsResult{pass, sip.MaxForwards(9)}},
		{maxForwardsInput("Max-Forwards: 70"), &maxForwardsResult{pass, sip.MaxForwards(70)}},
		{maxForwardsInput("Max-Forwards: 71"), &maxForwardsResult{pass, sip.MaxForwards(71)}},
		{maxForwardsInput("Max-Forwards: 0"), &maxForwardsResult{pass, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards:      0"), &maxForwardsResult{pass, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards:\t0"), &maxForwardsResult{pass, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards: \t 0"), &maxForwardsResult{pass, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards:\n  0"), &maxForwardsResult{pass, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards: -1"), &maxForwardsResult{fail, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards:"), &maxForwardsResult{fail, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards: "), &maxForwardsResult{fail, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards:\t"), &maxForwardsResult{fail, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards:\n"), &maxForwardsResult{fail, sip.MaxForwards(0)}},
		{maxForwardsInput("Max-Forwards: \n"), &maxForwardsResult{fail, sip.MaxForwards(0)}},
	}, t)
}

func TestExpires(t *testing.T) {
	doTests([]test{
		{expiresInput("Expires: 9"), &expiresResult{pass, sip.Expires(9)}},
		{expiresInput("Expires: 600"), &expiresResult{pass, sip.Expires(600)}},
		{expiresInput("Expires: 3600"), &expiresResult{pass, sip.Expires(3600)}},
		{expiresInput("Expires: 0"), &expiresResult{pass, sip.Expires(0)}},
		{expiresInput("Expires:      0"), &expiresResult{pass, sip.Expires(0)}},
		{expiresInput("Expires:\t0"), &expiresResult{pass, sip.Expires(0)}},
		{expiresInput("Expires: \t 0"), &expiresResult{pass, sip.Expires(0)}},
		{expiresInput("Expires:\n  0"), &expiresResult{pass, sip.Expires(0)}},
		{expiresInput("Expires: -1"), &expiresResult{fail, sip.Expires(0)}},
		{expiresInput("Expires:"), &expiresResult{fail, sip.Expires(0)}},
		{expiresInput("Expires: "), &expiresResult{fail, sip.Expires(0)}},
		{expiresInput("Expires:\t"), &expiresResult{fail, sip.Expires(0)}},
		{expiresInput("Expires:\n"), &expiresResult{fail, sip.Expires(0)}},
		{expiresInput("Expires: \n"), &expiresResult{fail, sip.Expires(0)}},
	}, t)
}

func TestUserAgent(t *testing.T) {
	doTests([]test{
		{userAgentInput("User-Agent: GoSIP v1.2.3"), &userAgentResult{pass, "GoSIP v1.2.3"}},
		{userAgentInput("User-Agent:      GoSIP v1.2.3"), &userAgentResult{pass, "GoSIP v1.2.3"}},
		{userAgentInput("User-Agent:\tGoSIP v1.2.3"), &userAgentResult{pass, "GoSIP v1.2.3"}},
		{userAgentInput("User-Agent:\n  GoSIP v1.2.3"), &userAgentResult{pass, "GoSIP v1.2.3"}},
	}, t)
}

func TestAllow(t *testing.T) {
	doTests([]test{
		{allowInput("Allow: INVITE, ACK, BYE"), &allowResult{pass, sip.AllowHeader{sip.INVITE, sip.ACK, sip.BYE}}},
		{allowInput("Allow: INVITE , ACK ,\nBYE "), &allowResult{pass, sip.AllowHeader{sip.INVITE, sip.ACK, sip.BYE}}},
	}, t)
}

func TestContentLength(t *testing.T) {
	doTests([]test{
		{contentLengthInput("Content-Length: 9"), &contentLengthResult{pass, sip.ContentLength(9)}},
		{contentLengthInput("Content-Length: 20"), &contentLengthResult{pass, sip.ContentLength(20)}},
		{contentLengthInput("Content-Length: 113"), &contentLengthResult{pass, sip.ContentLength(113)}},
		{contentLengthInput("l: 113"), &contentLengthResult{pass, sip.ContentLength(113)}},
		{contentLengthInput("Content-Length: 0"), &contentLengthResult{pass, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length:      0"), &contentLengthResult{pass, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length:\t0"), &contentLengthResult{pass, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length: \t 0"), &contentLengthResult{pass, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length:\n  0"), &contentLengthResult{pass, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length: -1"), &contentLengthResult{fail, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length:"), &contentLengthResult{fail, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length: "), &contentLengthResult{fail, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length:\t"), &contentLengthResult{fail, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length:\n"), &contentLengthResult{fail, sip.ContentLength(0)}},
		{contentLengthInput("Content-Length: \n"), &contentLengthResult{fail, sip.ContentLength(0)}},
	}, t)
}

func TestViaHeaders(t *testing.T) {
	// branch=z9hG4bKnashds8
	fooEqBar := sip.NewParams().Add("foo", sip.String{Str: "bar"})
	fooEqSlashBar := sip.NewParams().Add("foo", sip.String{Str: "//bar"})
	singleFoo := sip.NewParams().Add("foo", nil)
	doTests([]test{
		{viaInput("Via: SIP/2.0/UDP pc33.atlanta.com"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "pc33.atlanta.com", nil, noParams}}}},
		{viaInput("Via: bAzz/fooo/BAAR pc33.atlanta.com"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"bAzz", "fooo", "BAAR", "pc33.atlanta.com", nil, noParams}}}},
		{viaInput("Via: SIP/2.0/UDP pc33.atlanta.com"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "pc33.atlanta.com", nil, noParams}}}},
		{viaInput("Via: SIP /\t2.0 / UDP pc33.atlanta.com"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "pc33.atlanta.com", nil, noParams}}}},
		{viaInput("Via: SIP /\n 2.0 / UDP pc33.atlanta.com"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "pc33.atlanta.com", nil, noParams}}}},
		{viaInput("Via:\tSIP/2.0/UDP pc33.atlanta.com"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "pc33.atlanta.com", nil, noParams}}}},
		{viaInput("Via:\n SIP/2.0/UDP pc33.atlanta.com"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "pc33.atlanta.com", nil, noParams}}}},
		{viaInput("Via: SIP/2.0/UDP box:5060"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "box", &port5060, noParams}}}},
		{viaInput("Via: SIP/2.0/UDP box;foo=bar"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "box", nil, fooEqBar}}}},
		{viaInput("Via: SIP/2.0/UDP box:5060;foo=bar"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "box", &port5060, fooEqBar}}}},
		{viaInput("Via: SIP/2.0/UDP box:5060;foo"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "box", &port5060, singleFoo}}}},
		{viaInput("Via: SIP/2.0/UDP box:5060;foo=//bar"), &viaResult{pass, sip.ViaHeader{&sip.ViaHop{"SIP", "2.0", "UDP", "box", &port5060, fooEqSlashBar}}}},
		{viaInput("Via: /2.0/UDP box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: SIP//UDP box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: SIP/2.0/ box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via:  /2.0/UDP box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: SIP/ /UDP box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: SIP/2.0/  box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: \t/2.0/UDP box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: SIP/\t/UDP box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: SIP/2.0/\t  box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via:"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: "), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via:\t"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: box:5060"), &viaResult{fail, sip.ViaHeader{}}},
		{viaInput("Via: box:5060;foo=bar"), &viaResult{fail, sip.ViaHeader{}}},
	}, t)
}

func TestSupported(t *testing.T) {
	doTests([]test{
		{supportedInput("Supported: replaces, tdialog"), &supportedResult{pass, &sip.SupportedHeader{Options: []string{"replaces", "tdialog"}}}},
		{supportedInput("Supported: replaces, \n\ttdialog"), &supportedResult{pass, &sip.SupportedHeader{Options: []string{"replaces", "tdialog"}}}},
	}, t)
}

// Basic test of unstreamed parsing, using empty INVITE.
func TestUnstreamedParse1(t *testing.T) {
	test := ParserTest{false, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{
			"INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
				"\r\n",
			sip.NewRequest(
				"",
				sip.INVITE,
				&sip.SipUri{
					false,
					sip.String{"bob"},
					nil,
					"biloxi.com",
					nil,
					noParams,
					noParams,
				},
				"SIP/2.0",
				make([]sip.Header, 0),
				"",
				nil,
			),
			nil,
			nil,
		},
	}}

	test.Test(t)
}

// Test unstreamed parsing with a header and body.
func TestUnstreamedParse2(t *testing.T) {
	body := "I am a banana"
	test := ParserTest{false, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{"INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
			"CSeq: 13 INVITE\r\n" +
			"\r\n" +
			body,
			sip.NewRequest(
				"",
				sip.INVITE,
				&sip.SipUri{
					FIsEncrypted: false,
					FUser:        sip.String{Str: "bob"},
					FPassword:    nil,
					FHost:        "biloxi.com",
					FPort:        nil,
					FUriParams:   noParams,
					FHeaders:     noParams,
				},
				"SIP/2.0",
				[]sip.Header{&sip.CSeq{SeqNo: 13, MethodName: sip.INVITE}},
				"I am a banana",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test unstreamed parsing of a core.Request object (rather than a core.Response).
func TestUnstreamedParse3(t *testing.T) {
	body := "Everything is awesome."
	test := ParserTest{false, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{"SIP/2.0 200 OK\r\n" +
			"CSeq: 2 INVITE\r\n" +
			"\r\n" +
			body,
			sip.NewResponse(
				"",
				"SIP/2.0",
				200,
				"OK",
				[]sip.Header{&sip.CSeq{SeqNo: 2, MethodName: sip.INVITE}},
				"Everything is awesome.",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test unstreamed parsing with more than one header.
func TestUnstreamedParse4(t *testing.T) {
	callId := sip.CallID("cheesecake1729")
	maxForwards := sip.MaxForwards(65)
	body := "Everything is awesome."
	test := ParserTest{false, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{"SIP/2.0 200 OK\r\n" +
			"CSeq: 2 INVITE\r\n" +
			"Call-ID: cheesecake1729\r\n" +
			"Max-Forwards: 65\r\n" +
			"\r\n" +
			body,
			sip.NewResponse(
				"",
				"SIP/2.0",
				200,
				"OK",
				[]sip.Header{
					&sip.CSeq{SeqNo: 2, MethodName: sip.INVITE},
					&callId,
					&maxForwards,
				},
				"Everything is awesome.",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test unstreamed parsing with whitespace and line breaks.
func TestUnstreamedParse5(t *testing.T) {
	callId := sip.CallID("cheesecake1729")
	maxForwards := sip.MaxForwards(63)
	body := "Everything is awesome."
	test := ParserTest{false, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{"SIP/2.0 200 OK\r\n" +
			"CSeq:   2     \r\n" +
			"    INVITE\r\n" +
			"Call-ID:\tcheesecake1729\r\n" +
			"Max-Forwards:\t\r\n" +
			"\t63\r\n" +
			"\r\n" +
			body,
			sip.NewResponse(
				"",
				"SIP/2.0",
				200,
				"OK",
				[]sip.Header{
					&sip.CSeq{SeqNo: 2, MethodName: sip.INVITE},
					&callId,
					&maxForwards},
				"Everything is awesome.",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test error responses, and responses of minimal length.
func TestUnstreamedParse6(t *testing.T) {
	test := ParserTest{false, []parserTestStep{
		{"SIP/2.0 403 Forbidden\r\n\r\n",
			sip.NewResponse(
				"",
				"SIP/2.0",
				403,
				"Forbidden",
				[]sip.Header{},
				"",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test requests of minimal length.
func TestUnstreamedParse7(t *testing.T) {
	test := ParserTest{false, []parserTestStep{
		{"ACK sip:foo@bar.com SIP/2.0\r\n" +
			"\r\n",
			sip.NewRequest(
				"",
				sip.ACK,
				&sip.SipUri{
					FIsEncrypted: false,
					FUser:        sip.String{Str: "foo"},
					FPassword:    nil,
					FHost:        "bar.com",
					FPort:        nil,
					FUriParams:   noParams,
					FHeaders:     noParams,
				},
				"SIP/2.0",
				[]sip.Header{},
				"",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test multiple messages
func TestUnstreamedParse8(t *testing.T) {
	test := ParserTest{false, []parserTestStep{
		{"ACK sip:foo@bar.com SIP/2.0\r\n" +
			"\r\n",
			sip.NewRequest(
				"",
				sip.ACK,
				&sip.SipUri{
					FIsEncrypted: false,
					FUser:        sip.String{Str: "foo"},
					FPassword:    nil,
					FHost:        "bar.com",
					FPort:        nil,
					FUriParams:   noParams,
					FHeaders:     noParams,
				},
				"SIP/2.0",
				[]sip.Header{},
				"",
				nil,
			),
			nil,
			nil},
		{"SIP/2.0 200 OK\r\n" +
			"CSeq: 2 INVITE\r\n" +
			"\r\n" +
			"Everything is awesome.",
			sip.NewResponse(
				"",
				"SIP/2.0",
				200,
				"OK",
				[]sip.Header{&sip.CSeq{SeqNo: 2, MethodName: sip.INVITE}},
				"Everything is awesome.",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// TODO: Error cases for unstreamed parse.
// TODO: Multiple writes on unstreamed parse.

// Basic streamed parsing, using empty INVITE.
func TestStreamedParse1(t *testing.T) {
	contentLength := sip.ContentLength(0)
	test := ParserTest{true, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{"INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
			"Content-Length: 0\r\n\r\n",
			sip.NewRequest(
				"",
				sip.INVITE,
				&sip.SipUri{
					FIsEncrypted: false,
					FUser:        sip.String{Str: "bob"},
					FPassword:    nil,
					FHost:        "biloxi.com",
					FPort:        nil,
					FUriParams:   noParams,
					FHeaders:     noParams,
				},
				"SIP/2.0",
				[]sip.Header{&contentLength},
				"",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test writing a single message in two stages (breaking after the start line).
func TestStreamedParse2(t *testing.T) {
	contentLength := sip.ContentLength(0)
	test := ParserTest{true, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{"INVITE sip:bob@biloxi.com SIP/2.0\r\n", nil, nil, nil},
		{"Content-Length: 0\r\n\r\n",
			sip.NewRequest(
				"",
				sip.INVITE,
				&sip.SipUri{
					FIsEncrypted: false,
					FUser:        sip.String{Str: "bob"},
					FPassword:    nil,
					FHost:        "biloxi.com",
					FPort:        nil,
					FUriParams:   noParams,
					FHeaders:     noParams,
				},
				"SIP/2.0",
				[]sip.Header{&contentLength},
				"",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test writing two successive messages, both with bodies.
func TestStreamedParse3(t *testing.T) {
	contentLength23 := sip.ContentLength(23)
	contentLength33 := sip.ContentLength(33)
	test := ParserTest{true, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		{"INVITE sip:bob@biloxi.com SIP/2.0\r\n", nil, nil, nil},
		{"Content-Length: 23\r\n\r\n" +
			"Hello!\r\nThis is a test.",
			sip.NewRequest(
				"",
				sip.INVITE,
				&sip.SipUri{
					FIsEncrypted: false,
					FUser:        sip.String{"bob"},
					FPassword:    nil,
					FHost:        "biloxi.com",
					FPort:        nil,
					FUriParams:   noParams,
					FHeaders:     noParams,
				},
				"SIP/2.0",
				[]sip.Header{&contentLength23},
				"Hello!\r\nThis is a test.",
				nil,
			),
			nil,
			nil},
		{"ACK sip:bob@biloxi.com SIP/2.0\r\n" +
			"Content-Length: 33\r\n" +
			"Contact: sip:alice@biloxi.com\r\n\r\n" +
			"This is an ack! : \n ! \r\n contact:",
			sip.NewRequest(
				"",
				sip.ACK,
				&sip.SipUri{
					FUser:      sip.String{"bob"},
					FPassword:  nil,
					FHost:      "biloxi.com",
					FUriParams: noParams,
					FHeaders:   noParams,
				},
				"SIP/2.0",
				[]sip.Header{
					&contentLength33,
					&sip.ContactHeader{
						Address: &sip.SipUri{
							FUser:      sip.String{"alice"},
							FPassword:  nil,
							FHost:      "biloxi.com",
							FUriParams: noParams,
							FHeaders:   noParams,
						},
						Params: noParams,
					},
				},
				"This is an ack! : \n ! \r\n contact:",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

// Test writing 2 malformed messages followed by 1 correct message
func TestStreamedParse4(t *testing.T) {
	contentLength := sip.ContentLength(0)
	maxForwards := sip.MaxForwards(70)
	test := ParserTest{true, []parserTestStep{
		// Steps each have: Input, result, sent error, returned error
		// invalid start line
		{"INVITE sip:bob@biloxi.com\r\n" +
			"Content-Length: 0\r\n\r\n" +
			"INVITE sip:bob@biloxi.com\r\n" +
			"Content-Length: 0\r\n\r\n",
			nil,
			errors.New("malformed message start line"),
			nil},
		// missing Content-Length
		{"INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
			"Max-Forwards: 70\r\n\r\n",
			nil,
			errors.New("missing content-length header"),
			nil},
		// valid
		{"INVITE sip:bob@biloxi.com SIP/2.0\r\n" +
			"Max-Forwards: 70\r\n" +
			"Content-Length: 0\r\n\r\n",
			sip.NewRequest(
				"",
				sip.INVITE,
				&sip.SipUri{
					FIsEncrypted: false,
					FUser:        sip.String{Str: "bob"},
					FPassword:    nil,
					FHost:        "biloxi.com",
					FPort:        nil,
					FUriParams:   noParams,
					FHeaders:     noParams,
				},
				"SIP/2.0",
				[]sip.Header{&maxForwards, &contentLength},
				"",
				nil,
			),
			nil,
			nil},
	}}

	test.Test(t)
}

type paramInput struct {
	paramString      string
	start            uint8
	sep              uint8
	end              uint8
	quoteValues      bool
	permitSingletons bool
}

func (data *paramInput) String() string {
	return fmt.Sprintf(
		"paramString=\"%s\", start=%c, sep=%c, end=%c, quoteValues=%t, permitSingletons=%t",
		data.paramString,
		data.start,
		data.sep,
		data.end,
		data.quoteValues,
		data.permitSingletons,
	)
}
func (data *paramInput) evaluate() result {
	output, consumed, err := parser.ParseParams(
		data.paramString,
		data.start,
		data.sep,
		data.end,
		data.quoteValues,
		data.permitSingletons,
	)
	return &paramResult{err, output, consumed}
}

type paramResult struct {
	err      error
	params   sip.Params
	consumed int
}

func (expected *paramResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*paramResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.params.ToString('-'))
	} else if actual.err == nil && !expected.params.Equals(actual.params) {
		return false, fmt.Sprintf("unexpected result: expected \"%s\", got \"%s\"",
			expected.params.ToString('-'), actual.params.ToString('-'))
	} else if actual.err == nil && expected.consumed != actual.consumed {
		return false, fmt.Sprintf("unexpected consumed value: expected %d, got %d", expected.consumed, actual.consumed)
	}

	return true, ""
}

type sipUriInput string

func (data sipUriInput) String() string {
	return string(data)
}
func (data sipUriInput) evaluate() result {
	output, err := parser.ParseSipUri(string(data))
	return &sipUriResult{err, output}
}

type sipUriResult struct {
	err error
	uri sip.SipUri
}

func (expected *sipUriResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*sipUriResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.uri.String())
	} else if actual.err != nil {
		// Expected error. Test passes immediately.
		return true, ""
	}

	equal = expected.uri.Equals(&actual.uri)
	if !equal {
		reason = fmt.Sprintf("expected result %s, but got %s", expected.uri.String(), actual.uri.String())
	}
	return
}

type hostPortInput string

func (data hostPortInput) String() string {
	return string(data)
}

func (data hostPortInput) evaluate() result {
	host, port, err := parser.ParseHostPort(string(data))
	return &hostPortResult{err, host, port}
}

type hostPortResult struct {
	err  error
	host string
	port *sip.Port
}

func (expected *hostPortResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*hostPortResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err != nil {
		// Expected failure. Return true unconditionally.
		return true, ""
	}

	var actualStr string
	if actual.port == nil {
		actualStr = actual.host
	} else {
		actualStr = fmt.Sprintf("%s:%d", actual.host, actual.port)
	}

	if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got %s", actualStr)
	} else if expected.host != actual.host {
		return false, fmt.Sprintf("unexpected host part: expected \"%s\", got \"%s\"", expected.host, actual.host)
	} else if portStr(expected.port) != portStr(actual.port) {
		return false, fmt.Sprintf(
			"unexpected port: expected %s, got %s",
			expected.port,
			actual.port,
		)
	}

	return true, ""
}

type headerBlockInput []string

func (data headerBlockInput) String() string {
	return "['" + strings.Join(data, "', '") + "']"
}

func (data headerBlockInput) evaluate() result {
	contents, linesConsumed := parser.GetNextHeaderLine(data)
	return &headerBlockResult{contents, linesConsumed}
}

type headerBlockResult struct {
	contents      string
	linesConsumed int
}

func (expected *headerBlockResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*headerBlockResult))
	if expected.contents != actual.contents {
		return false, fmt.Sprintf("unexpected block contents: got \"%s\"; expected \"%s\"",
			actual.contents, expected.contents)
	} else if expected.linesConsumed != actual.linesConsumed {
		return false, fmt.Sprintf("unexpected number of lines used: %d (expected %d)",
			actual.linesConsumed, expected.linesConsumed)
	}

	return true, ""
}

func parseHeader(rawHeader string) (headers []sip.Header, err error) {
	messages := make(chan sip.Message, 0)
	errors := make(chan error, 0)
	logger := testutils.NewLogrusLogger()
	p := parser.NewParser(messages, errors, false, logger)
	defer func() {
		logger.Debugf("Stopping %p", p)
		p.Stop()
	}()

	headers, err = p.ParseHeader(rawHeader)

	return
}

type toHeaderInput string

func (data toHeaderInput) String() string {
	return string(data)
}

func (data toHeaderInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &toHeaderResult{err, headers[0].(*sip.ToHeader)}
	} else if len(headers) == 0 {
		return &toHeaderResult{err, &sip.ToHeader{}}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by To test: %s", string(data)))
	}
}

type toHeaderResult struct {
	err    error
	header *sip.ToHeader
}

func (expected *toHeaderResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*toHeaderResult))

	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got:\n%s\n\n", actual.header.String())
	} else if expected.err != nil {
		// Expected error. Return true immediately with no further checks.
		return true, ""
	}

	if expected.header.DisplayName != actual.header.DisplayName {
		return false, fmt.Sprintf("unexpected display name: expected \"%s\"; got \"%s\"",
			strMaybeStr(expected.header.DisplayName),
			strMaybeStr(actual.header.DisplayName))
	}

	switch expected.header.Address.(type) {
	case *sip.SipUri:
		uri := *(expected.header.Address.(*sip.SipUri))
		urisEqual := uri.Equals(actual.header.Address)
		msg := ""
		if !urisEqual {
			msg = fmt.Sprintf("unexpected result: expected %s, got %s",
				expected.header.Address.String(), actual.header.Address.String())
		}
		if !urisEqual {
			return false, msg
		}
	default:
		// If you're hitting this block, then you need to do the following:
		// - implement a package-private 'equals' method for the URI schema being tested.
		// - add a case block above for that schema, using the 'equals' method in the same was as the existing core.SipUri block above.
		return false, fmt.Sprintf("no support for testing Uri schema in Uri \"%s\" - fix me!", expected.header.Address)
	}

	if !expected.header.Params.Equals(actual.header.Params) {
		return false, fmt.Sprintf("unexpected parameters \"%s\" (expected \"%s\")",
			actual.header.Params.ToString('-'),
			expected.header.Params.ToString('-'))
	}

	return true, ""
}

type fromHeaderInput string

func (data fromHeaderInput) String() string {
	return string(data)
}

func (data fromHeaderInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &fromHeaderResult{err, headers[0].(*sip.FromHeader)}
	} else if len(headers) == 0 {
		return &fromHeaderResult{err, &sip.FromHeader{}}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by From test: %s", string(data)))
	}
}

type fromHeaderResult struct {
	err    error
	header *sip.FromHeader
}

func (expected *fromHeaderResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*fromHeaderResult))

	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got:\n%s\n\n", actual.header.String())
	} else if expected.err != nil {
		// Expected error. Return true immediately with no further checks.
		return true, ""
	}

	if expected.header.DisplayName != actual.header.DisplayName {
		return false, fmt.Sprintf("unexpected display name: expected \"%s\"; got \"%s\"",
			strMaybeStr(expected.header.DisplayName),
			strMaybeStr(actual.header.DisplayName))
	}

	switch expected.header.Address.(type) {
	case *sip.SipUri:
		uri := *(expected.header.Address.(*sip.SipUri))
		urisEqual := uri.Equals(actual.header.Address)
		msg := ""
		if !urisEqual {
			msg = fmt.Sprintf("unexpected result: expected %s, got %s",
				expected.header.Address.String(), actual.header.Address.String())
		}
		if !urisEqual {
			return false, msg
		}
	default:
		// If you're hitting this block, then you need to do the following:
		// - implement a package-private 'equals' method for the URI schema being tested.
		// - add a case block above for that schema, using the 'equals' method in the same was as the existing core.SipUri block above.
		return false, fmt.Sprintf("no support for testing Uri schema in Uri \"%s\" - fix me!", expected.header.Address)
	}

	if !expected.header.Params.Equals(actual.header.Params) {
		return false, fmt.Sprintf("unexpected parameters \"%s\" (expected \"%s\")",
			actual.header.Params.ToString('-'),
			expected.header.Params.ToString('-'))
	}

	return true, ""
}

type contactHeaderInput string

func (data contactHeaderInput) String() string {
	return string(data)
}

func (data contactHeaderInput) evaluate() result {
	headers, err := parseHeader(string(data))
	contactHeaders := make([]*sip.ContactHeader, len(headers))
	if len(headers) > 0 {
		for idx, header := range headers {
			contactHeaders[idx] = header.(*sip.ContactHeader)
		}
		return &contactHeaderResult{err, contactHeaders}
	} else {
		return &contactHeaderResult{err, contactHeaders}
	}
}

type contactHeaderResult struct {
	err     error
	headers []*sip.ContactHeader
}

func (expected *contactHeaderResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*contactHeaderResult))

	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err != nil {
		// Expected error. Return true immediately with no further checks.
		return true, ""
	}

	var buffer bytes.Buffer
	for _, header := range actual.headers {
		buffer.WriteString(fmt.Sprintf("\n\t%s", header))
	}
	buffer.WriteString("\n\n")
	actualStr := buffer.String()

	if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got: %s", actualStr)
	}

	if len(expected.headers) != len(actual.headers) {
		return false, fmt.Sprintf("expected %d headers; got %d. Last expected header: %s. Last actual header: %s",
			len(expected.headers), len(actual.headers),
			expected.headers[len(expected.headers)-1].String(), actual.headers[len(actual.headers)-1].String())
	}

	for idx := range expected.headers {
		if expected.headers[idx].DisplayName != actual.headers[idx].DisplayName {
			return false, fmt.Sprintf("unexpected display name: expected \"%s\"; got \"%s\"",
				strMaybeStr(expected.headers[idx].DisplayName),
				strMaybeStr(actual.headers[idx].DisplayName))
		}

		UrisEqual := expected.headers[idx].Address.Equals(actual.headers[idx].Address)
		if !UrisEqual {
			return false, fmt.Sprintf("expected Uri %#v; got Uri %#v", expected.headers[idx].Address, actual.headers[idx].Address)
		}

		if !expected.headers[idx].Params.Equals(actual.headers[idx].Params) {
			return false, fmt.Sprintf("unexpected parameters \"%s\" (expected \"%s\")",
				actual.headers[idx].Params.ToString('-'),
				expected.headers[idx].Params.ToString('-'))
		}
	}

	return true, ""
}

type splitByWSInput string

func (data splitByWSInput) String() string {
	return string(data)
}

func (data splitByWSInput) evaluate() result {
	return splitByWSResult(parser.SplitByWhitespace(string(data)))
}

type splitByWSResult []string

func (expected splitByWSResult) equals(other result) (equal bool, reason string) {
	actual := other.(splitByWSResult)
	if len(expected) != len(actual) {
		return false, fmt.Sprintf("unexpected result length in splitByWS test: expected %d %v, got %d %v.", len(expected), expected, len(actual), actual)
	}

	for idx, e := range expected {
		if e != actual[idx] {
			return false, fmt.Sprintf("unexpected result at index %d in splitByWS test: expected '%s'; got '%s'", idx, e, actual[idx])
		}
	}

	return true, ""
}

type cSeqInput string

func (data cSeqInput) String() string {
	return string(data)
}

func (data cSeqInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &cSeqResult{err, headers[0].(*sip.CSeq)}
	} else if len(headers) == 0 {
		return &cSeqResult{err, &sip.CSeq{}}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by core.CSeq test: %s", string(data)))
	}
}

type cSeqResult struct {
	err    error
	header *sip.CSeq
}

func (expected *cSeqResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*cSeqResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && expected.header.SeqNo != actual.header.SeqNo {
		return false, fmt.Sprintf("unexpected sequence number: expected \"%d\", got \"%d\"",
			expected.header.SeqNo, actual.header.SeqNo)
	} else if actual.err == nil && expected.header.MethodName != actual.header.MethodName {
		return false, fmt.Sprintf("unexpected method name: expected %s, got %s", expected.header.MethodName, actual.header.MethodName)
	}

	return true, ""
}

type callIdInput string

func (data callIdInput) String() string {
	return string(data)
}

func (data callIdInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &callIdResult{err, *(headers[0].(*sip.CallID))}
	} else if len(headers) == 0 {
		return &callIdResult{err, ""}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by core.CallID test: %s", string(data)))
	}
}

type callIdResult struct {
	err    error
	header sip.CallID
}

func (expected callIdResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*callIdResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && expected.header.String() != actual.header.String() {
		return false, fmt.Sprintf("unexpected call ID string: expected \"%s\", got \"%s\"",
			expected.header, actual.header)
	}
	return true, ""
}

type maxForwardsInput string

func (data maxForwardsInput) String() string {
	return string(data)
}

func (data maxForwardsInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &maxForwardsResult{err, *(headers[0].(*sip.MaxForwards))}
	} else if len(headers) == 0 {
		return &maxForwardsResult{err, sip.MaxForwards(0)}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by Max-Forwards test: %s", string(data)))
	}
}

type maxForwardsResult struct {
	err    error
	header sip.MaxForwards
}

func (expected *maxForwardsResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*maxForwardsResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && expected.header != actual.header {
		return false, fmt.Sprintf("unexpected Max-Forwards value: expected \"%d\", got \"%d\"",
			expected.header, actual.header)
	}
	return true, ""
}

type expiresInput string

func (data expiresInput) String() string {
	return string(data)
}

func (data expiresInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &expiresResult{err, *(headers[0].(*sip.Expires))}
	} else if len(headers) == 0 {
		return &expiresResult{err, sip.Expires(0)}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by Expires test: %s", string(data)))
	}
}

type expiresResult struct {
	err    error
	header sip.Expires
}

func (expected *expiresResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*expiresResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && expected.header != actual.header {
		return false, fmt.Sprintf("unexpected Expires value: expected \"%d\", got \"%d\"",
			expected.header, actual.header)
	}
	return true, ""
}

type userAgentInput string

func (data userAgentInput) String() string {
	return string(data)
}

func (data userAgentInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &userAgentResult{err, *(headers[0].(*sip.UserAgentHeader))}
	} else if len(headers) == 0 {
		return &userAgentResult{err, ""}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by User-Agent test: %s", string(data)))
	}
}

type userAgentResult struct {
	err    error
	header sip.UserAgentHeader
}

func (expected *userAgentResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*userAgentResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && expected.header != actual.header {
		return false, fmt.Sprintf("unexpected User-Agent value: expected \"%s\", got \"%s\"",
			expected.header, actual.header)
	}
	return true, ""
}

type allowInput string

func (data allowInput) String() string {
	return string(data)
}

func (data allowInput) evaluate() result {
	headers, err := parseHeader(data.String())
	if len(headers) == 1 {
		return &allowResult{err, headers[0].(sip.AllowHeader)}
	} else if len(headers) == 0 {
		return &allowResult{err, sip.AllowHeader{}}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by Allow test: %s", string(data)))
	}
}

type allowResult struct {
	err    error
	header sip.AllowHeader
}

func (expected *allowResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*allowResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && !expected.header.Equals(actual.header) {
		return false, fmt.Sprintf("unexpected Allow value: expected \"%s\", got \"%s\"",
			expected.header, actual.header)
	}
	return true, ""
}

type contentLengthInput string

func (data contentLengthInput) String() string {
	return string(data)
}

func (data contentLengthInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 1 {
		return &contentLengthResult{err, *(headers[0].(*sip.ContentLength))}
	} else if len(headers) == 0 {
		return &contentLengthResult{err, sip.ContentLength(0)}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by Content-Length test: %s", string(data)))
	}
}

type contentLengthResult struct {
	err    error
	header sip.ContentLength
}

func (expected *contentLengthResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*contentLengthResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && expected.header != actual.header {
		return false, fmt.Sprintf("unexpected max forwards value: expected \"%d\", got \"%d\"",
			expected.header, actual.header)
	}
	return true, ""
}

type viaInput string

func (data viaInput) String() string {
	return string(data)
}

func (data viaInput) evaluate() result {
	headers, err := parseHeader(string(data))
	if len(headers) == 0 {
		return &viaResult{err, sip.ViaHeader{}}
	} else if len(headers) == 1 {
		return &viaResult{err, headers[0].(sip.ViaHeader)}
	} else {
		panic("got more than one via header on test " + data)
	}
}

type viaResult struct {
	err    error
	header sip.ViaHeader
}

func (expected *viaResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*viaResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, "unexpected success - got: " + actual.header.String()
	} else if expected.err != nil {
		// Got an error, and were expecting one - return with no further checks.
	} else if len(expected.header) != len(actual.header) {
		return false,
			fmt.Sprintf("unexpected number of entries: expected %d; got %d.\n"+
				"expected the following entries: %s\n"+
				"got the following entries: %s",
				len(expected.header), len(actual.header),
				expected.header.String(), actual.header.String())
	}

	for idx, expectedHop := range expected.header {
		actualHop := (actual.header)[idx]
		if expectedHop.ProtocolName != actualHop.ProtocolName {
			return false, fmt.Sprintf("unexpected protocol name '%s' in via entry %d - expected '%s'",
				actualHop.ProtocolName, idx, expectedHop.ProtocolName)
		} else if expectedHop.ProtocolVersion != actualHop.ProtocolVersion {
			return false, fmt.Sprintf("unexpected protocol version '%s' in via entry %d - expected '%s'",
				actualHop.ProtocolVersion, idx, expectedHop.ProtocolVersion)
		} else if expectedHop.Transport != actualHop.Transport {
			return false, fmt.Sprintf("unexpected transport '%s' in via entry %d - expected '%s'",
				actualHop.Transport, idx, expectedHop.Transport)
		} else if expectedHop.Host != actualHop.Host {
			return false, fmt.Sprintf("unexpected host '%s' in via entry %d - expected '%s'",
				actualHop.Host, idx, expectedHop.Host)
		} else if portStr(expectedHop.Port) != portStr(actualHop.Port) {
			return false, fmt.Sprintf("unexpected port '%d' in via entry %d - expected '%d'",
				actualHop.Port, idx, expectedHop.Port)
		} else if !expectedHop.Params.Equals(actualHop.Params) {
			return false, fmt.Sprintf("unexpected params '%s' in via entry %d - expected '%s'",
				actualHop.Params.ToString('-'),
				idx,
				expectedHop.Params.ToString('-'))
		}
	}

	return true, ""
}

type supportedInput string

func (data supportedInput) String() string {
	return string(data)
}

func (data supportedInput) evaluate() result {
	headers, err := parseHeader(data.String())
	if len(headers) == 1 {
		return &supportedResult{err, headers[0].(*sip.SupportedHeader)}
	} else if len(headers) == 0 {
		return &supportedResult{err, &sip.SupportedHeader{}}
	} else {
		panic(fmt.Sprintf("Multiple headers returned by Supported test: %s", string(data)))
	}
}

type supportedResult struct {
	err    error
	header *sip.SupportedHeader
}

func (expected *supportedResult) equals(other result) (equal bool, reason string) {
	actual := *(other.(*supportedResult))
	if expected.err == nil && actual.err != nil {
		return false, fmt.Sprintf("unexpected error: %s", actual.err.Error())
	} else if expected.err != nil && actual.err == nil {
		return false, fmt.Sprintf("unexpected success: got \"%s\"", actual.header.String())
	} else if actual.err == nil && !expected.header.Equals(actual.header) {
		return false, fmt.Sprintf("unexpected Supported value: expected \"%s\", got \"%s\"",
			expected.header, actual.header)
	}
	return true, ""
}

type ParserTest struct {
	streamed bool
	steps    []parserTestStep
}

func (pt *ParserTest) Test(t *testing.T) {
	testsRun++
	output := make(chan sip.Message)
	errs := make(chan error)
	logger := testutils.NewLogrusLogger()
	p := parser.NewParser(output, errs, pt.streamed, logger)
	defer p.Stop()

	for stepIdx, step := range pt.steps {
		success, reason := step.Test(p, output, errs)
		if !success {
			t.Errorf("failure in pt step %d of input:\n%s\n\nfailure was: %s", stepIdx, pt.String(), reason)
			return
		}
	}
	if !pt.streamed {
		p := parser.NewPacketParser(logger)
		for stepIdx, step := range pt.steps {
			var (
				success bool
				reason  string
			)
			msg, err := p.ParseMessage([]byte(step.input))
			if err != nil {
				success, reason = step.checkError(err)
			} else {
				success, reason = step.checkMsg(msg)
			}
			if !success {
				t.Errorf("failure in pt step %d of input:\n%s\n\nfailure was: %s", stepIdx, pt.String(), reason)
			}
		}
	}

	testsPassed++
	return
}

func (pt *ParserTest) String() string {
	var buffer bytes.Buffer
	buffer.WriteString("[")
	for _, step := range pt.steps {
		buffer.WriteString(step.input)
		buffer.WriteString(",")
	}
	buffer.WriteString("]")
	return buffer.String()
}

type parserTestStep struct {
	input string

	// Slightly kludgy - two of these must be nil at any time.
	result        sip.Message
	sentError     error
	returnedError error
}

func (step *parserTestStep) checkError(err error) (success bool, reason string) {
	if err == nil && step.sentError != nil {
		success = false
		reason = fmt.Sprintf("nil error output from parser; expected: %s", step.sentError.Error())
	} else if err != nil && step.sentError == nil {
		success = false
		reason = fmt.Sprintf("expected no error; parser output: %s", err.Error())
	} else {
		success = true
	}
	return
}

func (step *parserTestStep) checkMsg(msg sip.Message) (success bool, reason string) {
	if msg == nil && step.result != nil {
		success = false
		reason = fmt.Sprintf("nil message returned from parser; expected:\n%s", step.result.String())
	} else if msg != nil && step.result == nil {
		success = false
		reason = fmt.Sprintf("expected no message to be returned; got\n%s", msg.String())
	} else if msg.String() != step.result.String() {
		success = false
		reason = fmt.Sprintf("unexpected message returned by parser; expected:\n\n%s\n\nbut got:\n\n%s", step.result.String(), msg.String())
	} else {
		success = true
	}
	return
}

func (step *parserTestStep) Test(parser parser.Parser, msgChan chan sip.Message, errChan chan error) (success bool, reason string) {
	_, err := parser.Write([]byte(step.input))
	if err != step.returnedError {
		success = false
		reason = fmt.Sprintf("expected returned error %s on write; got %s", errToStr(step.returnedError), errToStr(err))
		return
	} else if step.returnedError != nil {
		success = true
		return
	}

	// TODO - check returns here as they look a bit fishy.
	if err == nil {
		select {
		case msg := <-msgChan:
			success, reason = step.checkMsg(msg)
		case err = <-errChan:
			success, reason = step.checkError(err)
		case <-time.After(time.Second):
			if step.result != nil || step.sentError != nil {
				success = false
				reason = "timeout when processing input"
			} else {
				success = true
			}
		}
	}

	return
}

func TestZZZCountTests(t *testing.T) {
	fmt.Printf("\n *** %d tests run ***", testsRun)
	fmt.Printf("\n *** %d tests passed (%.2f%%) ***\n\n", testsPassed, float32(testsPassed)*100.0/float32(testsRun))
}

func strMaybeStr(s sip.MaybeString) string {
	switch s := s.(type) {
	case sip.String:
		return s.String()
	default:
		return "nil"
	}
}

func portStr(port *sip.Port) string {
	if port == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", *port)
}

func errToStr(err error) string {
	if err == nil {
		return "nil"
	} else {
		return err.Error()
	}
}
