// Package testutil provides test utilities.
package testutil

//go:generate errtrace -w .
//go:generate mockgen -typed -package=netmock -destination=netmock/net.go net Listener,PacketConn,Conn
