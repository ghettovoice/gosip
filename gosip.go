package gosip

// Version is the current gosip package version
var Version = "0.0.0"

var (
	defaultServer *Server
)

// Listen starts SIP stack
func Listen(listenAddr string) error {
	if defaultServer == nil {
		defaultServer = NewServer(ServerConfig{})
	}

	return defaultServer.Listen(listenAddr)
}
