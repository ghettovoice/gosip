package parser

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ghettovoice/gossip/message"
	"github.com/ghettovoice/gossip/log"
	"github.com/ghettovoice/gossip/utils"
)

// The whitespace characters recognised by the Augmented Backus-Naur Form syntax
// that SIP uses (RFC 3261 S.25).
const c_ABNF_WS = " \t"

// The maximum permissible CSeq number in a SIP message (2**31 - 1).
// C.f. RFC 3261 S. 8.1.1.5.
const MAX_CSEQ = 2147483647

// The buffer size of the parser input channel.

// A Parser converts the raw bytes of SIP messages into base.SipMessage objects.
// It allows
type Parser interface {
	log.WithLocalLogger
	// Implements io.Writer. Queues the given bytes to be parsed.
	// If the parser has terminated due to a previous fatal error, it will return n=0 and an appropriate error.
	// Otherwise, it will return n=len(p) and err=nil.
	// Note that err=nil does not indicate that the data provided is valid - simply that the data was successfully queued for parsing.
	Write(p []byte) (n int, err error)

	// Register a custom header parser for a particular header type.
	// This will overwrite any existing registered parser for that header type.
	// If a parser is not available for a header type in a message, the parser will produce a base.GenericHeader struct.
	SetHeaderParser(headerName string, headerParser HeaderParser)

	Stop()
}

// A HeaderParser is any function that turns raw header data into one or more SipHeader objects.
// The HeaderParser will receive arguments of the form ("max-forwards", "70").
// It should return a slice of headers, which should have length > 1 unless it also returns an error.
type HeaderParser func(headerName string, headerData string) (
	headers []message.SipHeader, err error)

func defaultHeaderParsers() map[string]HeaderParser {
	return map[string]HeaderParser{
		"to":             parseAddressHeader,
		"t":              parseAddressHeader,
		"from":           parseAddressHeader,
		"f":              parseAddressHeader,
		"contact":        parseAddressHeader,
		"m":              parseAddressHeader,
		"call-id":        parseCallId,
		"cseq":           parseCSeq,
		"via":            parseViaHeader,
		"v":              parseViaHeader,
		"max-forwards":   parseMaxForwards,
		"content-length": parseContentLength,
		"l":              parseContentLength,
	}
}

// Parse a SIP message by creating a parser on the fly.
// This is more costly than reusing a parser, but is necessary when we do not
// have a guarantee that all messages coming over a connection are from the
// same endpoint (e.g. UDP).
func ParseMessage(msgData []byte, logger log.Logger) (message.SipMessage, error) {
	output := make(chan message.SipMessage, 0)
	errors := make(chan error, 0)
	parser := NewParser(output, errors, false, logger)
	defer parser.Stop()

	parser.Write(msgData)
	select {
	case msg := <-output:
		return msg, nil
	case err := <-errors:
		return nil, err
	}
}

// Create a new Parser.
//
// Parsed SIP messages will be sent down the 'output' chan provided.
// Any errors which force the parser to terminate will be sent down the 'errs' chan provided.
//
// If streamed=false, each Write call to the parser should contain data for one complete SIP message.

// If streamed=true, Write calls can contain a portion of a full SIP message.
// The end of one message and the start of the next may be provided in a single call to Write.
// When streamed=true, all SIP messages provided must have a Content-Length header.
// SIP messages without a Content-Length will cause the parser to permanently stop, and will result in an error on the errs chan.

// 'streamed' should be set to true whenever the caller cannot reliably identify the starts and ends of messages from the transport frames,
// e.g. when using streamed protocols such as TCP.
func NewParser(output chan<- message.SipMessage, errs chan<- error, streamed bool, logger log.Logger) Parser {
	p := parser{streamed: streamed, log: logger}

	// Configure the parser with the standard set of header parsers.
	p.headerParsers = make(map[string]HeaderParser)
	for headerName, headerParser := range defaultHeaderParsers() {
		p.SetHeaderParser(headerName, headerParser)
	}

	p.output = output
	p.errs = errs

	if !streamed {
		// If we're not in streaming mode, set up a channel so the Write method can pass calculated body lengths to the parser.
		p.bodyLengths.Init(p.Log())
	}

	// Create a managed buffer to allow message data to be asynchronously provided to the parser, and
	// to allow the parser to block until enough data is available to parse.
	p.input = newParserBuffer(p.Log())

	// Wait for input a line at a time, and produce SipMessages to send down p.output.
	go p.parse(streamed)

	return &p
}

type parser struct {
	headerParsers map[string]HeaderParser
	streamed      bool
	input         *parserBuffer
	bodyLengths   utils.ElasticChan
	output        chan<- message.SipMessage
	errs          chan<- error
	terminalErr   error
	stopped       bool
	log           log.Logger
}

func (p *parser) Log() log.Logger {
	return p.log.WithField("pars-ptr", fmt.Sprintf("%p", p))
}

