package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := listenAddr()
	srv := newServer()
	log.Printf("taskflow-api listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.routes()); err != nil {
		log.Fatal(err)
	}
}

func listenAddr() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return ":" + port
}
