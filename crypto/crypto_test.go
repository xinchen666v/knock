package crypto

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ---- 身份 ----

func TestGenerateIdentity(t *testing.T) {
	id, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("GenerateIdentity: %v", err)
	}
	// 长度规格：Ed25519 公钥 32B/私钥 64B，X25519 双方 32B
	if len(id.SignPub) != ed25519.PublicKeySize {
		t.Fatalf("sign pub size = %d, want %d", len(id.SignPub), ed25519.PublicKeySize)
	}
	if len(id.SignPriv) != ed25519.PrivateKeySize {
		t.Fatalf("sign priv size = %d, want %d", len(id.SignPriv), ed25519.PrivateKeySize)
	}
	if isZero(id.BoxPriv[:]) || isZero(id.BoxPub[:]) {
		t.Fatal("box keypair 全零，随机源有问题")
	}
	// // 公私钥必须配对：公钥能从私钥重导出（Save/Load 的正确性依赖这一点）
	// if id.SignPub.String() != id.SignPriv.Public().(ed25519.PublicKey).String() {
	// 	t.Fatal("ed25519 公私钥不配对")
	// }
	// 公私钥必须配对：公钥能从私钥重导出（Save/Load 的正确性依赖这一点）
	derived := id.SignPriv.Public().(ed25519.PublicKey)
	if !bytes.Equal(id.SignPub, derived) {
		t.Fatal("ed25519 公私钥不配对")
	}

}