func (p *parser) Write(data []byte) (n int, err error) {
	if p.terminalErr != nil {
		// The parser has stopped due to a terminal error. Return it.
		p.Log().Warnf("parser %p ignores %d new bytes due to previous terminal error: %s", p, len(data), p.terminalErr.Error())
		return 0, p.terminalErr
	} else if p.stopped {
		return 0, fmt.Errorf("cannot write data to stopped parser %p", p)
	}

	if !p.streamed {
		l := getBodyLength(data)
		p.bodyLengths.In <- l
	}

	p.input.Write(data)
	return len(data), nil
}

// Stop parser processing, and allow all resources to be garbage collected.
// The parser will not release its resources until Stop() is called,
// even if the parser object itself is garbage collected.
func (p *parser) Stop() {
	p.Log().Debugf("stopping parser %p", p)
	p.stopped = true
	p.input.Stop()
	p.Log().Debugf("parser %p stopped", p)
}

// Consume input lines one at a time, producing base.SipMessage objects and sending them down p.output.
func (p *parser) parse(requireContentLength bool) {
	var message message.SipMessage

	for {
		// Parse the StartLine.
		startLine, err := p.input.NextLine()

		if err != nil {
			p.Log().Debugf("parser %p stopped", p)
			break
		}

		if isRequest(startLine) {
			method, recipient, sipVersion, err := parseRequestLine(startLine)
			message = message.NewRequest(method, recipient, sipVersion, []message.SipHeader{}, "", p.Log())
			p.terminalErr = err
		} else if isResponse(startLine) {
			sipVersion, statusCode, reason, err := parseStatusLine(startLine)
			message = message.NewResponse(sipVersion, statusCode, reason, []message.SipHeader{}, "", p.Log())
			p.terminalErr = err
		} else {
			p.terminalErr = fmt.Errorf("transmission beginning '%s' is not a SIP message", startLine)
		}

		if p.terminalErr != nil {
			p.terminalErr = fmt.Errorf("failed to parse first line of message: %s", p.terminalErr.Error())
			p.errs <- p.terminalErr
			break
		}

		// Parse the header section.
		// Headers can be split across lines (marked by whitespace at the start of subsequent lines),
		// so store lines into a buffer, and then flush and parse it when we hit the end of the header.
		var buffer bytes.Buffer
		headers := make([]message.SipHeader, 0)

		flushBuffer := func() {
			if buffer.Len() > 0 {
				newHeaders, err := p.parseHeader(buffer.String())
				if err == nil {
					headers = append(headers, newHeaders...)
				} else {
					p.Log().Debugf("skipping header '%s' due to error: %s", buffer.String(), err.Error())
				}
				buffer.Reset()
			}
		}

		for {
			line, err := p.input.NextLine()

			if err != nil {
				p.Log().Debugf("parser %p stopped", p)
				break
			}

			if len(line) == 0 {
				// We've hit the end of the header section.
				// Parse anything remaining in the buffer, then break out.
				flushBuffer()
				break
			}

			if !strings.Contains(c_ABNF_WS, string(line[0])) {
				// This line starts a new header.
				// Parse anything currently in the buffer, then store the new header line in the buffer.
				flushBuffer()
				buffer.WriteString(line)
			} else if buffer.Len() > 0 {
				// This is a continuation line, so just add it to the buffer.
				buffer.WriteString(" ")
				buffer.WriteString(line)
			} else {
				// This is a continuation line, but also the first line of the whole header section.
				// Discard it and log.
				p.Log().Debugf(
					"discarded unexpected continuation line '%s' at start of header block in message '%s'",
					line,
					message.Short())
			}
		}

		// Store the headers in the message object.
		for _, header := range headers {
			message.SetHeader(header, true)
		}

		var contentLength int

		// Determine the length of the body, so we know when to stop parsing this message.
		if p.streamed {
			// Use the content-length header to identify the end of the message.
			contentLengthHeaders := message.Headers("Content-Length")
			if len(contentLengthHeaders) == 0 {
				p.terminalErr = fmt.Errorf("missing required content-length header on message %s", message.Short())
				p.errs <- p.terminalErr
				break
			} else if len(contentLengthHeaders) > 1 {
				var errbuf bytes.Buffer
				errbuf.WriteString("multiple content-length headers on message ")
				errbuf.WriteString(message.Short())
				errbuf.WriteString(":\n")
				for _, header := range contentLengthHeaders {
					errbuf.WriteString("\t")
					errbuf.WriteString(header.String())
				}
				p.terminalErr = fmt.Errorf(errbuf.String())
				p.errs <- p.terminalErr
				break
			}

			contentLength = int(*(contentLengthHeaders[0].(*message.ContentLength)))
		} else {
			// We're not in streaming mode, so the Write method should have calculated the length of the body for us.
			contentLength = (<-p.bodyLengths.Out).(int)
		}

		// Extract the message body.
		body, err := p.input.NextChunk(contentLength)

		if err != nil {
			p.Log().Debugf("parsed %p stopped", p)
			break
		}

		switch message.(type) {
		case *message.Request:
			message.(*message.Request).SetBody(body)
		case *message.Response:
			message.(*message.Response).SetBody(body)
		default:
			p.Log().Errorf("internal error - message %s is neither a request type nor a response type", message.Short())
		}
		p.output <- message
	}

	if !p.streamed {
		// We're in unstreamed mode, so we created a bodyLengths ElasticChan which
		// needs to be disposed.
		close(p.bodyLengths.In)
	}
	return
}

