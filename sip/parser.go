package sip

import (
	"bufio"
	"fmt"
	"io"
	"iter"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// Parser is an interface for parsing SIP messages.
//
// It provides methods for parsing a single SIP message from a byte slice or for parsing multiple SIP messages from a
// byte stream.
// The [Parser] type is typically used as a factory for creating [StreamParser].
type Parser interface {
	// ParsePacket parses a single SIP message from the given buffer b.
	// The buffer b must contain a full SIP message.
	// In success case, it returns a [Message] and nil error.
	// If a message is incomplete, or an error occurs during parsing, it returns nil message and non-nil error.
	// If b contains more than one SIP message, only the first one is parsed and anything else is ignored.
	ParsePacket(b []byte) (Message, error)
	// ParseStream creates a new [StreamParser] for parsing SIP messages from the given [io.Reader].
	ParseStream(r io.Reader) StreamParser
}

// StreamParser is an interface for parsing SIP messages from a byte stream.
//
// It provides an iterator that yields each parsed [Message] and an error, if any.
type StreamParser interface {
	// Messages returns a single-use iterator that yields each parsed [Message] and an error, if any.
	//
	// In success case, it yields a [Message] and nil error.
	// If a message is incomplete, or an error occurs during parsing, it yields nil message and non-nil error.
	// The iterator is closed when the consumer breaks the loop.
	Messages() iter.Seq2[Message, error]
}

// StdParser is a standard implementation of the [Parser] interface for parsing SIP messages.
//
// It provides methods to parse a single SIP message from a byte slice or multiple SIP messages from a byte stream.
type StdParser struct{}

// ParsePacket parses a single SIP message from the given buffer b.
// See [Parser.ParsePacket] for details.
func (*StdParser) ParsePacket(b []byte) (Message, error) {
	r := util.GetBytesReader(b)
	br := getBufferedRdr(r)
	defer func() {
		freeBufferedRdr(br)
		util.FreeBytesReader(r)
	}()
	return errtrace.Wrap2(parseMsg(br, true))
}

// ParseStream creates a new [StdStreamParser] for parsing SIP messages from the given [io.Reader].
// The returned [StdStreamParser] uses the same header parsers as the [StdParser].
func (*StdParser) ParseStream(rdr io.Reader) StreamParser {
	return &StdStreamParser{rdr: rdr}
}

// StdStreamParser is a standard implementation of the [StreamParser] interface
// for parsing SIP messages from a byte stream.
// It can be initialized with [StdParser.ParseStream] method.
type StdStreamParser struct {
	rdr io.Reader
}

// Messages returns a single-use iterator that yields each parsed [Message] and an error, if any.
// See [StreamParser.Messages] for more details.
func (p *StdStreamParser) Messages() iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		br := getBufferedRdr(p.rdr)
		defer freeBufferedRdr(br)
		for {
			msg, err := parseMsg(br, false)
			if !yield(msg, errtrace.Wrap(err)) {
				break
			}
		}
	}
}

// ParseError represents an error that occurred during parsing.
// It contains the error that occurred, the current parsing state,
// the bytes that caused the error and the parsed incomplete message, if any.
type ParseError struct {
	Err   error      // the error that occurred during parsing
	State ParseState // the current parsing state when the error occurred
	Data  []byte     // the bytes that caused the error, if any
	Msg   Message    // the parsed incomplete message, if any
}

func (err *ParseError) Error() string {
	if err == nil {
		return sNilTag
	}
	return fmt.Sprintf("parse failed (state=%q data=%q): %v", err.State, util.Ellipsis(string(err.Data), 10), err.Err)
}

func (err *ParseError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func (err *ParseError) Grammar() bool { return err != nil && errorutil.IsGrammarErr(err.Err) }

func (err *ParseError) Timeout() bool { return err != nil && errorutil.IsTimeoutErr(err.Err) }

func (err *ParseError) Temporary() bool { return err != nil && errorutil.IsTemporaryErr(err.Err) }

// ParseState represents the current parsing state.
type ParseState int

const (
	ParseStateStart   ParseState = iota // parsing message start line
	ParseStateHeaders                   // parsing message headers
	ParseStateBody                      // parsing message body
)

func (s ParseState) String() string {
	switch s {
	case ParseStateStart:
		return "start"
	case ParseStateHeaders:
		return "headers"
	case ParseStateBody:
		return "body"
	default:
		return "???"
	}
}

//nolint:gocognit
func parseMsg(rdr *bufio.Reader, packetMode bool) (Message, error) {
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
				return nil, errtrace.Wrap(err)
			}

			msg, err = parseMsgStart(line)
			if err != nil {
				return nil, errtrace.Wrap(&ParseError{
					Err:   err,
					State: state,
					Data:  line,
				})
			}

			state = ParseStateHeaders
		case ParseStateHeaders:
			hdrs := make(Headers)
			SetMessageHeaders(msg, hdrs)
			for {
				line, err := txtRdr.ReadContinuedLineBytes()
				if err != nil {
					return nil, errtrace.Wrap(&ParseError{
						Err:   NewInvalidMessageError("incomplete headers"),
						State: state,
						Data:  line,
						Msg:   msg,
					})
				}

				if len(line) == 0 {
					break
				}

				hdr, err := ParseHeader(line)
				if err != nil {
					return nil, errtrace.Wrap(&ParseError{
						Err:   err,
						State: state,
						Data:  line,
						Msg:   msg,
					})
				}
				hdrs.Append(hdr)
			}

			var size int
			switch {
			case hdrs.Has("Content-Length"):
				if ct, ok := hdrs.ContentLength(); ok {
					if uint64(ct) > uint64(MaxMsgSize) {
						return nil, errtrace.Wrap(&ParseError{
							Err:   fmt.Errorf("%w: Content-Length exceeds max size %d", ErrEntityTooLarge, MaxMsgSize),
							State: state,
							Msg:   msg,
						})
					}
					size = int(ct)
				}
			case packetMode:
				size = rdr.Buffered()
			default:
				return nil, errtrace.Wrap(&ParseError{
					Err:   NewInvalidMessageError(newMissHdrErr("Content-Length")),
					State: state,
					Msg:   msg,
				})
			}
			if size == 0 {
				return msg, nil
			}
			SetMessageBody(msg, make([]byte, size))

			state = ParseStateBody
		case ParseStateBody:
			buf := GetMessageBody(msg)
			if n, err := io.ReadFull(rdr, buf); err != nil {
				return nil, errtrace.Wrap(&ParseError{
					Err:   NewInvalidMessageError("incomplete body"),
					State: state,
					Data:  buf[:n],
					Msg:   msg,
				})
			}
			return msg, nil
		}
	}
}

var defParser = &StdParser{}

// DefaultParser returns the default [StdParser].
func DefaultParser() *StdParser { return defParser }

// ParsePacket parses a single SIP message from the given buffer b using the default parser.
// See [StdParser.ParsePacket] for details.
func ParsePacket(b []byte) (Message, error) { return errtrace.Wrap2(defParser.ParsePacket(b)) }

// ParseStream creates a new [StdStreamParser] using the default parser and returns an iterator over reader r,
// that yields each parsed [Message] or error, if any.
// See [StdParser.ParseStream] for details.
func ParseStream(r io.Reader) iter.Seq2[Message, error] { return defParser.ParseStream(r).Messages() }
