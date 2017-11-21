package transp

type Udp struct {
	transport
}

func NewUdp() *Udp {
	return &Udp{}
}
