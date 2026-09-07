package main

import (
	"log/slog"

	"github.com/xinchen666v/knock/protocol"
)

// routed 是一次“待处理的消息”：谁发的 + 内容是什么
type routed struct {
	from *Client
	env  *protocol.Envelope
}

// Hub 管理连接注册表和队列路由表。
// 所有状态只被 Run 这个 goroutine 触碰——没有锁。
type Hub struct {
	register   chan *Client
	unregister chan *Client
	route      chan routed

	clients map[*Client]bool   // 所有的活着的连接
	queues  map[string]*Client // 队列路由表：qid -> 订阅者
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		route:      make(chan routed),
		clients:    make(map[*Client]bool),
		queues:     make(map[string]*Client),
	}
}

func (h *Hub) Register(c *Client)   { h.register <- c }
func (h *Hub) Unregister(c *Client) { h.unregister <- c }
func (h *Hub) Route(from *Client, env *protocol.Envelope) {
	h.route <- routed{from: from, env: env}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
			slog.Info("hub: registered", "total", len(h.clients))
		case c := <-h.unregister:
			// 存在性检查是防双 close 的关键：
			// 被踢的连接已经在这里被删过了，它迟到的 unregister 会安全跳过
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
				h.releaseQueue(c)
				slog.Info("hub: unregistered", "total", len(h.clients))
			}
		case r := <-h.route:
			h.handle(r.from, r.env)
		}
	}
}

// releaseQueues：断开的连接如果占着队列，释放它们
func (h *Hub) releaseQueue(c *Client) {
	for qid, sub := range h.queues {
		if sub == c {
			delete(h.queues, qid)
			slog.Info("hub: queue released", "qid", qid[:8]+"...")
		}
	}
}

// handle：根据命令类型路由。只被 Run goroutine 调用
func (h *Hub) handle(from *Client, env *protocol.Envelope) {
	switch env.Type {
	case protocol.TypeAck:
	}
}
