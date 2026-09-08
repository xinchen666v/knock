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

// readPump: 网络 → 终端。收到 DELIVER 就打印，PING/PONG 就地处理
func (s *Session) readPump() {
	defer func(){
		s.conn.Close()
		fmt.Println("\n[链接关闭，回车退出]")
	}()

	s.conn.SetReadLimit(64*1024)
	s.conn.SetReadDeadline(time.Now().Add(pongWait))
	s.conn.SetPongHandler(func(string) error{
		s.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for{
		_,data,err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		env,err := protocol.Parse(data)
		if err != nil {
			continue
		}
		switch env.Type {
		case protocol.TypeDeliver:
			var chat protocol.ChatMessage
			if err := env.DecodePayload(&chat); err != nil {
				continue
			}
			fmt.Printf("\r\033[K[%s] %s\n> ", s.nick, chat.Text) // \033[K 清掉当前输入行，体验细节
			fmt.Print("> ")
		default:
			// 其他类型忽略
		}
	}
}

// writePump: 和服务器的版本同构——send channel 的唯一消费者
func (s *Session) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func(){
		ticker.Stop()
		s.conn.Close()
	}()
	for {
		select {
		case msg,ok := <-s.send:
			s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				return
			}
			if err := s.conn.WriteMessage(websocket.TextMessage,msg);err != nil {
				return
			}
		case <-ticker.C:
			s.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := s.conn.WriteMessage(websocket.PingMessage,nil);err != nil {
				return
			}
		}
	}
}

// stdinLoop: 键盘 → 网络。主 goroutine 阻塞在这里
func (s *Session) stdinLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	prompt := "> "
	fmt.Print(prompt)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			fmt.Print(prompt)
			continue
		}
		if text == "/quit" {
			s.conn.Close()
			return
		}
		mid, _ := protocol.NewID()
		env, _ := protocol.NewEnvelope(protocol.TypeSend, s.peerQID, mid,
			protocol.ChatMessage{Text: text})
		if data, err := json.Marshal(env); err == nil {
			select {
			case s.send <- data:
			default:
				fmt.Println("[发送队列已满，消息被丢弃]")
			}
		}
		fmt.Print(prompt)
	}
}