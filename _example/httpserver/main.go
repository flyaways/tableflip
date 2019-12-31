package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
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

// This shows how to use the upgrader
// with the graceful shutdown facilities of net/http.
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
				log.Println("Upgrade failed:", err)
			}
		}
	}()

	ln, err := upg.ListenTCP("tcp", *listenAddr)
	if err != nil {
		log.Fatalln("Can't listen:", err)
	}

	srv := http.Server{}

	go func() {
		err := srv.Serve(ln)
		if err != http.ErrServerClosed {
			log.Println("HTTP server:", err)
		}
	}()

	if err := upg.Ready(); err != nil {
		return
	}

	<-upg.Exit()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server Shutdown: ", err)
	}

	log.Println("Server exiting")
}
