// Package testutil provides test utilities.
package testutil

//go:generate go tool errtrace -w .
//go:generate go tool mockgen -typed -package=netmock -destination=netmock/net.go net Listener,PacketConn,Conn