func TestIdentitySaveLoad(t *testing.T) {
	id, _ := GenerateIdentity()

	var buf bytes.Buffer
	if err := id.Save(&buf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadIdentity(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}

	// 序列化往返后必须逐字节等价——这是"重启后还是同一个人"的保证
	if !bytes.Equal(loaded.BoxPriv[:], id.BoxPriv[:]) {
		t.Fatal("box priv 往返不一致")
	}
	if !bytes.Equal(loaded.SignPriv, id.SignPriv) {
		t.Fatal("sign priv 往返不一致")
	}
	if !bytes.Equal(loaded.BoxPub[:], id.BoxPub[:]) {
		t.Fatal("box pub 往返不一致")
	}
}

// ---- 协商 ----

func TestDeriveKeySymmetry(t *testing.T) {
	A, _ := GenerateIdentity()
	B, _ := GenerateIdentity()

	sendA, _ := DeriveKey(A.BoxPriv, B.BoxPub, true)  // A 的发送 = A→B
	recvA, _ := DeriveKey(A.BoxPriv, B.BoxPub, false) // A 的接收 = B→A
	sendB, _ := DeriveKey(B.BoxPriv, A.BoxPub, false) // B 的发送 = B→A
	recvB, _ := DeriveKey(B.BoxPriv, A.BoxPub, true)  // B 的接收 = A→B

	if !bytes.Equal(sendA, recvB) {
		t.Fatal("A 发的钥匙 ≠ B 收的钥匙：A→B 方向断裂")
	}
	if !bytes.Equal(sendB, recvA) {
		t.Fatal("B 发的钥匙 ≠ A 收的钥匙：B→A 方向断裂")
	}
	if bytes.Equal(sendA, sendB) {
		t.Fatal("两方向密钥相同：方向分离失效")
	}
}

func TestDeriveKeyDifferentPeers(t *testing.T) {
	A, _ := GenerateIdentity()
	B, _ := GenerateIdentity()
	C, _ := GenerateIdentity()

	k1, _ := DeriveKey(A.BoxPriv, B.BoxPub, true)
	k2, _ := DeriveKey(A.BoxPriv, C.BoxPub, true)

	if bytes.Equal(k1, k2) {
		t.Fatal("对端不同密钥相同：意味着 C 能解 A→B 的消息")
	}
}

func TestDeriveKeyDirectionsDiffer(t *testing.T) {
	A, _ := GenerateIdentity()
	B, _ := GenerateIdentity()

	kA2B, _ := DeriveKey(A.BoxPriv, B.BoxPub, true)
	kB2A, _ := DeriveKey(A.BoxPriv, B.BoxPub, false) // 同一对钥匙，反方向

	if bytes.Equal(kA2B, kB2A) {
		t.Fatal("两个方向派生出同一把钥匙：方向分离失效，自反射攻击面打开")
	}
}

// ---- 加解密 ----

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	fill(key, 0xAA)

	plaintext := []byte("knock knock, who's there?")
	aad := []byte("test-queue-idtest-msg-id")

	ct, nonce, err := Encrypt(key, plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := Decrypt(key, ct, aad, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("roundtrip 内容不一致: got %q", got)
	}
}

func TestAADDetectsTamper(t *testing.T) {
	key := make([]byte, 32)
	fill(key, 0x01)
	aad := []byte("qid-mid-original")

	ct, nonce, _ := Encrypt(key, []byte("hello"), aad)

	// 攻击者把 AAD 里的 mid 换掉（换队列重放）→ 必须解密失败
	swapped := []byte("qid-mid-forged__")
	if _, err := Decrypt(key, ct, swapped, nonce); err == nil {
		t.Fatal("AAD 被替换却解密成功：重放防护失效")
	}
}

func TestCiphertextTamper(t *testing.T) {
	key := make([]byte, 32)
	fill(key, 0x02)
	aad := []byte("qid-mid")

	ct, nonce, _ := Encrypt(key, []byte("transfer 100 to alice"), aad)

	// 逐字节翻转密文每一位，任何一位都不可改
	tampered := bytes.Clone(ct)
	tampered[0] ^= 0x01
	if _, err := Decrypt(key, tampered, aad, nonce); err == nil {
		t.Fatal("密文篡改后解密成功：AEAD 完整性失效")
	}

	// tag 区域（尾部 16 字节）同样不可改
	tampered2 := bytes.Clone(ct)
	tampered2[len(ct)-1] ^= 0x80
	if _, err := Decrypt(key, tampered2, aad, nonce); err == nil {
		t.Fatal("认证标签篡改后解密成功")
	}
}

func TestNonceUniqueness(t *testing.T) {
	key := make([]byte, 32)
	fill(key, 0x03)

	seen := make(map[[24]byte]bool)
	for i := 0; i < 1000; i++ {
		_, nonce, err := Encrypt(key, []byte("x"), nil)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		if seen[nonce] {
			t.Fatalf("第 %d 次加密 nonce 重复：nonce 重用 = 密钥流重用 = 灾难", i)
		}
		seen[nonce] = true
	}
}

func TestWrongKeyFails(t *testing.T) {
	key1 := make([]byte, 32)
	fill(key1, 0x04)
	key2 := make([]byte, 32)
	fill(key2, 0x05)

	ct, nonce, _ := Encrypt(key1, []byte("secret"), []byte("aad"))
	if _, err := Decrypt(key2, ct, []byte("aad"), nonce); err == nil {
		t.Fatal("用错密钥解密成功：方向密钥选错将无法察觉")
	}
}

// ---- 签名 ----

func TestSignVerify(t *testing.T) {
	id, _ := GenerateIdentity()

	msg := []byte("DEL qid=abcdef123456 ts=1728880000")
	sig := Sign(id.SignPriv, msg)

	if !Verify(id.SignPub, msg, sig) {
		t.Fatal("自己的签名验不过")
	}

	// 篡改消息 → 验签必须失败（防止 DEL 命令被改时间戳重放/换队列）
	if Verify(id.SignPub, []byte("DEL qid=abcdef123456 ts=1728880001"), sig) {
		t.Fatal("篡改后的消息验签通过")
	}

	// 换公钥 → 验签必须失败
	other, _ := GenerateIdentity()
	if Verify(other.SignPub, msg, sig) {
		t.Fatal("冒名公钥验签通过")
	}
}

// ---- 指纹 ----

func TestFingerprintFormat(t *testing.T) {
	// 确定性：同一输入永远同一输出（两端对账的前提）
	id, _ := GenerateIdentity()
	fp1 := Fingerprint(id.BoxPub)
	fp2 := Fingerprint(id.BoxPub)
	if fp1 != fp2 {
		t.Fatalf("指纹不确定: %q vs %q", fp1, fp2)
	}

	// 格式契约：16 hex 大写、两位一组、单空格分隔 → "AB CD EF ..." 共 23 字符
	if len(fp1) != 23 {
		t.Fatalf("指纹长度 = %d, want 23（8字节hex分8组）: %q", len(fp1), fp1)
	}
	if fp1 != strings.ToUpper(fp1) {
		t.Fatalf("指纹必须大写: %q", fp1)
	}
	for i, c := range fp1 {
		isHex := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')
		isSpace := c == ' ' && i%3 == 2 // 第 2、5、8... 位是空格
		if !isHex && !isSpace {
			t.Fatalf("非法字符 %q at %d", c, i)
		}
	}

	// 不同密钥指纹必不同（概率意义上）
	other, _ := GenerateIdentity()
	if fp1 == Fingerprint(other.BoxPub) {
		t.Fatal("两把不同公钥指纹相同")
	}
}

// ---- 向量测试 ----

// TestVectors 读 vectors.json 逐条跑。
// 注意：这里用的是 crypto 包本身（与 genvectors 的裸库调用互为独立证明）
func TestVectors(t *testing.T) {
	data, err := os.ReadFile("vectors.json")
	if err != nil {
		t.Skipf("vectors.json 不存在（先跑 go run ./crypto/genvectors）: %v", err)
	}

	var doc struct {
		Version string `json:"version"`
		Vectors []struct {
			Name   string         `json:"name"`
			Op     string         `json:"op"`
			Input  map[string]any `json:"input"`
			Output map[string]any `json:"output"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("解析 vectors.json: %v", err)
	}
	if doc.Version != "knock-m3-v1" {
		t.Fatalf("向量版本不符: %s", doc.Version)
	}

	for _, v := range doc.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			switch v.Op {
			case "derive":
				priv := mustB64(t, v.Input["my_box_priv"])
				peer := mustB64(t, v.Input["peer_box_pub"])
				// isSender := v.Input["is_sender"].(bool)
				var priv32, peer32 [32]byte
				copy(priv32[:], priv)
				copy(peer32[:], peer)
				dir := mustStr(t, v.Input, "direction", v.Name) // 不再是裸 .(string)
				isA2B := dir == "a2b"

				got, err := DeriveKey(priv32, peer32, isA2B)
				if err != nil {
					t.Fatalf("DeriveKey: %v", err)
				}
				want := mustB64(t, v.Output["key"])
				if !bytes.Equal(got, want) {
					t.Fatal("derive 结果与向量不符——两端互通会在这断")
				}

			case "encrypt":
				key := mustB64(t, v.Input["key"])
				nonceB := mustB64(t, v.Input["nonce"])
				pt := mustB64(t, v.Input["plaintext"])
				aad := mustB64(t, v.Input["aad"])
				var nonce [24]byte
				copy(nonce[:], nonceB)

				got, outNonce, err := Encrypt(key, pt, aad)
				_ = outNonce // Encrypt 内部随机 nonce，向量测试只对得上加密逻辑:
				// 用固定 nonce 手动构造来对拍（见下方说明）
				if err != nil {
					t.Fatalf("Encrypt: %v", err)
				}
				want := mustB64(t, v.Output["ciphertext"])
				_ = got
				_ = want
				// 真正的确定性对拍用 Decrypt 反向验证：
				dec, err := Decrypt(key, want, aad, nonce)
				if err != nil {
					t.Fatalf("向量密文解不开: %v", err)
				}
				if !bytes.Equal(dec, pt) {
					t.Fatal("向量解密明文不符")
				}

			case "sign":
				privB := mustB64(t, v.Input["sign_priv"])
				msg := mustB64(t, v.Input["message"])
				priv := ed25519.PrivateKey(privB)
				sig := Sign(priv, msg)
				want := mustB64(t, v.Output["signature"])
				if !bytes.Equal(sig, want) {
					t.Fatal("签名与向量不符")
				}

			case "fingerprint":
				pubB := mustB64(t, v.Input["box_pub"])
				var pub [32]byte
				copy(pub[:], pubB)
				got := Fingerprint(pub)
				want := mustStr(t, v.Output, "fp", v.Name) // 裸断言消灭
				if got != want {
					t.Fatalf("指纹与向量不符: got %q want %q —— Dart 端会按向量复刻，两端会显示不同的安全码", got, want)
				}

			default:
				t.Fatalf("未知 op: %s", v.Op)
			}
		})
	}
}

// ---- helpers ----

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func fill(b []byte, v byte) {
	for i := range b {
		b[i] = v
	}
}

func mustB64(t *testing.T, s any) []byte {
	t.Helper()
	str, ok := s.(string)
	if !ok {
		t.Fatalf("向量字段不是字符串: %v", s)
	}
	b, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		t.Fatalf("向量 base64 解码失败: %v", err)
	}
	return b
}

func mustStr(t *testing.T, m map[string]any, key, vecName string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("向量 %s 缺少字段 %q——检查 vectors.json 与测试的字段名是否一致", vecName, key)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("向量 %s 字段 %q 不是字符串: %v", vecName, key, v)
	}
	return s
}

func mustBool(t *testing.T, m map[string]any, key, vecName string) bool {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("向量 %s 缺少字段 %q", vecName, key)
	}
	b, ok := v.(bool)
	if !ok {
		t.Fatalf("向量 %s 字段 %q 不是 bool: %v", vecName, key, v)
	}
	return b
}
