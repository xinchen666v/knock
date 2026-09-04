package protocol

type Invite struct {
	Host string //IP或者域名
	Port int //端口
	QueueID string //32位hex
}