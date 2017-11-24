package transport

// TCP stdProtocol implementation
type tcpProtocol struct {
}

func (tcp *tcpProtocol) Name() string {
	return "TCP"
}

func (tcp *tcpProtocol) IsReliable() bool {
	return true
}
