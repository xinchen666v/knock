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

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 45 * time.Second
)

type Session struct {
	conn   *websocket.Conn
	send   chan []byte //发送队列（写泵消费）
	outbox string      // 我的队列：我往里 SEND，对方订阅后从这里收
	inbox  string      // 对方的队列：我订阅它，我的 DELIVER 从这里来

	// 广播地址：双方共同的服务器，从对方的邀请链接里继承
	advHost string
	advPort int

	nick string //对方链接的host，用来区分聊天中谁在说话
}

func dial(peer *protocol.Invite) (*websocket.Conn, error) {
	host, port := "localhost", 8080
	if peer != nil {
		host, port = peer.Host, peer.Port
	}
	url := buildWSURL(host, port)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("连接服务器失败：%w", err)
	}
	return conn, nil
}

// request 串行地发一条命令并等待指定应答（启动阶段专用）
func (s *Session) request(env *protocol.Envelope, wantType string) (*protocol.Envelope, error) {
	data, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("发送%s请求失败: %w", env.Type, err)
	}
	// 启动阶段没有并发，直接在当前 goroutine 读，直到等到想要的应答
	// （中间可能混入服务器的 DELIVER/PONG，跳过即可）
	deadline := time.Now().Add(5 * time.Second)
	s.conn.SetReadDeadline(deadline)
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("等待%s应答失败: %w", env.Type, err)
		}
		resp, err := protocol.Parse(data)
		if err != nil {
			continue //坏帧跳过
		}
		if resp.Type == protocol.TypeError {
			var em protocol.ErrorMessage
			resp.DecodePayload(&em)
			return nil, fmt.Errorf("服务器拒绝（%d):%s", em.Code, em.Message)
		}
		if resp.Type == wantType {
			return resp, nil
		}
		// 不是想要的类型（如心跳），继续等
	}
}

// runChat: ①连接 ②建自己队列 ③订阅对方队列 ④进入聊天循环
func runChat(peerLink string) error {

	// peer, err := protocol.ParseInvite(peerLink)
	// if err != nil {
	// 	return fmt.Errorf("邀请链接不合法: %w", err)
	// }
	// conn, err := dial(peer.Host,peer.Port)
	// if err != nil {
	// 	return err
	// }
	// s := &Session{
	// 	conn:    conn,
	// 	send:    make(chan []byte, 16),
	// 	advHost: peer.Host,
	// 	advPort: peer.Port,
	// 	nick:    peer.Host,
	// }
	// // --- 启动阶段：串行握手 ---
	// // ② 创建自己的收件队列
	// myQID, err := protocol.NewID()
	// if err != nil {
	// 	return err
	// }
	// env, _ := protocol.NewEnvelope(protocol.TypeNew, myQID, "", nil)
	// resp, err := s.request(env, protocol.TypeNewOk)
	// if err != nil {
	// 	return err
	// }
	// var okMsg protocol.NewOkMessage
	// resp.DecodePayload(&okMsg)
	// s.myQID = okMsg.QueueID // 以服务器分配为准

	// // ③ 订阅对方的队列
	// env, _ = protocol.NewEnvelope(protocol.TypeSub, peer.QueueID, "", nil)
	// if _, err := s.request(env, protocol.TypeSubOk); err != nil {
	// 	return err
	// }
	// s.peerQID = peer.QueueID

	// fmt.Printf("═══ knock ═══\n")
	// fmt.Printf("你的邀请链接（发给对方）:\n  %s\n", s.myInvite())
	// fmt.Printf("已连接到 %s 的队列，开始聊天。直接输入文字回车发送，Ctrl+C 退出。\n", s.nick)
	// fmt.Printf("════════════════════════\n")

	// // --- 聊天阶段：并发 ---
	// go s.writePump()
	// go s.readPump()
	// s.stdinLoop() // 主 goroutine 读键盘

	// return nil

	var peer *protocol.Invite
	if peerLink != "" {
		p, err := protocol.ParseInvite(peerLink)
		if err != nil {
			return fmt.Errorf("邀请链接不合法: %w", err)
		}
		peer = p
	}

	conn, err := dial(peer) // 见下方 dial 的调整
	if err != nil {
		return err
	}

	// 广播地址：有对方链接就继承；没有就用默认 localhost:8080
	advHost, advPort := "localhost", 8080
	if peer != nil {
		advHost, advPort = peer.Host, peer.Port
	}

	s := &Session{
		conn:    conn,
		send:    make(chan []byte, 16),
		advHost: advHost,
		advPort: advPort,
		nick:    advHost,
	}

	// ① 先建自己的队列（两种模式都先做这件事）
	env, _ := protocol.NewEnvelope(protocol.TypeNew, mustNewID(), "", nil)
	resp, err := s.request(env, protocol.TypeNewOk)
	if err != nil {
		return err
	}
	var okMsg protocol.NewOkMessage
	resp.DecodePayload(&okMsg)
	s.outbox = okMsg.QueueID

	// ② 打印自己的邀请——此刻就可以发给对方了
	fmt.Printf("═══ knock ═══\n")
	fmt.Printf("你的邀请链接（发给对方）:\n  %s\n\n", s.myInvite())

	// ③ 拿到对方的链接：命令行给了就用，没给就在这里等粘贴
	if peer == nil {
		fmt.Print("把对方发的邀请链接粘贴到这里，回车: ")
		line := bufio.NewScanner(os.Stdin)
		if !line.Scan() {
			return fmt.Errorf("未输入链接")
		}
		p, err := protocol.ParseInvite(strings.TrimSpace(line.Text()))
		if err != nil {
			return fmt.Errorf("对方链接不合法: %w", err)
		}
		peer = p
		// 对方链接里的 host 才是真正的服务器地址，广播地址以它为准
		s.advHost, s.advPort, s.nick = p.Host, p.Port, p.Host
	}

	// ④ 订阅对方队列
	env, _ = protocol.NewEnvelope(protocol.TypeSub, peer.QueueID, "", nil)
	if _, err := s.request(env, protocol.TypeSubOk); err != nil {
		return err
	}
	s.inbox = peer.QueueID

	fmt.Printf("已连接，开始聊天。直接输入文字回车发送，/quit 退出。\n════════════\n\n")

	go s.writePump()
	go s.readPump()
	s.stdinLoop()
	return nil
}

func (s *Session) myInvite() string {
	inv := &protocol.Invite{Host: s.advHost, Port: s.advPort, QueueID: s.outbox}
	return inv.String()
}

// // myHost/myPort: 从连接信息反推自己的对外地址（简化实现，见正文讨论）
// func (s *Session) myHost() string { return hostOverride() }

// func hostOverride() string {
// 	return
// }
// func (s *Session) myPort() int { return portOverride() }

// func portOverride() int {
// 	return peer.Port
// }

func buildWSURL(host string, port int) string {
	return fmt.Sprintf("ws://%s:%d/ws", host, port)
}

// 一个顺手的小工具（消除三处重复的 NewID+err检查）
func mustNewID() string {
	id, err := protocol.NewID()
	if err != nil {
		panic(err) // 随机数失败属于不可恢复环境错误，panic 合理
	}
	return id
}
