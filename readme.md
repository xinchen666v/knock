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


# M2 离线消息
状态：完成（核心承诺全部验收，日志证据见文末附录）
前置：M1 协议雏形（NEW / SUB / SEND / DELIVER / ACK + 内存 hub）

##  M2 兑现了什么
一句话：服务器重启不丢队列、不丢未签收消息；客户端重启不丢身份。
|承诺|	实现机制|
|---|---|
|队列跨重启存活	|队列落 SQLite（store ready），内存条目丢失后可从 DB 复活|
|消息不丢	|SEND 无条件落库（DB 是真相源），在线投递也先落库，为重投留底|
|离线补投	|SUB 成功时 replayOffline：只补投未签收消息（replaying count=N）|
|签收闭环	|客户端 ACK → 服务器 MarkDelivered，补投只看 undelivered|
|客户端身份持久化	|qid 存 SharedPreferences（方案三：清空 + 随机覆盖作废），App 重启后零交互恢复|
|最近对话快捷入口	|last_peer_link 存 prefs，欢迎页“继续上次对话”，重置身份时一并清除|

## 2. 核心设计决策（及为什么）
### 2.1 qid 即凭证（capability 模型）
拿到 qid 的人就是队列主人：能 SEND（经自己的队列）、能 SUB（收自己的消息）。服务器不记录、也不关心“谁的 qid”——队列的 owner 字段只绑定在本进程内 NEW 过的连接上，服务器重启后复活的队列 owner 为 nil，不影响任何授权。

这个决策的直接推论：日志里“owner left”只在队列于本进程内 NEW 过时出现。复活队列断开时只有 subscriber left——这不是 bug，是模型使然。排查日志时注意。

### 2.2 owner 概念与“谁连着”解耦
M1 里“队列随创建者连接生灭”；M2 正式作废该语义。断开只清内存引用（owner left / subscriber left 后，双 nil 则删除内存条目），DB 里的一切继续活着。内存回收与 DB 持久化分层：内存是缓存，DB 是真相。

### 2.3 消息重建信封
客户端 SEND 的信封字段不可信，服务器收到后只透传 payload，qid / mid 由服务器侧确认或补发。DELIVER 的路由依据是服务器自己维护的订阅关系，不是客户端声称的字段。

### 2.4 协议卫生（M3 之前立好的规矩）
NEW 不得携带 qid——身份由服务器铸造，客户端对身份零发言权。携带即 400 拒绝（此前客户端曾把随机 ID 塞进 qid 槽位，服务器静默忽略；现已两端修复，静默容忍升级为显式拒绝）；
不得 SUB 自己的队列——服务器显式 400 拒绝。客户端此前有过“自链接检查顺序”bug，教训通用化：凡客户端可能踩的坑，服务器都要假设一定会被踩到（客户端修复是礼貌，服务器拒绝是法律）；
releaseQueue 按角色独立释放——同一连接可同时是一个队列的 owner 和另一队列的 sub，两个角色的清理必须互不排斥（else if 曾导致 sub 悬垂指针，已修复并用双端会话日志验证）。
## 3. 客户端
### 3.1 身份生命周期
首次进入对话时若无身份则铸造（NEW）并持久化；
重置身份 = 清空本地 qid + 清空 last_peer_link + 随机值覆盖原存储位置。语义是“切一段干净的过去”——注意服务器侧旧队列不会删除（协议尚无 DEL），旧链接在对方手里仍指向活队列，这是已知边界（见 §4）；
权衡记录：qid 单槽位、绑定单服务器。多账号/多服务器列入后续议题。
### 3.2 导航与状态
欢迎页从 prefs 读取 last_peer_link，提供“继续上次对话”（视觉层级刻意低于主流程，无记录时不渲染）；
坑位记录：Flutter initState 只跑一次，从上层路由返回不会重建——所有 push 必须 await 并在返回后重读 prefs，否则出现“按钮该出现不出现 / 该消失不消失”的双向失效。
## 4. 已知限制（诚实清单）
补投触发于订阅时刻：收件人单方面上线时，若对方不在线，对方发来的离线新消息要等对方下次重连（重 SUB 触发 replay）才送达。消息不丢，但送达时机可能滞后——“上线即收”需要 PUSH 事件，归入 M4 presence。
聊天记录仅存内存：Flutter Session 是单例，App 重启即清空；服务器只补投未签收消息，不重放历史。本地历史持久化列入后续议题，且必须与 M3 的 E2E 加密一并设计（明文攒聊天记录与产品定位冲突）。
单队列单订阅者：SUB 踢掉旧订阅者。多端登录在 M2 是显式不支持。
无队列吊销（DEL）：重置身份后旧队列仍在服务器存活。吊销语义与 E2E 凭证模型强耦合，M3 一并做。
单槽位绑定单服务器：身份与最近对话均只记一份，跨服务器切换会互相覆盖。
ACK 无归属校验：M2 威胁模型下无害（qid 即凭证），M3 凭证模型时重新评估。

## 5. 验收证据（日志摘录）
```
14:55  A:NEW→af94d035  B:NEW→f0c877a1  互发 1-6 全部 delivered+acked   ← 基线
14:56  exit status 1（非优雅终止）→ 14:56:51 重启 store ready
14:58  SUB f0c877a1 → queue revived from db（零 NEW，身份复用）
14:59  互发 7-12 正常；B 掉线 → A 发 13-15 → queued offline ×3
15:00  再重启 → B 重连 SUB → replaying count=3（与落库精确对应）
       B 端按序收到 [localhost] 13 14 15 → 三条 ACK 闭环


```

协议卫生验证：
```
NEW qid=""                                  ← 客户端槽位已清空
SUB 自队列 → 服务器拒绝（400): cannot subscribe your own queue
一连接双角色断开 → owner left + subscriber left 成对出现，total 归零  ← releaseQueue 多角色释放

```

## 6. 给 M3 的交接
M2 留给 E2E 加密阶段的接口与债：

- qid 升级为凭证的一部分 → §2.1 的 capability 模型是地基，密钥交换将附着其上；
- DEL 吊销 → §4.4 的缺口，与凭证生命周期一并设计；
- 富媒体预告：payload 已按“透明字节流”设计（服务器只透传），图片等二进制内容在协议上无障碍，但正式版等 M3 加密后一起做，避免明文版本做两遍；
- 本地存储（联系人、聊天记录）→ 等密钥模型定夺密文/明文后再动。

## 关于TTL
TTL 过期 = 队列及全部未签收消息不可逆销毁