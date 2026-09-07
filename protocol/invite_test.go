// protocol/invite_test.go
package protocol

import "testing"

func TestInviteRoundTrip(t *testing.T) {
	orig := &Invite{Host: "192.168.1.5", Port: 8080, QueueID: "b7f3e2a1c9d84f02a6b3c8d7e5f10293"}
	link := orig.String()

	got, err := ParseInvite(link)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if *got != *orig {
		t.Errorf("往返不一致: want %+v, got %+v", orig, got)
	}
}

func TestInviteDefaultPort(t *testing.T) {
	inv, err := ParseInvite("knock://example.com/b7f3e2a1c9d84f02a6b3c8d7e5f10293")
	if err != nil {
		t.Fatalf("无端口应默认8080, 却报错: %v", err)
	}
	if inv.Port != 8080 {
		t.Errorf("默认端口应为8080, got %d", inv.Port)
	}
}

// 表驱动测试：一组 输入→期望 的用例跑同一个逻辑
func TestParseInviteRejects(t *testing.T) {
	cases := []struct {
		name string
		link string
	}{
		{"错误scheme", "http://example.com/b7f3e2a1c9d84f02a6b3c8d7e5f10293"},
		{"缺qid", "knock://example.com"},
		{"空qid", "knock://example.com/"},
		{"qid含非法字符", "knock://example.com/zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
		{"qid长度不对", "knock://example.com/abc"},
		{"端口越界", "knock://example.com:99999/b7f3e2a1c9d84f02a6b3c8d7e5f10293"},
		{"host为空", "knock:///b7f3e2a1c9d84f02a6b3c8d7e5f10293"},
		{"完全的乱码", "随便什么字符串"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { // 子测试：每个用例独立报告
			if inv, err := ParseInvite(c.link); err == nil {
				t.Errorf("应报错却成功了: %+v", inv)
			}
		})
	}
}
