package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const inviteVersion = "v2"

type Invite struct {
	Host    string //IP或者域名
	Port    int    //端口
	QueueID string //32位hex
	BoxPub [32]byte //M3:队列注入的Box公钥
}

// func (i *Invite) String() string {
// 	if i.Port == 0{
// 		return fmt.Sprintf("knock://%s/%s",i.Host,i.QueueID)
// 	}
// 	return fmt.Sprintf("knock://%s:%d/%s", i.Host, i.Port, i.QueueID)
// }

func (i *Invite) String() string {
	key := base64.RawURLEncoding.EncodeToString(i.BoxPub[:])
	u := url.URL{
		Scheme:   "knock",
		Host:     fmt.Sprintf("%s:%d", i.Host, i.Port), // v2 永远显式端口
		Path:     "/" + inviteVersion + "/" + i.QueueID,
		RawQuery: "k=" + key + "&c=" + inviteChecksum(i.Host, i.Port, i.QueueID, key),
	}
	return u.String()
}

// inviteChecksum 是协议契约：Dart 端逐字节复刻（已进 vectors.json）
// 注意它只防手误不防攻击——中间人改链接后可重算校验和，安全靠指纹比对
func inviteChecksum(host string, port int, qid, keyB64 string) string {
	canonical := fmt.Sprintf("knock|%s|%d|%s|%s", host, port, qid, keyB64)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:4])
}


func ParseInvite(link string) (*Invite, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("parse invite: %w", err)
	}
	if u.Scheme != "knock" {
		return nil, fmt.Errorf("invalid scheme %q", u.Scheme)
	}

	// 版本判定：path 必须是 /v2/<qid>。
	// 旧格式（/<qid>）在此被明确拒绝——旧链接不含公钥，M3 下无法建立加密会话
	segs := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segs) != 2 {
		return nil, fmt.Errorf("旧版邀请链接（不含密钥），请对方更新 knock 后重新生成")
	}
	if segs[0] != inviteVersion {
		return nil, fmt.Errorf("不支持的邀请版本 %q", segs[0]) // 未来 v3 的报错入口
	}
	qid := segs[1]
	if !ValidQueueID(qid) {
		return nil, fmt.Errorf("invalid queue id %q", qid)
	}

	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("v2 链接必须携带合法端口，got %q", u.Port())
	}

	q := u.Query()
	key, err := base64.RawURLEncoding.DecodeString(q.Get("k"))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("invalid key parameter")
	}

	got := q.Get("c")
	if got == "" {
		return nil, fmt.Errorf("missing checksum")
	}
	if got != inviteChecksum(host, port, qid, q.Get("k")) {
		return nil, fmt.Errorf("校验和不符——链接可能抄错或被截断")
	}

	inv := &Invite{Host: host, Port: port, QueueID: qid}
	copy(inv.BoxPub[:], key)
	return inv, nil
}

// func ParseInvite(link string) (*Invite, error) {
// 	u, err := url.Parse(link)
// 	if err != nil {
// 		return nil, fmt.Errorf("parse invite: %w", err)
// 	}
// 	if u.Scheme != "knock" {
// 		return nil, fmt.Errorf("invalid scheme %q", u.Scheme)
// 	}
// 	if u.Host == "" {
// 		return nil, fmt.Errorf("missing host")
// 	}
// 	if u.Path == "" || u.Path == "/" {
// 		return nil, fmt.Errorf("missing queue id")
// 	}
// 	qid := u.Path[1:]
// 	if len(qid) != 32 {
// 		return nil, fmt.Errorf("invalid queue id %q", qid)
// 	}
// 	// 只进行校验，丢弃转换结果
// 	_, err = hex.DecodeString(qid)
// 	if err != nil {
// 		return nil, fmt.Errorf("invalid queue id %q: %w", qid, err)
// 	}

// 	port := 8080
// 	if u.Port() != "" {
// 		port, err = strconv.Atoi(u.Port())
// 		if err != nil {
// 			return nil, fmt.Errorf("invalid port %q: %w", u.Port(), err)
// 		}
// 		if port < 1 || port > 65535 {
// 			return nil, fmt.Errorf("invalid port %d", port)
// 		}
// 	}

// 	return &Invite{
// 		Host:    u.Hostname(),
// 		Port:    port,
// 		QueueID: qid,
// 	}, nil
// }
