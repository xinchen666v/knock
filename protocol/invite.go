package protocol

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
)

type Invite struct {
	Host    string //IP或者域名
	Port    int    //端口
	QueueID string //32位hex
}

func (i *Invite) String() string {
	if i.Port == 0{
		return fmt.Sprintf("knock://%s/%s",i.Host,i.QueueID)
	}
	return fmt.Sprintf("knock://%s:%d/%s", i.Host, i.Port, i.QueueID)
}

func ParseInvite(link string) (*Invite, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("parse invite: %w", err)
	}
	if u.Scheme != "knock" {
		return nil, fmt.Errorf("invalid scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host")
	}
	if u.Path == "" || u.Path == "/" {
		return nil, fmt.Errorf("missing queue id")
	}
	qid := u.Path[1:]
	if len(qid) != 32 {
		return nil, fmt.Errorf("invalid queue id %q", qid)
	}
	// 只进行校验，丢弃转换结果
	_, err = hex.DecodeString(qid)
	if err != nil {
		return nil, fmt.Errorf("invalid queue id %q: %w", qid, err)
	}

	port := 8080
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return nil, fmt.Errorf("invalid port %q: %w", u.Port(), err)
		}
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("invalid port %d", port)
		}
	}

	return &Invite{
		Host:    u.Hostname(),
		Port:    port,
		QueueID: qid,
	}, nil
}
