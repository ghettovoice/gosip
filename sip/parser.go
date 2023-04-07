package sip

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"iter"

	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/header"
)

// Parser is an interface for parsing SIP messages.
//
// It provides methods for parsing a single SIP message from a byte slice or for parsing multiple SIP messages from a
// byte stream.
// The [Parser] type is typically used as a factory for creating [StreamParser].
type Parser interface {
	// ParsePacket parses a single SIP message from the given buffer b.
	//
	// Any implementations must satisfy the following contract:
	// - it assumes that the b contains a full SIP message;
	// - in success case, it returns a [Message] and nil error;
	// - if a message is incomplete, or an error occurs during parsing, it returns incomplete message and non-nil error;
	// - if b contains more than one SIP message, only the first one is parsed and anything else is ignored.
	ParsePacket(b []byte) (Message, error)
	// ParseStream creates a new [StreamParser] for parsing SIP messages from the given [io.Reader].
	ParseStream(r io.Reader) StreamParser
}

// StreamParser is an interface for parsing SIP messages from a byte stream.
//
// It provides an iterator that yields each parsed [Message] and an error, if any.
// The iterator is closed when the consumer breaks the loop.
type StreamParser interface {
	// Messages returns an iterator that yields each parsed [Message] and an error, if any.
	//
	// Any implementations must satisfy the following contract:
	// - in success case, it yields a [Message] and nil error;
	// - if an error occurs during parsing, it yields nil (or incomplete) message and non-nil error.
	Messages() iter.Seq2[Message, error]
}

var defParser = &DefaultParser{}

// ParsePacket parses a single SIP message from the given buffer b using the default parser.
// See [DefaultParser.ParsePacket] for details.
func ParsePacket(b []byte) (Message, error) { return defParser.ParsePacket(b) }

// ParseStream creates a new [StreamParser] for parsing SIP messages from the given [io.Reader] using the default
// parser.
// See [DefaultParser.ParseStream] for details.
func ParseStream(r io.Reader) StreamParser { return defParser.ParseStream(r) }

// DefaultParser implements the [Parser] interface.
//
// It provides methods for parsing a single SIP message from a byte slice or for parsing multiple SIP messages from a
// byte stream.
// The parser can be configured with custom header parsers to parse non-standard headers.
// The [DefaultParser] type is typically used as a factory for creating [DefaultStreamParser], which is used to parse
// byte stream.
type DefaultParser struct {
	// HeaderParsers is a set of custom header parsers
	HeaderParsers map[string]HeaderParser
}

// ParsePacket parses a single SIP message from the given buffer b.
//
// It assumes that the b contains a full SIP message.
// In success case, it returns a [Message] and nil error.
// If a message is incomplete, or an error occurs during parsing, it returns incomplete message and non-nil error.
//
// If b contains more than one SIP message, only the first one is parsed and anything else is ignored.
// To parse multiple messages, use [DefaultStreamParser] that can be built from [DefaultParser.ParseStream] method.
func (p *DefaultParser) ParsePacket(b []byte) (Message, error) {
	r := getBytesRdr(b)
	br := getBufRdr(r)
	defer func() {
		freeBufRdr(br)
		freeBytesRdr(r)
	}()
	return parseMessage(br, p.HeaderParsers, true)
}

// ParseStream creates a new [DefaultStreamParser] for parsing SIP messages from the given [io.Reader].
//
// The returned [DefaultStreamParser] uses the same header parsers as the original [DefaultParser].
// It is suitable for parsing multiple SIP messages from a continuous byte stream.
func (p *DefaultParser) ParseStream(rdr io.Reader) StreamParser {
	sp := newStreamParser(rdr)
	sp.HeaderParsers = p.HeaderParsers
	return sp
}

// DefaultStreamParser parses a stream of SIP messages.
//
// It can be initialized using [DefaultParser.ParseStream] method.
type DefaultStreamParser struct {
	HeaderParsers map[string]HeaderParser

	rdr io.Reader
}

func newStreamParser(rdr io.Reader) *DefaultStreamParser {
	return &DefaultStreamParser{rdr: rdr}
}

