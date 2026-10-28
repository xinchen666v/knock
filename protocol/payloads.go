package protocol

// 客户端 -> 服务端

// SEND 的 payload
type ChatMessage struct {
	Text string `json:"text"`
}

// ACK
type AckMessage struct {
	MsgID string `json:"mid"`
}

//服务端 -> 客户端

//NEW_OK
type NewOkMessage struct {
	QueueID string `json:"qid"`
}

//ERROR 
type ErrorMessage struct {
	Code int `json:"code"`
	Message string `json:"message"`
}

// 错误码。服务端必须显式拒绝客户端能踩到的每一个坑——
// 客户端的礼貌修复是加分项，服务端的硬拒绝才是底线。
const (
	CodeBadRequest     = 400 //命令格式不对
	CodeForbidden      = 403 //身份对不上（如 ACK 一个不是自己订阅的队列）
	CodeQueueNotFound  = 404 //qid不存在
	CodeTooManyRequests = 429 //限流：建队列过频
	CodeServerError    = 500 //存储/内部错误
)