// Implements ParserFactory.SetHeaderParser.
func (p *parser) SetHeaderParser(headerName string, headerParser HeaderParser) {
	headerName = strings.ToLower(headerName)
	p.headerParsers[headerName] = headerParser
}

// Calculate the size of a SIP message's body, given the entire contents of the message as a byte array.
func getBodyLength(data []byte) int {
	s := string(data)

	// Body starts with first character following a double-CRLF.
	bodyStart := strings.Index(s, "\r\n\r\n") + 4

	return len(s) - bodyStart
}

// Heuristic to determine if the given transmission looks like a SIP request.
// It is guaranteed that any RFC3261-compliant request will pass this test,
// but invalid messages may not necessarily be rejected.
func isRequest(startLine string) bool {
	// SIP request lines contain precisely two spaces.
	if strings.Count(startLine, " ") != 2 {
		return false
	}

	// Check that the version string starts with SIP.
	parts := strings.Split(startLine, " ")
	if len(parts) < 3 {
		return false
	} else if len(parts[2]) < 3 {
		return false
	} else {
		return strings.ToUpper(parts[2][:3]) == "SIP"
	}
}

// Heuristic to determine if the given transmission looks like a SIP response.
// It is guaranteed that any RFC3261-compliant response will pass this test,
// but invalid messages may not necessarily be rejected.
func isResponse(startLine string) bool {
	// SIP status lines contain at least two spaces.
	if strings.Count(startLine, " ") < 2 {
		return false
	}

	// Check that the version string starts with SIP.
	parts := strings.Split(startLine, " ")
	if len(parts) < 3 {
		return false
	} else if len(parts[0]) < 3 {
		return false
	} else {
		return strings.ToUpper(parts[0][:3]) == "SIP"
	}
}

// Parse the first line of a SIP request, e.g:
//   INVITE bob@example.com SIP/2.0
//   REGISTER jane@telco.com SIP/1.0
func parseRequestLine(requestLine string) (
	method message.Method, recipient message.Uri, sipVersion string, err error) {
	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 {
		err = fmt.Errorf("request line should have 2 spaces: '%s'", requestLine)
		return
	}

	method = message.Method(strings.ToUpper(parts[0]))
	recipient, err = ParseUri(parts[1])
	sipVersion = parts[2]

	switch recipient.(type) {
	case *message.WildcardUri:
		err = fmt.Errorf("wildcard URI '*' not permitted in request line: '%s'", requestLine)
	}

	return
}

// Parse the first line of a SIP response, e.g:
//   SIP/2.0 200 OK
//   SIP/1.0 403 Forbidden
func parseStatusLine(statusLine string) (
	sipVersion string, statusCode uint16, reasonPhrase string, err error) {
	parts := strings.Split(statusLine, " ")
	if len(parts) < 3 {
		err = fmt.Errorf("status line has too few spaces: '%s'", statusLine)
		return
	}

	sipVersion = parts[0]
	statusCodeRaw, err := strconv.ParseUint(parts[1], 10, 16)
	statusCode = uint16(statusCodeRaw)
	reasonPhrase = strings.Join(parts[2:], "")

	return
}

// parseUri converts a string representation of a URI into a Uri object.
// If the URI is malformed, or the URI schema is not recognised, an error is returned.
// URIs have the general form of schema:address.
func ParseUri(uriStr string) (uri message.Uri, err error) {
	if strings.TrimSpace(uriStr) == "*" {
		// Wildcard '*' URI used in the Contact headers of REGISTERs when unregistering.
		return message.WildcardUri{}, nil
	}

	colonIdx := strings.Index(uriStr, ":")
	if colonIdx == -1 {
		err = fmt.Errorf("no ':' in URI %s", uriStr)
		return
	}

	switch strings.ToLower(uriStr[:colonIdx]) {
	case "sip":
		var sipUri message.SipUri
		sipUri, err = ParseSipUri(uriStr)
		uri = &sipUri
	case "sips":
		// SIPS URIs have the same form as SIP uris, so we use the same parser.
		var sipUri message.SipUri
		sipUri, err = ParseSipUri(uriStr)
		uri = &sipUri
	default:
		err = fmt.Errorf("unsupported URI schema %s", uriStr[:colonIdx])
	}

	return
}