// Messages returns an iterator that yields each parsed [Message] and an error, if any.
//
// In succeeded case, it yields a [Message] and nil error.
// If an error occurs during parsing, it yields nil (or incomplete) message and non-nil error.
// If an [io.EOF] happens in the middle of reading headers or body state, then it will be replaced with
// [io.ErrUnexpectedEOF].
//
// The iterator is closed when the consumer breaks the loop.
//
// Example:
//
//	for msg, err := range p.Messages() {
//		if err != nil {
//			var perr *sip.ParseError
//			if errors.As(err, &perr) {
//				// handle error and decide break or continue
//				// msg can contain incomplete message
//			}
//			break
//		}
//		// everything ok, message is valid
//	}
func (p *DefaultStreamParser) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		br := getBufRdr(p.rdr)
		defer freeBufRdr(br)
		for {
			msg, err := parseMessage(br, p.HeaderParsers, false)
			if !yield(msg, err) {
				break
			}
		}
	}
}

// ParseError represents an error that occurred during parsing.
//
// It contains the error that occurred, the current parsing state and the bytes that caused the error.
type ParseError struct {
	Err   error
	State ParseState
	Buf   []byte
}

func (err *ParseError) Error() string {
	return fmt.Sprintf("parse error: %v", err.Err)
}

func (err *ParseError) Unwrap() error { return err.Err }

func (err *ParseError) Grammar() bool { return utils.IsGrammarErr(err.Err) }

func (err *ParseError) Timeout() bool { return utils.IsTimeoutErr(err.Err) }

func (err *ParseError) Temporary() bool { return utils.IsTemporaryErr(err.Err) }

type ParseState int

const (
	ParseStateStart   ParseState = iota // parsing message start line
	ParseStateHeaders                   // parsing message headers
	ParseStateBody                      // parsing message body
)

func parseMessage(rdr *bufio.Reader, hdrParsers map[string]HeaderParser, packetMode bool) (Message, error) {
	var (
		state ParseState
		msg   Message
	)
	txtRdr := getTxtProtoRdr(rdr)
	defer freeTxtProtoRdr(txtRdr)
	for {
		switch state {
		case ParseStateStart:
			line, err := txtRdr.ReadLineBytes()
			if err != nil {
				// if packetMode && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
				// 	err = grammar.ErrEmptyInput
				// }
				return nil, &ParseError{err, state, nil}
			}

			msg, err = parseMessageStart(line)
			if err != nil {
				return msg, &ParseError{err, state, line}
			}

			state = ParseStateHeaders
		case ParseStateHeaders:
			msg.SetMessageHeaders(make(Headers))
			for {
				line, err := txtRdr.ReadContinuedLineBytes()
				if err != nil {
					if errors.Is(err, io.EOF) {
						err = io.ErrUnexpectedEOF
					}
					// if packetMode && errors.Is(err, io.ErrUnexpectedEOF) {
					// 	err = grammar.ErrMalformedInput
					// }
					return msg, &ParseError{err, state, nil}
				}

				if len(line) == 0 {
					break
				}

				hdr, err := ParseHeader(line, hdrParsers)
				if err != nil {
					return msg, &ParseError{err, state, line}
				}
				msg.MessageHeaders().Append(hdr)
			}

			var size int
			if clHdrs := msg.MessageHeaders().Get("Content-Length"); len(clHdrs) > 0 {
				size = int(clHdrs[0].(header.ContentLength))
			} else if packetMode {
				size = rdr.Buffered()
			} else {
				return msg, &ParseError{&missingHeaderError{"Content-Length"}, state, nil}
			}
			if size == 0 {
				return msg, nil
			}
			msg.SetMessageBody(make([]byte, size))

			state = ParseStateBody
		case ParseStateBody:
			if n, err := io.ReadFull(rdr, msg.MessageBody()); err != nil {
				if errors.Is(err, io.EOF) {
					// io.EOF possible only if no bytes where read
					// but if we here in parseStateBody then the body has non-zero size
					err = io.ErrUnexpectedEOF
				}
				// if packetMode && errors.Is(err, io.ErrUnexpectedEOF) {
				// 	err = grammar.ErrMalformedInput
				// }
				return msg, &ParseError{err, state, msg.MessageBody()[:n]}
			}
			return msg, nil
		}
	}
}
