package protocol

import (
	"encoding/json"
	"fmt"
	"time"
	"crypto/rand"
	"encoding/hex"

)

// 协议版本
const Version = 1

// 客户端 -> 服务端 的四种命令
const (
	TypeNew = "NEW" // 创建队列
	TypeSub = "SUB" // 订阅队列
	TypeSend = "SEND" // 发送消息
	TypeAck = "ACK" // 确认收到
	TypePing = "PING" // 心跳
)

// 服务端 -> 客户端 的应答和推送
const (
	TypeNewOk = "NEW_OK" // 创建队列成功,payload携带qid
	TypeSubOk = "SUB_OK" // SUB的应答
	TypeDeliver = "DELIVER" // 推送消息给订阅者
	TypePong = "PONG" // 心跳应答
	TypeError = "ERROR" // 错误通道，payload携带错误原因
)

// Envelope 是所有消息的统一信封
// 服务端只读信封字段做路由，不理解payload的内容
type Envelope struct {
	Version int `json:"v"`
	Type string `json:"type"`
	QueueID string `json:"qid,omitempty"`//omitempty:空则不序列化
	MsgID string `json:"mid,omitempty"` //消息唯一ID，用于去重和ACK
	TS int64 `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// NewEnvelope 构造一条消息。payload 传任意可序列化的东西，传 nil 则为空对象
func NewEnvelope(msgType,qid,mid string,payload any)(*Envelope,error){
	var raw json.RawMessage
	if payload != nil {
		b,err := json.Marshal(payload)
		if err != nil {
			return nil,fmt.Errorf("marshal payload :%w",err)
		}
		raw = b
	} else {
		raw = json.RawMessage("{}")
	}
	return &Envelope{
		Version: Version,
		Type: msgType,
		QueueID: qid,
		MsgID: mid,
		TS: time.Now().Unix(),
		Payload: raw,
	},nil
}

// Parse 把一帧字节解析成信封，并做基本校验
func Parse(data []byte)(*Envelope,error){
	var e Envelope
	if err := json.Unmarshal(data,&e); err != nil {
		return nil,fmt.Errorf("invalid json: %w",err)
	}
	if e.Version != Version {
		return nil,fmt.Errorf("unsupported protocol version %d (want %d)",e.Version,Version)
	}
	if e.Type == ""{
		return nil,fmt.Errorf("missing type")
	}
	return &e,nil
}

// DecodePayload 把 payload 解析成具体的结构体
func (e *Envelope) DecodePayload(v any) error {
	return json.Unmarshal(e.Payload,v)
}

func NewID() (string,error){
	b := make([]byte,16)
	if _,err := rand.Read(b); err != nil {
		return "",err
	}
	return hex.EncodeToString(b),nil
}


// ValidQueueID 校验 qid 是否为合法的32位小写hex
func ValidQueueID(qid string) bool {
	if len(qid) != 32 {
		return false
	}
	_,err := hex.DecodeString(qid)
	return err == nil
}