// ParseSipUri converts a string representation of a SIP or SIPS URI into a SipUri object.
func ParseSipUri(uriStr string) (uri message.SipUri, err error) {
	// Store off the original URI in case we need to print it in an error.
	uriStrCopy := uriStr

	// URI should start 'sip' or 'sips'. Check the first 3 chars.
	if strings.ToLower(uriStr[:3]) != "sip" {
		err = fmt.Errorf("invalid SIP uri protocol name in '%s'", uriStrCopy)
		return
	}
	uriStr = uriStr[3:]

	if strings.ToLower(uriStr[0:1]) == "s" {
		// URI started 'sips', so it's encrypted.
		uri.IsEncrypted = true
		uriStr = uriStr[1:]
	}

	// The 'sip' or 'sips' protocol name should be followed by a ':' character.
	if uriStr[0] != ':' {
		err = fmt.Errorf("no ':' after protocol name in SIP uri '%s'", uriStrCopy)
		return
	}
	uriStr = uriStr[1:]

	// SIP URIs may contain a user-info part, ending in a '@'.
	// This is the only place '@' may occur, so we can use it to check for the
	// existence of a user-info part.
	uri.User = message.NoString{}
	uri.Password = message.NoString{}
	endOfUserInfoPart := strings.Index(uriStr, "@")
	if endOfUserInfoPart != -1 {
		// A user-info part is present. These take the form:
		//     user [ ":" password ] "@"
		endOfUsernamePart := strings.Index(uriStr, ":")
		if endOfUsernamePart > endOfUserInfoPart {
			endOfUsernamePart = -1
		}

		if endOfUsernamePart == -1 {
			// No password component; the whole of the user-info part before
			// the '@' is a username.
			user := uriStr[:endOfUserInfoPart]
			uri.User = message.String{S: user}
		} else {
			user := uriStr[:endOfUsernamePart]
			pwd := uriStr[endOfUsernamePart+1 : endOfUserInfoPart]
			uri.User = message.String{S: user}
			uri.Password = message.String{S: pwd}
		}
		uriStr = uriStr[endOfUserInfoPart+1:]
	}

	// A ';' indicates the beginning of a URI params section, and the end of the URI itself.
	endOfUriPart := strings.Index(uriStr, ";")
	if endOfUriPart == -1 {
		// There are no URI parameters, but there might be header parameters (introduced by '?').
		endOfUriPart = strings.Index(uriStr, "?")
	}
	if endOfUriPart == -1 {
		// There are no parameters at all. The URI ends after the host[:port] part.
		endOfUriPart = len(uriStr)
	}

	uri.Host, uri.Port, err = parseHostPort(uriStr[:endOfUriPart])
	uriStr = uriStr[endOfUriPart:]
	if err != nil {
		return
	} else if len(uriStr) == 0 {
		uri.UriParams = message.NewParams()
		uri.Headers = message.NewParams()
		return
	}

	// Now parse any URI parameters.
	// These are key-value pairs separated by ';'.
	// They end at the end of the URI, or at the start of any URI headers
	// which may be present (denoted by an initial '?').
	var uriParams message.Params
	var n int
	if uriStr[0] == ';' {
		uriParams, n, err = parseParams(uriStr, ';', ';', '?', true, true)
		if err != nil {
			return
		}
	} else {
		uriParams, n = message.NewParams(), 0
	}
	uri.UriParams = uriParams
	uriStr = uriStr[n:]

	// Finally parse any URI headers.
	// These are key-value pairs, starting with a '?' and separated by '&'.
	var headers message.Params
	headers, n, err = parseParams(uriStr, '?', '&', 0, true, false)
	if err != nil {
		return
	}
	uri.Headers = headers
	uriStr = uriStr[n:]
	if len(uriStr) > 0 {
		err = fmt.Errorf("internal error: parse of SIP uri ended early! '%s'",
			uriStrCopy)
		return // Defensive return
	}

	return
}

// Parse a text representation of a host[:port] pair.
// The port may or may not be present, so we represent it with a *uint16,
// and return 'nil' if no port was present.
func parseHostPort(rawText string) (host string, port *uint16, err error) {
	colonIdx := strings.Index(rawText, ":")
	if colonIdx == -1 {
		host = rawText
		return
	}

	// Surely there must be a better way..!
	var portRaw64 uint64
	var portRaw16 uint16
	host = rawText[:colonIdx]
	portRaw64, err = strconv.ParseUint(rawText[colonIdx+1:], 10, 16)
	portRaw16 = uint16(portRaw64)
	port = &portRaw16

	return
}

