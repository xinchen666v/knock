package main

import (
	"path/filepath"
	"testing"
)

// openTemp 每个用例独享一个文件型 DB。注意故意用文件而不是
// :memory:——因为"重开连接数据还在"正是要测的行为
func openTemp(t *testing.T) (string, *Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return path, s
}

func TestQueueLifecycle(t *testing.T) {
	_, s := openTemp(t)

	if ok, _ := s.QueueExists("nope"); ok {
		t.Fatal("不存在的队列不该存在")
	}
	if err := s.CreateQueue("q1", 1000); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 重复创建安全（INSERT OR IGNORE）
	if err := s.CreateQueue("q1", 2000); err != nil {
		t.Fatalf("duplicate create: %v", err)
	}
	if ok, _ := s.QueueExists("q1"); !ok {
		t.Fatal("创建后应存在")
	}
}

func TestSaveAndUndelivered(t *testing.T) {
	_, s := openTemp(t)
	s.CreateQueue("q1", 1000)

	msgs := []StoredMessage{
		{Mid: "m1", QID: "q1", Payload: `{"text":"first"}`, TS: 1001},
		{Mid: "m2", QID: "q1", Payload: `{"text":"second"}`, TS: 1002},
		{Mid: "m3", QID: "q1", Payload: `{"text":"third"}`, TS: 1003},
	}
	for _, m := range msgs {
		if err := s.SaveMessage(m); err != nil {
			t.Fatalf("save %s: %v", m.Mid, err)
		}
	}

	got, err := s.Undelivered("q1")
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 messages, got %d", len(got))
	}
	// 顺序是补投语义的核心：必须按 ts 升序
	for i, want := range []string{"m1", "m2", "m3"} {
		if got[i].Mid != want {
			t.Errorf("顺序错: got[%d]=%s, want %s", i, got[i].Mid, want)
		}
	}

	// 投递两条，剩一条
	s.MarkDelivered("m1")
	s.MarkDelivered("m2")
	got, _ = s.Undelivered("q1")
	if len(got) != 1 || got[0].Mid != "m3" {
		t.Fatalf("mark 后应只剩 m3, got %+v", got)
	}
}

func TestOtherQueueIsolation(t *testing.T) {
	_, s := openTemp(t)
	s.CreateQueue("qa", 1000)
	s.CreateQueue("qb", 1000)
	s.SaveMessage(StoredMessage{Mid: "ma", QID: "qa", Payload: `{}`, TS: 1})

	got, _ := s.Undelivered("qb")
	if len(got) != 0 {
		t.Fatal("qb 不该看到 qa 的消息")
	}
}

// 重启存活——M2 的灵魂测试。关掉再开，数据必须还在
func TestSurvivesReopen(t *testing.T) {
	path, s := openTemp(t)
	s.CreateQueue("q1", 1000)
	s.SaveMessage(StoredMessage{Mid: "m1", QID: "q1", Payload: `{"text":"hi"}`, TS: 1})
	s.Close() // 模拟进程退出

	s2, err := Open(path) // 模拟重启
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	if ok, _ := s2.QueueExists("q1"); !ok {
		t.Fatal("重启后队列丢失")
	}
	got, _ := s2.Undelivered("q1")
	if len(got) != 1 || got[0].Payload != `{"text":"hi"}` {
		t.Fatalf("重启后消息丢失或损坏: %+v", got)
	}
}

// mid 主键去重——重复投递在 DB 层就该被拦下
func TestDuplicateMidRejected(t *testing.T) {
	_, s := openTemp(t)
	s.CreateQueue("q1", 1000)
	m := StoredMessage{Mid: "dup", QID: "q1", Payload: `{}`, TS: 1}
	s.SaveMessage(m)
	if err := s.SaveMessage(m); err == nil {
		t.Fatal("重复 mid 应该报错（主键约束）")
	}
}
