package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xinchen666v/knock/crypto"
)

// Contact: 通讯录条目。指纹是身份锚点，InviteURL 存原始链接便于直接还原
type Contact struct {
	Fingerprint string `json:"fingerprint"` // 64位hex（无空格），map 的 key
	InviteURL   string `json:"invite_url"`  // 完整原始邀请链接，重连时直接 ParseInvite
	LastSeen    int64  `json:"last_seen"`   // Unix 秒，用于"最近联系人"排序
	Alias       string `json:"alias,omitempty"`
}

// State: 客户端会话状态。Outbox 按服务器记自己的队列，
// Contacts 按对方指纹记通讯录
type State struct {
	Outbox   map[string]string   `json:"outbox"`   // key = 服务器 host
	Contacts map[string]*Contact `json:"contacts"` // key = 对方指纹 hex
}

// userConfigDir: 依赖注入点。测试时替换成临时目录，避免污染真实用户配置
var userConfigDir = os.UserConfigDir

func statePath(id *crypto.Identity) (string, error) {
	// dir, err := os.UserConfigDir()
	dir, err := userConfigDir() // ← 原来是 os.UserConfigDir()
	if err != nil {
		return "", err
	}
	fp := crypto.Fingerprint(id.BoxPub) // "81 69 D1 27 ..." 带空格
	clean := strings.ReplaceAll(fp, " ", "")
	if len(clean) < 8 { // 指纹理论上不可能这么短，防御一下
		return "", fmt.Errorf("指纹异常: %q", fp)
	}
	return filepath.Join(dir, "knock", "state-"+clean[:8]+".json"), nil
}

func loadState(id *crypto.Identity) *State {
	st := &State{
		Outbox:   make(map[string]string),
		Contacts: make(map[string]*Contact),
	}
	path, err := statePath(id)
	if err != nil {
		return st
	}
	f, err := os.Open(path)
	if err != nil {
		return st // 文件不存在/读不了 → 全新状态，不算错误
	}
	defer f.Close()
	json.NewDecoder(f).Decode(st) // 坏了就用默认值，宽容处理
	// 旧版 state 文件没有 contacts 字段 → Decode 后是 nil，不补会 panic
	if st.Outbox == nil {
		st.Outbox = make(map[string]string)
	}
	if st.Contacts == nil {
		st.Contacts = make(map[string]*Contact)
	}
	return st
}

func saveState(id *crypto.Identity, st *State) error {
	path, err := statePath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// 原子写套路：临时文件 + rename
	tmp, err := os.CreateTemp(filepath.Dir(path), "state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := json.NewEncoder(tmp).Encode(st); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// fpHex: 指纹去空格，用作通讯录 map 的 key
func fpHex(boxPub [32]byte) string {
	return strings.ReplaceAll(crypto.Fingerprint(boxPub), " ", "")
}

// sortedContacts: 通讯录按 LastSeen 降序（最近的在前）
func sortedContacts(st *State) []*Contact {
	list := make([]*Contact, 0, len(st.Contacts))
	for _, c := range st.Contacts {
		if c.InviteURL == "" {
			continue // 没链接的条目无法还原，跳过
		}
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].LastSeen > list[j].LastSeen
	})
	return list
}
