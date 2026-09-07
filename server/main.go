package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
)

func main(){
	logger := slog.New(slog.NewTextHandler(os.Stdout,nil))
	slog.SetDefault(logger)

	addr:= ":8080"
	hub := NewHub()
	go hub.Run()

	mux:=http.NewServeMux()
	mux.HandleFunc("/ws",func(w http.ResponseWriter,r*http.Request){
		serveWS(hub,w,r)
	})

	log.Printf("knock server listening on %s",addr)
	if err:=http.ListenAndServe(addr,mux);err!=nil{
		log.Fatalf("server exited: %v",err)
	}
}