package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xinchen666v/knock/protocol"
)

// 终端输出的两个原子片段。
// clearLine 回到行首并清空整行，是"重绘"的前提：
// stdinLoop 和 readPump 谁先画提示符都无所谓，后画的那个会把先画的擦掉重来，
// 所以屏幕上永远只有一个 "> "，不会出现 "> > "
const (
	prompt    = "> "
	clearLine = "\r\033[K"
)

// readPump: 网络 → 终端。收到 DELIVER 就打印，PING/PONG 就地处理
func (s *Session) readPump() {
	defer func() {
		s.conn.Close()
		fmt.Printf("%s[链接关闭，回车退出]\n", clearLine)
	}()

	s.conn.SetReadLimit(64 * 1024)
	s.conn.SetReadDeadline(time.Now().Add(pongWait))
	s.conn.SetPongHandler(func(string) error {
		s.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		env, err := protocol.Parse(data)
		if err != nil {
			continue
		}
		switch env.Type {
		case protocol.TypeDeliver:
			// ① 去重：ACK 丢失时服务器会重投，已显示过的直接跳过
			if s.seen[env.MsgID] {
				continue
			}
			s.seen[env.MsgID] = true

			var chat protocol.ChatMessage
			if err := env.DecodePayload(&chat); err != nil {
				continue
			}
			// 一条 Printf 写完"清行 + 消息 + 换行 + 提示符"：
			// 既只有一次 Write（不会和别的输出交错），也保证只画一个提示符
			fmt.Printf("%s[%s] %s\n%s", clearLine, s.nick, chat.Text, prompt)

			// ② 签收：必须走 s.send（写泵是唯一合法写者），必须阻塞发送——
			//    ACK 丢了服务器就永远重投，宁等勿丢
			ack,_ := protocol.NewEnvelope(protocol.TypeAck,env.QueueID,env.MsgID,protocol.AckMessage{MsgID:env.MsgID})
			if data,err := json.Marshal(ack);err == nil{
				s.send <- data
			}

		case protocol.TypeError:
			var em protocol.ErrorMessage
			if err := env.DecodePayload(&em); err == nil {
				fmt.Printf("%s[错误 %d] %s\n%s", clearLine, em.Code, em.Message, prompt)
			}
		default:
			// 其他类型忽略
		}
	}
}

// writePump: 和服务器的版本同构——send channel 的唯一消费者
func (s *Session) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		s.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-s.send:
			s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				return
			}
			if err := s.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// stdinLoop: 键盘 → 网络。主 goroutine 阻塞在这里
func (s *Session) stdinLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print(clearLine + prompt)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			fmt.Print(clearLine + prompt)
			continue
		}
		if text == "/quit" {
			s.conn.Close()
			return
		}
		mid, _ := protocol.NewID()
		env, _ := protocol.NewEnvelope(protocol.TypeSend, s.outbox, mid,
			protocol.ChatMessage{Text: text})
		if data, err := json.Marshal(env); err == nil {
			select {
			case s.send <- data:
			default:
				fmt.Println("[发送队列已满，消息被丢弃]")
			}
		}
		fmt.Print(clearLine + prompt)
	}
}
