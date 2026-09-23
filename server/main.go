package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	store, err := Open("./database/knock.db")
	if err != nil {
		slog.Error("open store failed", "err", err)
		os.Exit(1)
	}
	defer store.Close()
	slog.Info("store ready", "path", "knock.db")

	addr := ":8080"
	hub := NewHub(store)
	go hub.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWS(hub, w, r)
	})

	log.Printf("knock server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
