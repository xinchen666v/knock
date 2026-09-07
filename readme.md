# Knock 协议 v1

## 概述
一句话：客户端通过 WebSocket 与中继通信，服务器只做队列转发。

## 传输层
- WebSocket，一条 ws 消息 = 一条 JSON 信封
- 心跳：客户端每 30s 发 PING，服务器回 PONG；90s 无响应判死

## 信封结构
| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| v | int | 是 | 协议版本，当前为 1 |
| type | string | 是 | 命令类型 |
| qid | string | 否 | 队列ID |
| mid | string | 否 | 消息ID，用于去重与ACK |
| ts | int64 | 是 | Unix秒 |
| payload | object | 是 | 各命令的载荷，见下表 |

## 命令表
（每个命令：方向、payload 结构、成功应答、失败应答）
NEW / SUB / SEND / DELIVER / ACK / PING / PONG / ERROR

## 错误码
400 / 404 / 429 各自含义

## 邀请链接格式
knock://host[:port]/qid，端口缺省 8080，qid 为 32 位 hex

## 已知限制（v1 刻意不做的）
- 无加密（M2 加入）
- 无离线暂存（M2 加入）
- 同一队列的多次订阅行为：见“设计决策”一节
