package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"

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
		fs := flag.NewFlagSet("chat",flag.ExitOnError)
		server := fs.String("server","","服务器地址(对方链接会覆盖))")
		fs.Parse(os.Args[2:])

		link := ""
		if args := fs.Args();len(args) > 0 {
			link = args[0]
		}
		if err := runChat(link,*server);err != nil {
			fmt.Fprintf(os.Stderr,"错误：%s\n",err)
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
	if addr == "" {
		addr = "localhost:8080"
	}
	invite := &protocol.Invite{Host: hostOf(addr), Port: portOf(addr), QueueID: "placeholder"}
	conn, err := dialAddr(invite.Host,invite.Port)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	s := &Session{conn: conn, send: make(chan []byte, 16)}

	qid, err := protocol.NewID()
	if err != nil {
		return "", err
	}
	env, _ := protocol.NewEnvelope(protocol.TypeNew, qid, "", nil)
	resp, err := s.request(env, protocol.TypeNewOk)
	if err != nil {
		return "", err
	}
	var okMsg protocol.NewOkMessage
	resp.DecodePayload(&okMsg)

	out := &protocol.Invite{Host: hostOf(addr), Port: portOf(addr), QueueID: okMsg.QueueID}
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

// hostOf/portOf: 把 "192.168.1.5:8080" 拆开；无端口时默认 8080
func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr // 没有端口部分，整个就是host
	}
	return host
}

func portOf(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 8080
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return 8080
	}
	return port
}
