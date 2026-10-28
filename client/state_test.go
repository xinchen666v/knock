package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xinchen666v/knock/crypto"
	"github.com/xinchen666v/knock/identity"
	"github.com/xinchen666v/knock/protocol"
)

// withTestConfigDir: 把 userConfigDir 指向临时目录，测试结束自动还原。
// 返回临时目录路径，测试里可以用它预置/检查文件
func withTestConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userConfigDir = orig })
	return dir
}

// newTestIdentity: 在临时目录生成一把真实身份（走正式的 LoadOrCreate 路径）
func newTestIdentity(t *testing.T) *crypto.Identity {
	t.Helper()
	id, _, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "id.json"))
	if err != nil {
		t.Fatalf("生成测试身份失败: %v", err)
	}
	return id
}

// fixedBoxPub: 测试用的固定 Box 公钥（32 字节）。
// BoxPub 是 [32]byte，用 copy 填充，不依赖 string→[N]byte 的转换语法
func fixedBoxPub() [32]byte {
	var b [32]byte
	copy(b[:], "0123456789abcdef0123456789abcdef")
	return b
}

// fpToHex: 由指纹推导 state 文件名里的后缀。
// 直接复用生产的 fpHex，避免测试里再抄一份转换逻辑——
// 抄一份的话，实现改了测试会跟着一起错，用例就失去意义了
func fpToHex(t *testing.T, boxPub [32]byte) string {
	t.Helper()
	clean := fpHex(boxPub)
	if len(clean) < 8 {
		t.Fatalf("指纹异常: %q", crypto.Fingerprint(boxPub))
	}
	return clean
}

// --- statePath ---

func TestStatePath_PerIdentity(t *testing.T) {
	withTestConfigDir(t)
	idA := newTestIdentity(t)
	idB := newTestIdentity(t)

	pa, err := statePath(idA)
	if err != nil {
		t.Fatalf("statePath(idA): %v", err)
	}
	pb, err := statePath(idB)
	if err != nil {
		t.Fatalf("statePath(idB): %v", err)
	}

	if pa == pb {
		t.Errorf("不同身份的 state 路径不应相同: %s", pa)
	}

	// 路径形如 <dir>/knock/state-XXXXXXXX.json
	base := filepath.Base(pa)
	if !strings.HasPrefix(base, "state-") || !strings.HasSuffix(base, ".json") {
		t.Errorf("文件名格式不对: %s", base)
	}
	suffix := strings.TrimSuffix(strings.TrimPrefix(base, "state-"), ".json")
	if len(suffix) != 8 {
		t.Errorf("指纹后缀应为8位hex，得到 %q (%d位)", suffix, len(suffix))
	}

	// 后缀必须是该身份指纹的前8位
	fp := fpToHex(t, idA.BoxPub)
	if suffix != fp[:8] {
		t.Errorf("后缀 %q ≠ 指纹前8位 %q", suffix, fp[:8])
	}
}

// --- loadState ---

func TestLoadState_NoFile(t *testing.T) {
	withTestConfigDir(t)
	id := newTestIdentity(t)

	st := loadState(id)
	if st == nil {
		t.Fatal("loadState 不应返回 nil")
	}
	if st.Outbox == nil {
		t.Error("无文件时 Outbox 应是非 nil map")
	}
	if st.Contacts == nil {
		t.Error("无文件时 Contacts 应是非 nil map")
	}
}