// General utility method for parsing 'key=value' parameters.
// Takes a string (source), ensures that it begins with the 'start' character provided,
// and then parses successive key/value pairs separated with 'sep',
// until either 'end' is reached or there are no characters remaining.
// A map of keys to values will be returned, along with the number of characters consumed.
// Provide 0 for start or end to indicate that there is no starting/ending delimiter.
// If quoteValues is true, values can be enclosed in double-quotes which will be validated by the
// parser and omitted from the returned map.
// If permitSingletons is true, keys with no values are permitted.
// These will result in a nil value in the returned map.
func parseParams(source string,
	start uint8, sep uint8, end uint8,
	quoteValues bool, permitSingletons bool) (
	params message.Params, consumed int, err error) {

	params = message.NewParams()

	if len(source) == 0 {
		// Key-value section is completely empty; return defaults.
		return
	}

	// Ensure the starting character is correct.
	if start != 0 {
		if source[0] != start {
			err = fmt.Errorf("expected %c at start of key-value section; got %c. section was %s",
				start, source[0], source)
			return
		}
		consumed++
	}

	// Statefully parse the given string one character at a time.
	var buffer bytes.Buffer
	var key string
	parsingKey := true // false implies we are parsing a value
	inQuotes := false
parseLoop:
	for ; consumed < len(source); consumed++ {
		switch source[consumed] {
		case end:
			if inQuotes {
				// We read an end character, but since we're inside quotations we should
				// treat it as a literal part of the value.
				buffer.WriteString(string(end))
				continue
			}

			break parseLoop

		case sep:
			if inQuotes {
				// We read a separator character, but since we're inside quotations
				// we should treat it as a literal part of the value.
				buffer.WriteString(string(sep))
				continue
			}
			if parsingKey && permitSingletons {
				params.Add(buffer.String(), message.NoString{})
			} else if parsingKey {
				err = fmt.Errorf("singleton param '%s' when parsing params which disallow singletons: \"%s\"",
					buffer.String(), source)
				return
			} else {
				value := buffer.String()
				params.Add(key, message.String{value})
			}
			buffer.Reset()
			parsingKey = true

		case '"':
			if !quoteValues {
				// We hit a quote character, but since quoting is turned off we treat it as a literal.
				buffer.WriteString("\"")
				continue
			}

			if parsingKey {
				// Quotes are never allowed in keys.
				err = fmt.Errorf("unexpected '\"' in parameter key in params \"%s\"", source)
				return
			}

			if !inQuotes && buffer.Len() != 0 {
				// We hit an initial quote midway through a value; that's not allowed.
				err = fmt.Errorf("unexpected '\"' in params \"%s\"", source)
				return
			}

			if inQuotes &&
				consumed != len(source)-1 &&
				source[consumed+1] != sep {
				// We hit an end-quote midway through a value; that's not allowed.
				err = fmt.Errorf("unexpected character %c after quoted param in \"%s\"",
					source[consumed+1], source)

				return
			}

			inQuotes = !inQuotes

		case '=':
			if buffer.Len() == 0 {
				err = fmt.Errorf("key of length 0 in params \"%s\"", source)
				return
			}
			if !parsingKey {
				err = fmt.Errorf("unexpected '=' char in value token: \"%s\"", source)
				return
			}
			key = buffer.String()
			buffer.Reset()
			parsingKey = false

		default:
			if !inQuotes && strings.Contains(c_ABNF_WS, string(source[consumed])) {
				// Skip unquoted whitespace.
				continue
			}

			buffer.WriteString(string(source[consumed]))
		}
	}

	// The param string has ended. Check that it ended in a valid place, and then store off the
	// contents of the buffer.
	if inQuotes {
		err = fmt.Errorf("unclosed quotes in parameter string: %s", source)
	} else if parsingKey && permitSingletons {
		params.Add(buffer.String(), message.NoString{})
	} else if parsingKey {
		err = fmt.Errorf("singleton param '%s' when parsing params which disallow singletons: \"%s\"",
			buffer.String(), source)
	} else {
		value := buffer.String()
		params.Add(key, message.String{value})
	}
	return
}

// Parse a header string, producing one or more SipHeader objects.
// (SIP messages containing multiple headers of the same type can express them as a
// single header containing a comma-separated argument list).
func (p *parser) parseHeader(headerText string) (headers []message.SipHeader, err error) {
	p.Log().Debugf("parser %p parsing header \"%s\"", p, headerText)
	headers = make([]message.SipHeader, 0)

	colonIdx := strings.Index(headerText, ":")
	if colonIdx == -1 {
		err = fmt.Errorf("field name with no value in header: %s", headerText)
		return
	}

	fieldName := strings.TrimSpace(headerText[:colonIdx])
	lowerFieldName := strings.ToLower(fieldName)
	fieldText := strings.TrimSpace(headerText[colonIdx+1:])
	if headerParser, ok := p.headerParsers[lowerFieldName]; ok {
		// We have a registered parser for this header type - use it.
		headers, err = headerParser(lowerFieldName, fieldText)
	} else {
		// We have no registered parser for this header type,
		// so we encapsulate the header data in a GenericHeader struct.
		p.Log().Debugf("parser %p has no parser for header type %s", p, fieldName)
		header := message.GenericHeader{HeaderName: fieldName, Contents: fieldText}
		headers = []message.SipHeader{&header}
	}

	return
}

