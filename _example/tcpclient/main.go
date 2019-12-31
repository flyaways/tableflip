package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/flyaways/tableflip"
)

var (
	listenAddr = flag.String("listen", "localhost:8080", "`Address` to listen on")
	pidFile    = flag.String("pid-file", "", "`Path` to pid file")
)

// This shows how to use the Upgrader
// with a listener based service.
func main() {
	flag.Parse()
	log.SetPrefix(fmt.Sprintf("%d ", os.Getpid()))

	upg, err := tableflip.New(tableflip.Options{
		PIDFile: *pidFile,
	})
	if err != nil {
		panic(err)
	}

	// Do an upgrade on SIGHUP
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGHUP)
		for range sig {
			err := upg.Upgrade()
			if err != nil {
				log.Println("upgrade failed:", err)
			}
		}
	}()

	go func() {
		upg.ConnectTCP("tcp", "addr", "connid", NewConn)
	}()

	log.Printf("ready")
	if err := upg.Ready(); err != nil {
		panic(err)
	}
	<-upg.Exit()
}

func NewConn(string, string) (net.Conn, uintptr, error) {

	return nil, 0, nil
}
