package main

import (
	"encoding/json"
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
	case protocol.TypeNew:
		// 创建队列：分配随机qid,创建者成为队列主人
		qid,err := protocol.NewID()
		if err != nil {
			from.SendError(500,"internal error")
			return
		}
		h.queues[qid] = from
		from.SendEnvelope(protocol.TypeNewOk,qid,"",
			protocol.NewOkMessage{QueueID: qid})
		slog.Info("hub: queue created","qid",qid[:8]+"...")
	
	case protocol.TypeSub:
		qid := env.QueueID
		if !protocol.ValidQueueID(qid) {
			from.SendError(protocol.CodeBadRequest,"invalid queue id")
			return
		}
		//不存在的qid回404，不假装成功
		if _,ok := h.queues[qid]; !ok {
			from.SendError(protocol.CodeQueueNotFound,"queue not found")
			return
		}

		//单订阅者，踢掉旧的
		if old,ok := h.queues[qid];ok && old != from {
			h.queues[qid] = from
			slog.Info("hub: subscriber replaced","qid",qid[:8]+"...")
			h.kick(old)
		} else {
			h.queues[qid] = from
		}
		from.SendEnvelope(protocol.TypeSubOk,qid,"",nil)
		slog.Info("hub: subscriber","qid",qid[:8]+"...")

	case protocol.TypeSend:
		qid := env.QueueID
		if !protocol.ValidQueueID(qid){
			from.SendError(protocol.CodeBadRequest,"invalid queue id")
			return
		}
		sub,ok := h.queues[qid]
		if !ok {
			from.SendError(protocol.CodeQueueNotFound,"queue not found")
			return
		}
		mid := env.MsgID
		if mid == ""{
			// 客户端没有mid就在服务器补一个，去重要用到这个
			mid,_ =protocol.NewID()
		}
		// 关键：服务器重建信封，只透传payload。客户端无权伪造信封字段
		sub.SendEnvelope(protocol.TypeDeliver,qid,mid,json.RawMessage(env.Payload))
		slog.Info("hub: delivered","qid",qid[:8]+"...","mid",mid[:8]+"...")
	
	case protocol.TypeAck:
		// M1：无持久化，无物可删，仅记录。M2 加SQLite后这里触发 DELETE
		slog.Info("hub: acked","qid",env.QueueID[:8]+"...","mid",env.MsgID[:8]+"...")
	
	default:
		from.SendError(protocol.CodeBadRequest, "unknown command: "+env.Type)

	}
}

// kick 踢掉一个连接。注意它和 unregister 的配合：
// 1. 先从 clients 删除并 close(send) —— writePump 检测到关闭，发 Close 帧后退出
// 2. writePump 退出时 close(conn) → readPump 读到错误 → 调 Unregister
// 3. 那个迟到的 Unregister 发现 c 已不在 map 里 → 安全跳过，不会二次 close
// 整条死亡链没有任何锁、任何竞态，全靠 channel 的所有权规则保证
func (h *Hub) kick(c *Client) {
	if _,ok := h.clients[c]; !ok{
		return //连接已死，不踢
	}
	delete(h.clients,c)
	close(c.send)
}
