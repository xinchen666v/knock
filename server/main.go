package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	queueTTL   = 7 * 24 * time.Hour
	sweepEvery = 1 * time.Hour
	ackGrace   = 24 * time.Hour
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

	go sweepLoop(store)

	log.Printf("knock server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server exited: %v", err)
	}

}
func sweepLoop(store *Store) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for range t.C {
		nq, nm, na, err := store.Sweep(time.Now().Unix(),
			int64(queueTTL.Seconds()), int64(ackGrace.Seconds()))
		if err != nil {
			slog.Error("sweep failed", "err", err)
			continue
		}
		if nq > 0 || nm > 0 {
			slog.Info("sweep done", "queues", nq, "orphaned_msgs", nm, "acked_residue", na)
		}
	}
}