// Parse a To, From or Contact header line, producing one or more logical SipHeaders.
func parseAddressHeader(headerName string, headerText string) (
	headers []message.SipHeader, err error) {
	switch headerName {
	case "to", "from", "contact", "t", "f", "m":
		var displayNames []message.MaybeString
		var uris []message.Uri
		var paramSets []message.Params

		// Perform the actual parsing. The rest of this method is just typeclass bookkeeping.
		displayNames, uris, paramSets, err = parseAddressValues(headerText)

		if err != nil {
			return
		}
		if len(displayNames) != len(uris) || len(uris) != len(paramSets) {
			// This shouldn't happen unless parseAddressValues is bugged.
			err = fmt.Errorf("internal parser error: parsed param mismatch. "+
				"%d display names, %d uris and %d param sets "+
				"in %s",
				len(displayNames), len(uris), len(paramSets),
				headerText)
			return
		}

		// Build a slice of headers of the appropriate kind, populating them with the values parsed above.
		// It is assumed that all headers returned by parseAddressValues are of the same kind,
		// although we do not check for this below.
		for idx := 0; idx < len(displayNames); idx++ {
			var header message.SipHeader
			if headerName == "to" || headerName == "t" {
				if idx > 0 {
					// Only a single To header is permitted in a SIP message.
					return nil,
						fmt.Errorf("multiple to: headers in message:\n%s: %s",
							headerName, headerText)
				}
				switch uris[idx].(type) {
				case message.WildcardUri:
					// The Wildcard '*' URI is only permitted in Contact headers.
					err = fmt.Errorf("wildcard uri not permitted in to: "+
						"header: %s", headerText)
					return
				default:
					toHeader := message.ToHeader{DisplayName: displayNames[idx],
						Address: uris[idx],
						Params:  paramSets[idx]}
					header = &toHeader
				}
			} else if headerName == "from" || headerName == "f" {
				if idx > 0 {
					// Only a single From header is permitted in a SIP message.
					return nil,
						fmt.Errorf("multiple from: headers in message:\n%s: %s",
							headerName, headerText)
				}
				switch uris[idx].(type) {
				case message.WildcardUri:
					// The Wildcard '*' URI is only permitted in Contact headers.
					err = fmt.Errorf("wildcard uri not permitted in from: "+
						"header: %s", headerText)
					return
				default:
					fromHeader := message.FromHeader{DisplayName: displayNames[idx],
						Address: uris[idx],
						Params:  paramSets[idx]}
					header = &fromHeader
				}
			} else if headerName == "contact" || headerName == "m" {
				switch uris[idx].(type) {
				case message.ContactUri:
					if uris[idx].(message.ContactUri).IsWildcard() {
						if paramSets[idx].Length() > 0 {
							// Wildcard headers do not contain parameters.
							err = fmt.Errorf("wildcard contact header should contain no parameters: '%s",
								headerText)
							return
						}
						if _, ok := displayNames[idx].(message.String); ok {
							// Wildcard headers do not contain display names.
							err = fmt.Errorf("wildcard contact header should contain no display name %s",
								headerText)
							return
						}
					}
					contactHeader := message.ContactHeader{DisplayName: displayNames[idx],
						Address: uris[idx].(message.ContactUri),
						Params:  paramSets[idx]}
					header = &contactHeader
				default:
					// URIs in contact headers are restricted to being either SIP URIs or 'Contact: *'.
					return nil,
						fmt.Errorf("uri %s not valid in Contact header. Must be SIP uri or '*'", uris[idx].String())
				}
			}

			headers = append(headers, header)
		}
	}

	return
}

// Parse a string representation of a CSeq header, returning a slice of at most one CSeq.
func parseCSeq(headerName string, headerText string) (
	headers []message.SipHeader, err error) {
	var cseq message.CSeq

	parts := splitByWhitespace(headerText)
	if len(parts) != 2 {
		err = fmt.Errorf("CSeq field should have precisely one whitespace section: '%s'",
			headerText)
		return
	}

	var seqno uint64
	seqno, err = strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return
	}

	if seqno > MAX_CSEQ {
		err = fmt.Errorf("invalid CSeq %d: exceeds maximum permitted value "+
			"2**31 - 1", seqno)
		return
	}

	cseq.SeqNo = uint32(seqno)
	cseq.MethodName = message.Method(strings.TrimSpace(parts[1]))

	if strings.Contains(string(cseq.MethodName), ";") {
		err = fmt.Errorf("unexpected ';' in CSeq body: %s", headerText)
		return
	}

	headers = []message.SipHeader{&cseq}

	return
}

// Parse a string representation of a Call-Id header, returning a slice of at most one CallId.
func parseCallId(headerName string, headerText string) (
	headers []message.SipHeader, err error) {
	headerText = strings.TrimSpace(headerText)
	var callId message.CallId = message.CallId(headerText)

	if strings.ContainsAny(string(callId), c_ABNF_WS) {
		err = fmt.Errorf("unexpected whitespace in CallId header body '%s'", headerText)
		return
	}
	if strings.Contains(string(callId), ";") {
		err = fmt.Errorf("unexpected semicolon in CallId header body '%s'", headerText)
		return
	}
	if len(string(callId)) == 0 {
		err = fmt.Errorf("empty Call-Id body")
		return
	}

	headers = []message.SipHeader{&callId}

	return
}

