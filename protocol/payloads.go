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

// M1 阶段的错误码
const (
	CodeBadRequest = 400  //命令格式不对
	CodeQueueNotFound = 404  //qid不存在
	CodeQueueFull = 429   //同一个队列重复订阅的冲突
)