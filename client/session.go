package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xinchen666v/knock/crypto"
	"github.com/xinchen666v/knock/protocol"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 45 * time.Second
)

type Session struct {
	conn *websocket.Conn
	send chan []byte // 发送队列（写泵消费）

	outbox string // 我的队列：我往里 SEND，对方订阅后从这里收
	inbox  string // 对方的队列：我订阅它，我的 DELIVER 从这里来

	// 广播地址：双方共同的服务器，从对方的邀请链接里继承
	advHost string
	advPort int
	nick    string // 对方链接的host，用来区分聊天中谁在说话

	seen map[string]bool
	id   *crypto.Identity // M3: 我的身份，链接里带 BoxPub，密钥派生要用
}

func dialAddr(host string, port int) (*websocket.Conn, error) {
	url := buildWSURL(host, port)
	fmt.Printf("dialing: %s\n", url)
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
			continue // 坏帧跳过
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

// runChat: ①加载身份 ②确定服务器地址 ③确定自己的 outbox（-queue > 状态文件 > 新建）
// ④打印/等待交换邀请链接（含联系人选择）⑤订阅对方队列 ⑥记通讯录 ⑦进入聊天循环
func runChat(peerLink string, serverAddr string, myQueue string) error {
	// ① 身份先行：state 文件名要用指纹，必须先知道"我是谁"
	id, err := loadMyIdentity()
	if err != nil {
		return err
	}
	fmt.Printf("你的指纹: %s\n", crypto.Fingerprint(id.BoxPub))

	// ② 解析对方链接（命令行给了就先解析，没给等会儿在⑥等输入）
	var peer *protocol.Invite
	var peerRawLink string // 原始链接字符串，解析成功后始终有值，⑥用它入册

	if peerLink != "" {
		p, err := protocol.ParseInvite(peerLink)
		if err != nil {
			return fmt.Errorf("邀请链接不合法: %w", err)
		}
		peer = p
		peerRawLink = peerLink // 命令行给的原文
		fmt.Printf("对方指纹: %s\n", crypto.Fingerprint(peer.BoxPub))
	}

	// ③ 服务器地址三层优先级：对方链接 > -server 参数 > 本地默认
	advHost, advPort := parseAddr(serverAddr)
	if serverAddr == "" {
		advHost, advPort = "localhost", 8080
	}
	if peer != nil {
		advHost, advPort = peer.Host, peer.Port
	}

	conn, err := dialAddr(advHost, advPort)
	if err != nil {
		return err
	}

	s := &Session{
		conn:    conn,
		send:    make(chan []byte, 16),
		advHost: advHost,
		advPort: advPort,
		nick:    advHost,
		seen:    make(map[string]bool),
		id:      id,
	}

	// ④ 确定自己的 outbox，优先级：-queue 参数 > 状态文件 > 新建
	//    复用意味着邀请链接跨重启不变，对方手里的链接永远有效
	switch {
	case myQueue != "":
		if err := validQID(myQueue); err != nil {
			return fmt.Errorf("-queue 参数不合法: %w", err)
		}
		s.outbox = myQueue
		st := loadState(id)
		if st.Outbox[advHost] != myQueue { // 显式指定的队列也落盘，下次自动复用
			st.Outbox[advHost] = myQueue
			if err := saveState(id, st); err != nil {
				return fmt.Errorf("保存队列状态失败: %w", err)
			}
		}
	default:
		st := loadState(id)
		if q, ok := st.Outbox[advHost]; ok && validQID(q) == nil {
			s.outbox = q // 上次在这台服务器建过队列 → 直接复用
		} else {
			// 真正第一次：NEW 并落盘
			env, _ := protocol.NewEnvelope(protocol.TypeNew, "", "", nil)
			resp, err := s.request(env, protocol.TypeNewOk)
			if err != nil {
				return err
			}
			var okMsg protocol.NewOkMessage
			if err := resp.DecodePayload(&okMsg); err != nil {
				return err
			}
			if okMsg.QueueID == "" {
				return fmt.Errorf("服务器返回的 qid 为空")
			}
			s.outbox = okMsg.QueueID
			st.Outbox[advHost] = s.outbox
			if err := saveState(id, st); err != nil {
				return fmt.Errorf("保存队列状态失败: %w", err)
			}
		}
	}

	// ⑤ 打印自己的邀请——此刻就可以发给对方了
	fmt.Printf("═══ knock ═══\n")
	fmt.Printf("你的邀请链接（发给对方）:\n  %s\n\n", s.myInvite())

	// ⑥ 命令行没给对方链接 → 联系人选择 / 粘贴新链接
	if peer == nil {
		st := loadState(id)
		contacts := sortedContacts(st)
		reader := bufio.NewScanner(os.Stdin)

		for {
			if len(contacts) > 0 {
				fmt.Println("联系人列表:")
				for i, c := range contacts {
					alias := c.Alias
					if alias == "" {
						alias = "-"
					}
					fmt.Printf("  [%d] %s (%s)  上次: %s\n",
						i+1, alias, c.Fingerprint[:8], formatTime(c.LastSeen))
				}
				fmt.Print("回车=继续上次 [1]，序号=选人，粘贴新链接=新联系人: ")
			} else {
				fmt.Print("把对方发的邀请链接粘贴到这里，回车: ")
			}

			if !reader.Scan() {
				return fmt.Errorf("未输入")
			}
			input := strings.TrimSpace(reader.Text())

			switch {
			case input == "" && len(contacts) > 0:
				// 回车 → 最近联系人，无感重连
				p, err := protocol.ParseInvite(contacts[0].InviteURL)
				if err != nil {
					fmt.Println("联系人记录损坏，请粘贴链接:", err)
					continue
				}
				peer, peerRawLink = p, contacts[0].InviteURL
			case isIndex(input, len(contacts)):
				// 序号 → 选定联系人
				c := contacts[toIndex(input)]
				p, err := protocol.ParseInvite(c.InviteURL)
				if err != nil {
					fmt.Println("联系人记录损坏，请粘贴链接:", err)
					continue
				}
				peer, peerRawLink = p, c.InviteURL
			default:
				// 其余任何输入 → 按链接解析（新联系人）
				p, err := protocol.ParseInvite(input)
				if err != nil {
					fmt.Println("对方链接不合法，请重新输入:", err)
					continue
				}
				peer, peerRawLink = p, input
			}
			break
		}

		// 对方链接里的服务器才是双方共同的服务器，广播地址以它为准
		s.advHost, s.advPort, s.nick = peer.Host, peer.Port, peer.Host
		fmt.Printf("对方指纹: %s\n", crypto.Fingerprint(peer.BoxPub))
	}

	// ⑦ 订阅对方队列
	env, _ := protocol.NewEnvelope(protocol.TypeSub, peer.QueueID, "", nil)
	if _, err := s.request(env, protocol.TypeSubOk); err != nil {
		return err
	}
	s.inbox = peer.QueueID

	// SUB 成功才记通讯录，避免坏链接/不可达服务器污染联系人列表
	if err := upsertContact(id, peer, peerRawLink); err != nil {
		return fmt.Errorf("保存联系人失败: %w", err)
	}

	fmt.Printf("已连接，开始聊天。直接输入文字回车发送，/quit 退出。\n════════════\n\n")

	go s.writePump()
	go s.readPump()
	s.stdinLoop()
	return nil
}

func (s *Session) myInvite() string {
	inv := &protocol.Invite{
		Host:    s.advHost,
		Port:    s.advPort,
		QueueID: s.outbox,
		BoxPub:  s.id.BoxPub,
	}
	return inv.String()
}

func buildWSURL(host string, port int) string {
	if port == 0 || port == 443 {
		return fmt.Sprintf("wss://%s/ws", host) // 云隧道模式：默认 443 + TLS
	}
	return fmt.Sprintf("ws://%s:%d/ws", host, port)
}

// parseAddr: "host:port" → (host, port)；无端口 → (host, 0)（隧道模式信号）
// 注意：空串会返回 ("", 0)——调用方必须在调用前处理"完全没给地址"的
// 情况，兜底 localhost:8080，不能依赖本函数
func parseAddr(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0 // 无端口 → 隧道模式
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return host, 0
	}
	return host, port
}

