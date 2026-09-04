// protocol/envelope_test.go
package protocol

import (
	"encoding/json"
	"testing"
)

// 测试：构造 → 序列化 → 解析，一圈回来字段应该原样保留
func TestEnvelopeRoundTrip(t *testing.T) {
	msg, err := NewEnvelope(TypeSend, "q-123", "m-456", ChatMessage{Text: "你好"})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}

	// 序列化成字节（以后这一步的产物就是一帧 WebSocket 消息）
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	t.Logf("线上传输的样子: %s", data)  // 跑测试时打印出来亲眼看一下！

	// 解析回来
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if got.Type != TypeSend || got.QueueID != "q-123" || got.MsgID != "m-456" {
		t.Errorf("字段不匹配: got %+v", got)
	}

	// payload 解出来应该是原文
	var chat ChatMessage
	if err := got.DecodePayload(&chat); err != nil {
		t.Fatalf("payload 解析失败: %v", err)
	}
	if chat.Text != "你好" {
		t.Errorf("文本不匹配: got %q", chat.Text)
	}
}

// 测试：格式损坏的输入必须被拒绝，而不是 panic 或静默通过
func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("这不是json")); err == nil {
		t.Error("非JSON输入应该报错")
	}
	if _, err := Parse([]byte(`{"type":"SEND"}`)); err == nil {
		t.Error("缺版本号的输入应该报错")
	}
	if _, err := Parse([]byte(`{"v":99,"type":"SEND"}`)); err == nil {
		t.Error("未知版本号应该报错")
	}
}

// 测试：nil payload 的信封应该正常工作（NEW 命令就是这种）
func TestNilPayload(t *testing.T) {
	msg, err := NewEnvelope(TypeNew, "", "", nil)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if string(msg.Payload) != `{}` {
		t.Errorf("空payload应为{}, got %s", msg.Payload)
	}
}
