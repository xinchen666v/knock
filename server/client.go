package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xinchen666v/knock/protocol"
)

const (
	writeWait  = 10 * time.Second // 单次写操作的超时
	pongWait   = 60 * time.Second // 允许对端多久不吭声
	pingPeriod = 45 * time.Second // 自己发 ping 的间隔（必须小于 pongWait）
	maxMsgSize = 64 * 1024        // 单条消息上限 64KB
)

// Client 代表一条活着的连接
type Client struct {
	conn *websocket.Conn
	send chan []byte // 发送队列：任何人想给这个客户端发消息，往这里丢
}

func serveWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("upgrade failed", "err", err)
		return
	}
	client := &Client{
		conn: conn,
		send: make(chan []byte, 16),
	}
	hub.Register(client) // 登记到 hub（今天的 hub 只是记个数）

	go client.writePump()
	go client.readPump(hub)
	slog.Info("client connected", "remote", conn.RemoteAddr())
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// readPump：从连接读消息，直到出错或对端关闭
func (c *Client) readPump(hub *Hub) {
	defer func() {
		hub.Unregister(c)
		c.conn.Close()
		slog.Info("client disconnected", "remote", c.conn.RemoteAddr())
	}()

	c.conn.SetReadLimit(maxMsgSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		// 对端回了 pong，续命
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage() // 阻塞等下一条消息
		if err != nil {
			return // 任何错误（超时/断开/格式错）都终止读循环
		}

		env, err := protocol.Parse(data)
		if err != nil {
			// 这是我们在协议层埋好的价值时刻：格式不对，回 ERROR 并继续
			c.SendError(protocol.CodeBadRequest, err.Error())
			continue // 注意：坏消息不断连接，只报错
		}
		slog.Info("msg received", "type", env.Type, "qid", env.QueueID)

		// 今天先做最简分发：回一个 PONG，证明双向通了
		switch env.Type {
		case protocol.TypePing:
			c.SendEnvelope(protocol.TypePong,"","",nil) //就地处理
		default:
			hub.Route(c,env) //其他命令排队交给hub
		}
	}
}

// writePump：从 send channel 取消息写出，顺带定时 ping
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// hub 关闭了这个 channel：通知对端再见，然后退出
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return // 写失败说明连接已死，readPump 那边也会很快收到错误
			}
		case <-ticker.C:
			// 定时心跳：主动探活
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// --- 发送辅助方法（唯一允许往 conn 写东西的地方） ---

func (c *Client) SendPong() {
	// env, _ := protocol.NewEnvelope(protocol.TypePong, "", "", nil)
	// if data, err := json.Marshal(env); err == nil {
	// 	select {
	// 	case c.send <- data:
	// 	default: // 队列满了说明写不动了，丢消息保连接
	// 	}
	// }
	c.SendEnvelope(protocol.TypePong,"","",nil)
}

func (c *Client) SendError(code int, msg string) {
	// env, _ := protocol.NewEnvelope(protocol.TypeError, "", "",
	// 	protocol.ErrorMessage{Code: code, Message: msg})
	// if data, err := json.Marshal(env); err == nil {
	// 	select {
	// 	case c.send <- data:
	// 	default:
	// 	}
	// }
	c.SendEnvelope(protocol.TypeError,"","",
			protocol.ErrorMessage{Code: code, Message: msg})		
}


//所有发送的统一入口
func (c *Client) SendEnvelope(msgType,qid,mid string,payload any) {
	env,err := protocol.NewEnvelope(msgType,qid,mid,payload)
	if err != nil {
		return 
	}
	data,err := json.Marshal(env)
	if err != nil {
		return 
	}
	select {
	case c.send <- data:
	default: //hub不阻塞，满了就丢
	}
}
