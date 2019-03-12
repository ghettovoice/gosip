package gosip

// Version is the current gosip package version
var Version = "0.0.0"

var (
	defaultServer *Server
)

// DefaultServer returns auto created default SIP server
func DefaultServer() *Server {
	return defaultServer
}

// Listen starts SIP stack
func Listen(network string, listenAddr string) error {
	if defaultServer == nil {
		defaultServer = NewServer(nil)
	}

	return defaultServer.Listen(network, listenAddr)
}