// Parse a string representation of a Via header, returning a slice of at most one ViaHeader.
// Note that although Via headers may contain a comma-separated list, RFC 3261 makes it clear that
// these should not be treated as separate logical Via headers, but as multiple values on a single
// Via header.
func parseViaHeader(headerName string, headerText string) (
	headers []message.SipHeader, err error) {
	sections := strings.Split(headerText, ",")
	var via message.ViaHeader = message.ViaHeader{}
	for _, section := range sections {
		var hop message.ViaHop
		parts := strings.Split(section, "/")

		if len(parts) < 3 {
			err = fmt.Errorf("not enough protocol parts in via header: '%s'",
				parts)
			return
		}

		parts[2] = strings.Join(parts[2:], "/")

		// The transport part ends when whitespace is reached, but may also start with
		// whitespace.
		// So the end of the transport part is the first whitespace char following the
		// first non-whitespace char.
		initialSpaces := len(parts[2]) - len(strings.TrimLeft(parts[2], c_ABNF_WS))
		sentByIdx := strings.IndexAny(parts[2][initialSpaces:], c_ABNF_WS) + initialSpaces + 1
		if sentByIdx == 0 {
			err = fmt.Errorf("expected whitespace after sent-protocol part "+
				"in via header '%s'", section)
			return
		} else if sentByIdx == 1 {
			err = fmt.Errorf("empty transport field in via header '%s'", section)
			return
		}

		hop.ProtocolName = strings.TrimSpace(parts[0])
		hop.ProtocolVersion = strings.TrimSpace(parts[1])
		hop.Transport = strings.TrimSpace(parts[2][:sentByIdx-1])

		if len(hop.ProtocolName) == 0 {
			err = fmt.Errorf("no protocol name provided in via header '%s'", section)
		} else if len(hop.ProtocolVersion) == 0 {
			err = fmt.Errorf("no version provided in via header '%s'", section)
		} else if len(hop.Transport) == 0 {
			err = fmt.Errorf("no transport provided in via header '%s'", section)
		}
		if err != nil {
			return
		}

		viaBody := parts[2][sentByIdx:]

		paramsIdx := strings.Index(viaBody, ";")
		var host string
		var port *uint16
		if paramsIdx == -1 {
			// There are no header parameters, so the rest of the Via body is part of the host[:post].
			host, port, err = parseHostPort(viaBody)
			hop.Host = host
			hop.Port = port
			if err != nil {
				return
			}
			hop.Params = message.NewParams()
		} else {
			host, port, err = parseHostPort(viaBody[:paramsIdx])
			if err != nil {
				return
			}
			hop.Host = host
			hop.Port = port

			hop.Params, _, err = parseParams(viaBody[paramsIdx:],
				';', ';', 0, true, true)
		}
		via = append(via, &hop)
	}

	headers = []message.SipHeader{&via}
	return
}

// Parse a string representation of a Max-Forwards header into a slice of at most one MaxForwards header object.
func parseMaxForwards(headerName string, headerText string) (
	headers []message.SipHeader, err error) {
	var maxForwards message.MaxForwards
	var value uint64
	value, err = strconv.ParseUint(strings.TrimSpace(headerText), 10, 32)
	maxForwards = message.MaxForwards(value)

	headers = []message.SipHeader{&maxForwards}
	return
}

// Parse a string representation of a Content-Length header into a slice of at most one ContentLength header object.
func parseContentLength(headerName string, headerText string) (
	headers []message.SipHeader, err error) {
	var contentLength message.ContentLength
	var value uint64
	value, err = strconv.ParseUint(strings.TrimSpace(headerText), 10, 32)
	contentLength = message.ContentLength(value)

	headers = []message.SipHeader{&contentLength}
	return
}

// parseAddressValues parses a comma-separated list of addresses, returning
// any display names and header params, as well as the SIP URIs themselves.
// parseAddressValues is aware of < > bracketing and quoting, and will not
// break on commas within these structures.
func parseAddressValues(addresses string) (
	displayNames []message.MaybeString, uris []message.Uri,
	headerParams []message.Params, err error) {

	prevIdx := 0
	inBrackets := false
	inQuotes := false

	// Append a comma to simplify the parsing code; we split address sections
	// on commas, so use a comma to signify the end of the final address section.
	addresses = addresses + ","

	for idx, char := range addresses {
		if char == '<' && !inQuotes {
			inBrackets = true
		} else if char == '>' && !inQuotes {
			inBrackets = false
		} else if char == '"' {
			inQuotes = !inQuotes
		} else if !inQuotes && !inBrackets && char == ',' {
			var displayName message.MaybeString
			var uri message.Uri
			var params message.Params
			displayName, uri, params, err =
				parseAddressValue(addresses[prevIdx:idx])
			if err != nil {
				return
			}
			prevIdx = idx + 1

			displayNames = append(displayNames, displayName)
			uris = append(uris, uri)
			headerParams = append(headerParams, params)
		}
	}

	return
}

