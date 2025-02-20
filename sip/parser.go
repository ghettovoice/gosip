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
	// - if an [io.EOF] happens in the middle of reading headers or body state, then it must be replaced with [io.ErrUnexpectedEOF];
	// - if b contains more than one SIP message, only the first one is parsed and anything else is ignored.
	ParsePacket(b []byte) (Message, error)
	// ParseStream creates a new [StreamParser] for parsing SIP messages from the given [io.Reader].
	ParseStream(r io.Reader) StreamParser
}

// StreamParser is an interface for parsing SIP messages from a byte stream.
//
// It provides an iterator that yields each parsed [Message] and an error, if any.
type StreamParser interface {
	// Messages returns an iterator that yields each parsed [Message] and an error, if any.
	//
	// Any implementations must satisfy the following contract:
	// - in success case, it yields a [Message] and nil error;
	// - if an error occurs during parsing, it yields a nil (or incomplete) message and non-nil error;
	// - if an [io.EOF] happens in the middle of reading headers or body state, then it must be replaced with [io.ErrUnexpectedEOF];
	// - the iterator is closed when the consumer breaks the loop.
	//
	// Example:
	//	for msg, err := range p.Messages() {
	//		if err != nil {
	//			var perr *sip.ParseError
	//			if errors.As(err, &perr) {
	//				// handle error and decide break or continue
	//				// msg can contain an incomplete message
	//			}
	//			break
	//		}
	//		// everything ok, the message is valid
	//	}
	Messages() iter.Seq2[Message, error]
}

// StdParser is a standard implementation of the [Parser] interface for parsing SIP messages.
//
// It provides methods to parse a single SIP message from a byte slice or multiple SIP messages from a byte stream.
// Custom header parsing is supported through the HeaderParsers map, which allows for extending the default parsing
// capabilities with user-defined header parsers.
type StdParser struct {
	// HeaderParsers is a map of custom header parsers where the key is the header name
	// and the value is the [HeaderParser].
	HeaderParsers map[string]HeaderParser
}

// ParsePacket parses a single SIP message from the given buffer b.
func (p *StdParser) ParsePacket(b []byte) (Message, error) {
	r := getBytesRdr(b)
	br := getBufRdr(r)
	defer func() {
		freeBufRdr(br)
		freeBytesRdr(r)
	}()
	return parseMessage(br, p.HeaderParsers, true)
}

// ParseStream creates a new [StdStreamParser] for parsing SIP messages from the given [io.Reader].
// The returned [StdStreamParser] uses the same header parsers as the [StdParser].
func (p *StdParser) ParseStream(rdr io.Reader) StreamParser {
	return &StdStreamParser{
		rdr:  rdr,
		prss: p.HeaderParsers,
	}
}

// StdStreamParser is a standard implementation of the [StreamParser] interface
// for parsing SIP messages from a byte stream.
// It can be initialized with [StdParser.ParseStream] method.
type StdStreamParser struct {
	rdr  io.Reader
	prss map[string]HeaderParser
}

// Messages returns an iterator that yields each parsed [Message] and an error, if any.
// See [StreamParser.Messages] for more details.
func (p *StdStreamParser) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		br := getBufRdr(p.rdr)
		defer freeBufRdr(br)
		for {
			msg, err := parseMessage(br, p.prss, false)
			if !yield(msg, err) {
				break
			}
		}
	}
}

// ParseError represents an error that occurred during parsing.
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

// ParseState represents the current parsing state.
type ParseState int

const (
	ParseStateStart   ParseState = iota // parsing message start line
	ParseStateHeaders                   // parsing message headers
	ParseStateBody                      // parsing message body
)

//nolint:gocognit
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
			hdrs := make(Headers)
			SetMessageHeaders(msg, hdrs)
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
				hdrs.Append(hdr)
			}

			var size int
			if hdrs := hdrs.Get("Content-Length"); len(hdrs) > 0 {
				if cl, ok := hdrs[0].(header.ContentLength); ok {
					size = int(cl)
				}
			} else if packetMode {
				size = rdr.Buffered()
			} else {
				return msg, &ParseError{&missingHeaderError{"Content-Length"}, state, nil}
			}
			if size == 0 {
				return msg, nil
			}
			SetMessageBody(msg, make([]byte, size))

			state = ParseStateBody
		case ParseStateBody:
			buf := GetMessageBody(msg)
			if n, err := io.ReadFull(rdr, buf); err != nil {
				if errors.Is(err, io.EOF) {
					// io.EOF possible only if no bytes where read
					// but if we here in parseStateBody then the body has a non-zero size
					err = io.ErrUnexpectedEOF
				}
				// if packetMode && errors.Is(err, io.ErrUnexpectedEOF) {
				// 	err = grammar.ErrMalformedInput
				// }
				return msg, &ParseError{err, state, buf[:n]}
			}
			return msg, nil
		}
	}
}

var defaultParser = &StdParser{}

// DefaultParser returns the default parser that can be used for parsing SIP messages.
func DefaultParser() *StdParser { return defaultParser }

// ParsePacket parses a single SIP message from the given buffer b using the default parser.
func ParsePacket(b []byte) (Message, error) { return defaultParser.ParsePacket(b) }

// ParseStream creates a new [StdStreamParser] for parsing SIP messages from the given [io.Reader].
func ParseStream(r io.Reader) *StdStreamParser {
	return defaultParser.ParseStream(r).(*StdStreamParser) //nolint:forcetypeassert
}
