package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/xinchen666v/knock/crypto"
	"github.com/xinchen666v/knock/identity"
	"github.com/xinchen666v/knock/protocol"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "new":
		// 只创建队列、打印邀请链接就退出（调试/单发模式用）
		addr := ""
		if len(os.Args) >= 3 {
			addr = os.Args[2]
		}
		invite, err := runNew(addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误：%s", err)
			os.Exit(1)
		}
		fmt.Println("把这条邀请链接发给对方:")
		fmt.Println(invite)

	case "chat":
		// link := ""
		// if len(os.Args) >= 3 {
		// 	// fmt.Fprintln(os.Stderr, "用法：knock chat <对方邀请链接>")
		// 	// os.Exit(1)
		// 	link = os.Args[2]
		// }
		// if err := runChat(link); err != nil {
		// 	fmt.Fprintf(os.Stderr, "错误：%s", err)
		// 	os.Exit(1)
		// }
		fs := flag.NewFlagSet("chat", flag.ExitOnError)
		server := fs.String("server", "", "服务器地址(对方链接会覆盖))")
		queue := fs.String("queue", "", "复用自己的旧队列qid（离线消息入口）")
		fs.Parse(os.Args[2:])

		link := ""
		if args := fs.Args(); len(args) > 0 {
			link = args[0]
		}
		if err := runChat(link, *server, *queue); err != nil {
			fmt.Fprintf(os.Stderr, "错误：%s\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}

}

// client/session.go 里追加

// runNew: 创建一条队列，打印邀请链接后退出。
// 可选参数指定服务器地址（如 "192.168.1.5:8080"），缺省 localhost:8080
func runNew(addr string) (string, error) {
	id, err := loadMyIdentity()
	if err != nil {
		return "", err
	}

	if addr == "" {
		addr = "localhost:8080"
	}
	// invite := &protocol.Invite{Host: hostOf(addr), Port: portOf(addr), QueueID: "placeholder"}
	// conn, err := dialAddr(invite.Host, invite.Port)

	host, port := parseAddr(addr) // 统一用 parseAddr：无端口 → 0 → wss
	conn, err := dialAddr(host, port)

	if err != nil {
		return "", err
	}
	defer conn.Close()

	s := &Session{conn: conn, send: make(chan []byte, 16), id: id}

	// qid, err := protocol.NewID()
	// if err != nil {
	// 	return "", err
	// }
	env, _ := protocol.NewEnvelope(protocol.TypeNew, "", "", nil)
	resp, err := s.request(env, protocol.TypeNewOk)
	if err != nil {
		return "", err
	}
	var okMsg protocol.NewOkMessage
	resp.DecodePayload(&okMsg)

	// out := &protocol.Invite{Host: hostOf(addr), Port: portOf(addr), QueueID: okMsg.QueueID}
	out := &protocol.Invite{
		Host:    host,
		Port:    port,
		QueueID: okMsg.QueueID,
		BoxPub:  id.BoxPub,
	}
	return out.String(), nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `knock - 端到端加密聊天的 M1 雏形

用法:
  knock new  [-server 地址]              创建队列，打印邀请链接后退出
  knock chat [-server 地址] [对方链接]   进入双向聊天

地址格式:
  localhost:8080                          本地模式（ws）
  guru-xxx.trycloudflare.com              隧道模式（无端口即 wss）

示例:
  knock chat -server my-tunnel.trycloudflare.com
  knock chat knock://my-tunnel.trycloudflare.com/abcd1234
`)
}

// client/main.go 或 session.go
func loadMyIdentity() (*crypto.Identity, error) {
	path, err := identity.DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("定位身份文件失败: %w", err)
	}
	id, created, err := identity.LoadOrCreate(path)
	if err != nil {
		return nil, err
	}
	if created {
		fmt.Println("（首次运行，已生成身份:", path, "）")
	}
	// fmt.Println("你的指纹:", crypto.Fingerprint(id.BoxPub))
	return id, nil
}
