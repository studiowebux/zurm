package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/studiowebux/zurm/zserver"
)

func defaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/zurm-server.sock"
	}
	return filepath.Join(home, ".config", "zurm", "server.sock")
}

func main() {
	socket := flag.String("socket", defaultSocketPath(), "Unix socket path")
	flag.Parse()

	dir := filepath.Dir(*socket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatalf("zurm-server: mkdir %s: %v", dir, err)
	}

	srv, err := zserver.NewServer(*socket)
	if err != nil {
		log.Fatalf("zurm-server: listen %s: %v", *socket, err)
	}
	log.Printf("zurm-server: listening on %s", *socket)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("zurm-server: shutting down")
		srv.Close()
		os.Remove(*socket)
		os.Exit(0)
	}()

	srv.Serve()
}
