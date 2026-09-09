# knock 协议 v1

> 一个端到端加密聊天的最小可行实现（M1 完成）。
> 本文档同时是协议契约与开发复盘。

## 概述

一句话：客户端通过 WebSocket 与中继通信，服务器只做队列转发，不存储、不读内容。

## 当前状态

- [x] M1 雏形：NEW / SUB / SEND / DELIVER / ACK / PING / PONG / ERROR 全链路
- [x] 服务器日志（slog 行车记录仪）
- [x] 公网部署验证：cloudflared quick tunnel + wss，双客户端跨公网互通
- [ ] M2：SQLite 持久化 + 离线消息（消灭 503 暂态）
- [ ] M3：端到端加密

## 目录结构

knock/
├── client/            客户端
│   ├── main.go        子命令入口（new / chat），FlagSet 解析
│   ├── session.go     会话：拨号、请求-应答、收发循环
│   └── session_test.go
├── protocol/          协议层（客户端服务器共享，唯一真相源）
│   ├── envelope.go    信封编解码
│   ├── invite.go      邀请链接的生成与解析
│   ├── payloads.go    各命令载荷结构
│   └── *_test.go
├── server/            中继服务器
│   ├── main.go        启动、监听、日志初始化
│   ├── hub.go         队列注册表、订阅管理、离线 503 判定
│   └── client.go      单连接的读写泵
├── go.mod
└── readme.md

依赖方向：client → protocol ← server。协议层不 import 任何一方。

## 传输层

- WebSocket，一条 ws 消息 = 一条 JSON 信封
- 心跳：客户端每 30s 发 PING，服务器回 PONG；90s 无响应判死
- 服务器地址两种形态：
  - `host:port` → 明文 `ws://host:port/ws`（本地/局域网）
  - `host`（无端口）→ 加密 `wss://host/ws`（443，隧道/反代模式）

## 信封结构

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| v | int | 是 | 协议版本，当前为 1 |
| type | string | 是 | 命令类型 |
| qid | string | 否 | 队列ID |
| mid | string | 否 | 消息ID，用于去重与ACK |
| ts | int64 | 是 | Unix秒 |
| payload | object | 是 | 各命令的载荷，见命令表 |

## 命令表

| 命令 | 方向 | payload | 成功应答 | 失败应答 |
|---|---|---|---|---|
| NEW | C→S | 无 | NEW_OK{queue_id} | — |
| SUB | C→S | {queue_id} | SUB_OK | 404 |
| SEND | C→S | {body} | ACK{mid} | 503（无订阅者）/ 404 |
| DELIVER | S→C | {mid, body} | — | — |
| ACK | C→S | {mid} | — | — |
| PING | C→S | 无 | PONG | — |
| ERROR | S→C | {code, message} | — | — |

## 错误码

| 码 | 含义 | 说明 |
|---|---|---|
| 400 | 请求不合法 | 信封/载荷解析失败 |
| 404 | 队列不存在 | SUB/SEND 指向未知 qid |
| 429 | 请求过频 | 限流触发 |
| 503 | 队列暂无订阅者 | SEND 时对端离线；M2 后将从"终态"变"暂态"（落库补投） |

## 邀请链接格式

knock://host[:port]/qid

- 本地/局域网：`knock://192.168.1.5:8080/abcd...`（ws）
- 隧道模式：`knock://my-tunnel.trycloudflare.com/abcd...`（无端口 → wss）
- qid 为 32 位 hex

## 服务器地址的三级优先级

1. 对方邀请链接里的 host:port（join 方自动继承）
2. `-server` 命令行参数（先行方必须显式给）
3. 缺省 `localhost:8080`（本地开发）

## 部署形态

### 本地开发

    go run ./server
    go run ./client chat

### 公网（Cloudflare quick tunnel，零成本）

    # 终端1
    go run ./server
    # 终端2
    cloudflared tunnel --url http://localhost:8080
    # → 打印 https://xxx.trycloudflare.com，即公网入口（自动 TLS）

    # 先行方 A
    go run ./client chat -server xxx.trycloudflare.com
    # join 方 B：粘 A 的链接即可