func TestLoadState_LegacyFile_NoContacts(t *testing.T) {
	// 关键回归：旧版 state 文件只有 outbox 没有 contacts 字段。
	// Decode 后 Contacts 会是 nil，不补防御的话第一次 upsert 就 panic
	withTestConfigDir(t)
	id := newTestIdentity(t)

	dir, _ := userConfigDir()
	knockDir := filepath.Join(dir, "knock")
	if err := os.MkdirAll(knockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := statePath(id)
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	legacy := `{"outbox": {"localhost": "d7be6dba9a5a1c52767c22789355af8d"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	st := loadState(id)
	if st.Contacts == nil {
		t.Fatal("旧格式文件加载后 Contacts 必须补成非 nil，否则写入 panic")
	}
	if got := st.Outbox["localhost"]; got != "d7be6dba9a5a1c52767c22789355af8d" {
		t.Errorf("旧数据丢失: got %q", got)
	}

	// 旧文件加载后直接 upsert 不应 panic
	err = upsertContact(id, &protocol.Invite{BoxPub: fixedBoxPub()}, "knock://x")
	if err != nil {
		t.Fatalf("旧文件 + upsertContact 不应出错: %v", err)
	}
}

// --- save / load 往返 ---

func TestSaveLoad_Roundtrip(t *testing.T) {
	withTestConfigDir(t)
	id := newTestIdentity(t)

	st := loadState(id)
	st.Outbox["localhost"] = "d7be6dba9a5a1c52767c22789355af8d"
	st.Outbox["relay.example.com"] = "673fa42d374d02eaacd14d2e900d4833"
	st.Contacts["aabbccdd00112233"] = &Contact{
		Fingerprint: "aabbccdd00112233",
		InviteURL:   "knock://localhost:8080/v2/673fa42d...?k=xx&c=yy",
		LastSeen:    1700000000,
		Alias:       "老张",
	}
	if err := saveState(id, st); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	got := loadState(id)
	if got.Outbox["localhost"] != st.Outbox["localhost"] {
		t.Errorf("Outbox[localhost] 往返不一致: %q", got.Outbox["localhost"])
	}
	if got.Outbox["relay.example.com"] != st.Outbox["relay.example.com"] {
		t.Errorf("Outbox[relay] 往返不一致: %q", got.Outbox["relay.example.com"])
	}
	c, ok := got.Contacts["aabbccdd00112233"]
	if !ok {
		t.Fatal("联系人往返丢失")
	}
	if c.Alias != "老张" || c.InviteURL != st.Contacts["aabbccdd00112233"].InviteURL {
		t.Errorf("联系人字段往返不一致: %+v", c)
	}
}

func TestSaveState_Overwrite(t *testing.T) {
	withTestConfigDir(t)
	id := newTestIdentity(t)

	st := loadState(id)
	st.Outbox["localhost"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := saveState(id, st); err != nil {
		t.Fatal(err)
	}

	st2 := loadState(id)
	st2.Outbox["localhost"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := saveState(id, st2); err != nil {
		t.Fatal(err)
	}

	got := loadState(id)
	if got.Outbox["localhost"] != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Errorf("二次保存应覆盖: got %q", got.Outbox["localhost"])
	}
	// 原子写不应留下 .tmp 残留
	dir, _ := userConfigDir()
	matches, _ := filepath.Glob(filepath.Join(dir, "knock", "state-*.tmp"))
	if len(matches) != 0 {
		t.Errorf("不应残留临时文件: %v", matches)
	}
}

func TestSaveLoad_PerIdentityIsolation(t *testing.T) {
	// 复现当初 AB 撞车的事故：两个身份各自保存，互不可见
	withTestConfigDir(t)
	idA := newTestIdentity(t)
	idB := newTestIdentity(t)

	stA := loadState(idA)
	stA.Outbox["localhost"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := saveState(idA, stA); err != nil {
		t.Fatal(err)
	}

	stB := loadState(idB)
	if q := stB.Outbox["localhost"]; q != "" {
		t.Errorf("B 不应看到 A 的队列: %q", q)
	}
	stB.Outbox["localhost"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := saveState(idB, stB); err != nil {
		t.Fatal(err)
	}

	// 再读 A，确认没被 B 覆盖
	if got := loadState(idA).Outbox["localhost"]; got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("A 的队列被 B 污染: %q", got)
	}
}

// --- upsertContact ---

func TestUpsertContact_NewThenUpdate(t *testing.T) {
	withTestConfigDir(t)
	id := newTestIdentity(t)

	peer := &protocol.Invite{
		Host:    "localhost",
		Port:    8080,
		QueueID: "673fa42d374d02eaacd14d2e900d4833",
		BoxPub:  fixedBoxPub(),
	}
	link := "knock://localhost:8080/v2/673fa42d374d02eaacd14d2e900d4833?k=xx&c=yy"

	if err := upsertContact(id, peer, link); err != nil {
		t.Fatalf("首次 upsert: %v", err)
	}

	fp := fpHex(peer.BoxPub)
	st := loadState(id)
	c, ok := st.Contacts[fp]
	if !ok {
		t.Fatalf("联系人未入册，map 里有 %d 条", len(st.Contacts))
	}
	if c.InviteURL != link {
		t.Errorf("InviteURL 不一致: %q", c.InviteURL)
	}
	firstSeen := c.LastSeen
	if firstSeen == 0 {
		t.Error("LastSeen 应被设置")
	}

	// 第二次 upsert：更新而非新增，链接变化要跟进来
	newLink := link + "&extra=1"
	if err := upsertContact(id, peer, newLink); err != nil {
		t.Fatal(err)
	}
	st = loadState(id)
	if n := len(st.Contacts); n != 1 {
		t.Fatalf("重复 upsert 不应新增条目，现在有 %d 条", n)
	}
	if st.Contacts[fp].InviteURL != newLink {
		t.Errorf("链接应更新为最新: %q", st.Contacts[fp].InviteURL)
	}
	if st.Contacts[fp].LastSeen < firstSeen {
		t.Error("LastSeen 不应倒退")
	}
}

// --- sortedContacts ---

func TestSortedContacts(t *testing.T) {
	withTestConfigDir(t)
	id := newTestIdentity(t)

	st := loadState(id)
	st.Contacts["aaaa"] = &Contact{Fingerprint: "aaaa", InviteURL: "knock://a", LastSeen: 100}
	st.Contacts["bbbb"] = &Contact{Fingerprint: "bbbb", InviteURL: "knock://b", LastSeen: 300}
	st.Contacts["cccc"] = &Contact{Fingerprint: "cccc", InviteURL: "knock://c", LastSeen: 200}
	st.Contacts["dddd"] = &Contact{Fingerprint: "dddd", InviteURL: "", LastSeen: 999} // 无链接，应被过滤

	got := sortedContacts(st)
	if len(got) != 3 {
		t.Fatalf("应过滤掉无链接条目，得到 %d 条", len(got))
	}
	wantOrder := []string{"bbbb", "cccc", "aaaa"} // LastSeen 降序
	for i, w := range wantOrder {
		if got[i].Fingerprint != w {
			t.Errorf("位置 %d 应为 %s，得到 %s", i, w, got[i].Fingerprint)
		}
	}
}

// --- saveState 产物可读性抽查（确保 JSON 结构符合预期字段名）---

func TestSaveState_JSONFieldNames(t *testing.T) {
	withTestConfigDir(t)
	id := newTestIdentity(t)

	st := loadState(id)
	st.Outbox["localhost"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	st.Contacts["aabb"] = &Contact{
		Fingerprint: "aabb",
		InviteURL:   "knock://x",
		LastSeen:    42,
		Alias:       "老张",
	}
	if err := saveState(id, st); err != nil {
		t.Fatal(err)
	}

	path, err := statePath(id)
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("落盘的不是合法 JSON: %v", err)
	}
	if _, ok := probe["outbox"]; !ok {
		t.Error(`缺少 "outbox" 字段`)
	}
	if _, ok := probe["contacts"]; !ok {
		t.Error(`缺少 "contacts" 字段`)
	}
	// 防止将来改字段名悄悄破坏旧文件兼容性
	s := string(raw)
	for _, want := range []string{`"invite_url"`, `"last_seen"`, `"fingerprint"`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON 里缺少 %s，字段 tag 被改了？", want)
		}
	}
}
