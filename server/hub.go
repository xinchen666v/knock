package main

import (
	"log/slog"
)


// HUB管理所有活连接
type Hub struct {
	register chan *Client
	unregister chan *Client
	clients map[*Client]bool
}

func NewHub() *Hub{
	return &Hub{
		register: make(chan *Client),
		unregister: make(chan *Client),
		clients: make(map[*Client]bool),
	}
}

func (h *Hub)Register(c *Client) {h.register <- c}
func (h *Hub)Unregister(c *Client) {h.unregister <- c}

func(h *Hub)Run(){
	for{
		select{
		case c := <-h.register:
			h.clients[c] = true
			slog.Info("hub: registered", "total", len(h.clients))
		case c := <-h.unregister:
			if _,ok := h.clients[c];ok{
				delete(h.clients,c)
				close(c.send)
				slog.Info("hub: unregistered", "total", len(h.clients))
			}

		}
	}
}