// validQID: qid 必须是 32 位小写 hex，防止垃圾值进 state 或参数
func validQID(q string) error {
	if len(q) != 32 {
		return fmt.Errorf("qid 长度应为 32，得到 %d", len(q))
	}
	for _, c := range q {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("qid 含非 hex 字符: %q", q)
		}
	}
	return nil
}

// isIndex: input 是否是范围 [1, n] 内的序号（联系人选择用）
func isIndex(input string, n int) bool {
	if n == 0 || input == "" {
		return false
	}
	for _, c := range input {
		if c < '0' || c > '9' {
			return false
		}
	}
	v, err := strconv.Atoi(input)
	return err == nil && v >= 1 && v <= n
}

// toIndex: 序号字符串 → contacts 切片下标
func toIndex(input string) int {
	v, _ := strconv.Atoi(input)
	return v - 1
}

// formatTime: Unix 秒 → "01-02 15:04" 展示；0 视为从未
func formatTime(unix int64) string {
	if unix == 0 {
		return "从未"
	}
	return time.Unix(unix, 0).Format("01-02 15:04")
}

// upsertContact: 聊天对象记入通讯录。rawLink 存原始链接字符串，
// 重连时直接 ParseInvite(rawLink) 还原，不依赖 Invite 结构的序列化格式。
// 已存在则只刷新 LastSeen 和链接（对方换服务器/换队列走这里自动更新）
func upsertContact(id *crypto.Identity, p *protocol.Invite, rawLink string) error {
	st := loadState(id)
	fp := fpHex(p.BoxPub)

	c, ok := st.Contacts[fp]
	if !ok {
		c = &Contact{Fingerprint: fp}
	}
	c.InviteURL = rawLink
	c.LastSeen = time.Now().Unix()
	st.Contacts[fp] = c
	return saveState(id, st)
}