链路：客户端 --wss:443--> CF 边缘 --http--> cloudflared --ws--> localhost:8080

### 云服务器（VPS 备选）

Linux 交叉编译注意 GOARCH（x86 机器 amd64，Oracle ARM 机 arm64），
用 systemd 托管（Restart=always），开端口要开两道：系统防火墙 + 云控制台安全组。

## 踩坑记录（最有价值的部分）

每条 = 现象 → 根因 → 教训

1. **错误应答静默丢失**
   现象：对方离线时客户端毫无反应。根因：`if err == nil` 条件写反，
   错误分支永远不进。教训：错误路径要专门造场景测试（杀掉对端再发消息）。

2. **`lookup https: no such host`**
   现象：DNS 去解析一个叫 "https" 的主机名。根因：`wss://` 拼到了
   已带 `https://` 前缀的字符串上，双重 scheme。教训：URL 只在一个
   函数里组装（wsURL），打印 `dialing: <url>` 一行即可让此类 bug 现形。

3. **邀请链接永远是 localhost**
   现象：连的是隧道，印出来的链接却是 localhost:8080。根因：打印邀请
   发生在解析对方链接（更新服务器地址）之前；连接地址和广播地址两个
   用途用了不同来源的数据。教训：同一事实只有一个写入点（advHost），
   用途不同的数据不能共享字段名却来源不同。

4. **cloudflared 报 "does not support ws protocol"**
   根因：`--url` 要的是回源协议，只能填 http://；ws 只是 http 上的
   Upgrade，隧道原样透传，无需声明。教训：分清"入口协议"和"端到端协议"。

5. **QUIC 连接超时**
   现象：隧道注册失败/不稳。根因：国内运营商对 UDP 限流。
   解法：`--protocol http2`（新版会自动降级，日志 SUMMARY 可见）。

6. **winget 安装报 12029**
   根因：msstore 源网络不通，与目标软件无关。解法：`--source winget`
   指定健康源，或直接 GitHub 下载。教训：Win32 网络错误码先问"哪个
   源不通"，再问"能不能换源"。

7. **进程还跑着旧代码**
   现象：改了服务端但行为不变。根因：`go build` 产物没重新生成 / 旧
   进程没杀。教训：改完代码 → 重新编译 → 确认进程重启，三步缺一不可。

8. **Invite.String() 泄漏 `:0`**
   现象：隧道模式的链接带 `:0`（内部哨兵值泄漏到协议文本）。
   状态：待修。原则：内部表示不出现在用户可见的协议字符串里。

## 方法论沉淀

- **观测性是攒出来的**：三层日志（客户端 dialing 打印 / 服务器 slog /
  cloudflared 请求日志）对时间戳，问题定位在哪一层一眼分明。
  出了事再补日志，晚了。
- **改动一半是事故之源**：硬编码测试地址、注释掉的用法检查，都是
  "临时改一下"留下的雷。测试脚手架用完即拆。
- **便利默认值要分层**：`localhost:8080` 作为"完全没配置时"的默认是
  便利；把"无端口"一律默认 8080 则会和隧道模式的 wss 语义打架。
  哨兵值（port=0）要有明确的、全局一致的含义。
- **多层结构的问题用二分法定位**：先浏览器验证网络层可达，再验证
  应用层——一层一层排除，不要全链路盲猜。

## 设计决策

- 服务器无状态（M1）：队列只存内存 map，进程重启即失——换来最简单
  的正确性证明，M2 再引入持久化。
- request-response 模式：客户端对每个请求等待特定 type 的应答，
  超时报错，避免异步竞态。
- port=0 作为 wss 信号：链接文本无端口 ⇔ 加密传输，协议文本自描述。

## 已知限制（v1 刻意不做的）

- 无端到端加密（M3 加入；当前 TLS 只保护客户端到服务器的链路，
  服务器可见明文）
- 无离线暂存（M2 加入；当前表现为 SEND 收到 503）
- 无消息持久化 / 历史
- 同一队列的多次订阅行为：后订阅者覆盖前者
- quick tunnel 地址随机且随进程存活，重启即变（生产需 named tunnel）
