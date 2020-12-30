package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/ghettovoice/gosip"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/transport"
)

var (
	logger log.Logger
)

func init() {
	logger = log.NewDefaultLogrusLogger().WithPrefix("Server")
}

func main() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	srvConf := gosip.ServerConfig{}
	srv := gosip.NewServer(srvConf, nil, nil, logger)
	srv.Listen("wss", "0.0.0.0:5081", &transport.Options{CertFile: "certs/cert.pem", KeyFile: "certs/key.pem"})

	<-stop

	srv.Shutdown()
}
