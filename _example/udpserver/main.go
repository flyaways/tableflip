package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	ln, err := upg.ListenUDP("udp", *listenAddr)
	if err != nil {
		log.Fatalln("Can't listen:", err)
	}

	listener, ok := ln.(*net.UDPConn)
	if !ok {
		log.Fatalln("listener is not UDPConn ", err)
	}

	defer ln.Close()

	go func() {
		defer ln.Close()

		log.Printf("listening on udp %s\n", listener.LocalAddr())

		for {
			data := make([]byte, 1024)
			n, remoteAddr, err := listener.ReadFromUDP(data)
			if err != nil {
				return
			}

			log.Println(n, remoteAddr)

			b := make([]byte, 4)
			daytime := time.Now().Unix()
			binary.BigEndian.PutUint32(b, uint32(daytime))

			if _, err = listener.WriteToUDP(b, remoteAddr); err != nil {
				log.Println("failed to write UDP ", err.Error())
			}
		}
	}()

	log.Println("ready udp")

	if err := upg.Ready(); err != nil {
		panic(err)
	}

	<-upg.Exit()
}