// parseAddressValue parses an address - such as from a From, To, or
// Contact header. It returns:
//   - a MaybeString containing the display name (or not)
//   - a parsed SipUri object
//   - a map containing any header parameters present
//   - the error object
// See RFC 3261 section 20.10 for details on parsing an address.
// Note that this method will not accept a comma-separated list of addresses;
// addresses in that form should be handled by parseAddressValues.
func parseAddressValue(addressText string) (
	displayName message.MaybeString, uri message.Uri,
	headerParams message.Params, err error) {

	headerParams = message.NewParams()

	if len(addressText) == 0 {
		err = fmt.Errorf("address-type header has empty body")
		return
	}

	addressTextCopy := addressText
	addressText = strings.TrimSpace(addressText)

	firstAngleBracket := findUnescaped(addressText, '<', quotes_delim)
	displayName = message.NoString{}
	if firstAngleBracket > 0 {
		// We have an angle bracket, and it's not the first character.
		// Since we have just trimmed whitespace, this means there must
		// be a display name.
		if addressText[0] == '"' {
			// The display name is within quotations.
			// So it is comprised of all text until the closing quote.
			addressText = addressText[1:]
			nextQuote := strings.Index(addressText, "\"")

			if nextQuote == -1 {
				// Unclosed quotes - parse error.
				err = fmt.Errorf("unclosed quotes in header text: %s",
					addressTextCopy)
				return
			}

			nameField := addressText[:nextQuote]
			displayName = message.String{S: nameField}
			addressText = addressText[nextQuote+1:]
		} else {
			// The display name is unquoted, so it is comprised of
			// all text until the opening angle bracket, except surrounding whitespace.
			// According to the ABNF grammar: display-name   =  *(token LWS)/ quoted-string
			// there are certain characters the display name cannot contain unless it's quoted,
			// however we don't check for them here since it doesn't impact parsing.
			// May as well be lenient.
			nameField := addressText[:firstAngleBracket]
			displayName = message.String{S: strings.TrimSpace(nameField)}
			addressText = addressText[firstAngleBracket:]
		}
	}

	// Work out where the SIP URI starts and ends.
	addressText = strings.TrimSpace(addressText)
	var endOfUri int
	var startOfParams int
	if addressText[0] != '<' {
		switch displayName.(type) {
		case message.String:
			// The address must be in <angle brackets> if a display name is
			// present, so this is an invalid address line.
			err = fmt.Errorf("invalid character '%c' following display "+
				"name in address line; expected '<': %s",
				addressText[0], addressTextCopy)
			return
		}

		endOfUri = strings.Index(addressText, ";")
		if endOfUri == -1 {
			endOfUri = len(addressText)
		}
		startOfParams = endOfUri

	} else {
		addressText = addressText[1:]
		endOfUri = strings.Index(addressText, ">")
		if endOfUri == 0 {
			err = fmt.Errorf("'<' without closing '>' in address %s",
				addressTextCopy)
			return
		}
		startOfParams = endOfUri + 1

	}

	// Now parse the SIP URI.
	uri, err = ParseUri(addressText[:endOfUri])
	if err != nil {
		return
	}

	if startOfParams >= len(addressText) {
		return
	}

	// Finally, parse any header parameters and then return.
	addressText = addressText[startOfParams:]
	headerParams, _, err = parseParams(addressText, ';', ';', ',', true, true)
	return
}

// Extract the next logical header line from the message.
// This may run over several actual lines; lines that start with whitespace are
// a continuation of the previous line.
// Therefore also return how many lines we consumed so the parent parser can
// keep track of progress through the message.
func getNextHeaderLine(contents []string) (headerText string, consumed int) {
	if len(contents) == 0 {
		return
	}
	if len(contents[0]) == 0 {
		return
	}

	var buffer bytes.Buffer
	buffer.WriteString(contents[0])

	for consumed = 1; consumed < len(contents); consumed++ {
		firstChar, _ := utf8.DecodeRuneInString(contents[consumed])
		if !unicode.IsSpace(firstChar) {
			break
		} else if len(contents[consumed]) == 0 {
			break
		}

		buffer.WriteString(" " + strings.TrimSpace(contents[consumed]))
	}

	headerText = buffer.String()
	return
}

// A delimiter is any pair of characters used for quoting text (i.e. bulk escaping literals).
type delimiter struct {
	start uint8
	end   uint8
}

// Define common quote characters needed in parsing.
var quotes_delim = delimiter{'"', '"'}

var angles_delim = delimiter{'<', '>'}

// Find the first instance of the target in the given text which is not enclosed in any delimiters
// from the list provided.
func findUnescaped(text string, target uint8, delims ...delimiter) int {
	return findAnyUnescaped(text, string(target), delims...)
}

// Find the first instance of any of the targets in the given text that are not enclosed in any delimiters
// from the list provided.
func findAnyUnescaped(text string, targets string, delims ...delimiter) int {
	escaped := false
	var endEscape uint8 = 0

	endChars := make(map[uint8]uint8)
	for _, delim := range delims {
		endChars[delim.start] = delim.end
	}

	for idx := 0; idx < len(text); idx++ {
		if !escaped && strings.Contains(targets, string(text[idx])) {
			return idx
		}

		if escaped {
			escaped = text[idx] != endEscape
			continue
		} else {
			endEscape, escaped = endChars[text[idx]]
		}
	}

	return -1
}

// Splits the given string into sections, separated by one or more characters
// from c_ABNF_WS.
func splitByWhitespace(text string) []string {
	var buffer bytes.Buffer
	var inString bool = true
	result := make([]string, 0)

	for _, char := range text {
		s := string(char)
		if strings.Contains(c_ABNF_WS, s) {
			if inString {
				// First whitespace char following text; flush buffer to the results array.
				result = append(result, buffer.String())
				buffer.Reset()
			}
			inString = false
		} else {
			buffer.WriteString(s)
			inString = true
		}
	}

	if buffer.Len() > 0 {
		result = append(result, buffer.String())
	}

	return result
}
