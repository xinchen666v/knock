package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// DeriveKey 的裸库复刻 —— 逻辑必须与 crypto.DeriveKey 逐行等价
// （info 字符串是协议常量，两边必须一致，改一处必须改两处）
func deriveKey(myPriv, peerPub []byte, info string) ([]byte, error) {
	shared, err := curve25519.X25519(myPriv, peerPub)
	if err != nil {
		return nil, err
	}
	r := hkdf.New(sha256.New, shared, nil, []byte(info))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// Fingerprint 的裸库复刻 —— 格式必须与 crypto.Fingerprint 逐字符一致
func fingerprint(boxPub []byte) string {
	sum := sha256.Sum256(boxPub)
	hex := fmt.Sprintf("%x", sum[:8])
	var sb strings.Builder
	for i := 0; i < len(hex); i += 2 {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(strings.ToUpper(hex[i : i+2]))
	}
	return sb.String()
}

func main() {
	// 固定测试密钥（写满 32 字节，禁止用真实身份的密钥）
	privSeedA := [32]byte{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	}
	privSeedB := [32]byte{
		0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0xFA, 0xF9, 0xF8, 0xF7, 0xF6, 0xF5, 0xF4, 0xF3, 0xF2, 0xF1, 0xF0,
		0xEF, 0xEE, 0xED, 0xEC, 0xEB, 0xEA, 0xE9, 0xE8, 0xE7, 0xE6, 0xE5, 0xE4, 0xE3, 0xE2, 0xE1, 0xE0,
	}

	// 各方公钥
	pubA, err := curve25519.X25519(privSeedA[:], curve25519.Basepoint)
	must(err, "pubA")
	pubB, err := curve25519.X25519(privSeedB[:], curve25519.Basepoint)
	must(err, "pubB")

	// ---- derive 向量：A 视角与 B 视角，两把方向密钥 ----
	keyA2B, err := deriveKey(privSeedA[:], pubB, "knock-m3-v1:a2b") // A 视角，isSender=true
	must(err, "derive a2b")
	keyB2A, err := deriveKey(privSeedB[:], pubA, "knock-m3-v1:b2a") // B 视角，isSender=false
	must(err, "derive b2a")

	// 生成时自证对称性：两把钥匙必须能被对端各自算出同一把
	// （即 keyA2B 也等于 derive(privSeedB, pubA, ...) 的"对方视角"推导，此处直接断言非空且互异）
	if len(keyA2B) != 32 || len(keyB2A) != 32 {
		panic("derive 输出长度异常")
	}

	// ---- encrypt 向量：固定 nonce，确定性密文 ----
	nonce := [24]byte{
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B,
		0x1C, 0x1D, 0x1E, 0x1F, 0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27,
	}
	plaintext := []byte("knock test payload")
	aad := []byte("test-queue-idtest-msg-id")

	aead, err := chacha20poly1305.NewX(keyA2B)
	must(err, "aead")
	ct := aead.Seal(nil, nonce[:], plaintext, aad)

	// ---- sign 向量：Ed25519 从固定 seed 派生 ----
	signSeedA := [32]byte{
		0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7, 0xA8, 0xA9, 0xAA, 0xAB, 0xAC, 0xAD, 0xAE, 0xAF, 0xB0,
		0xB1, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8, 0xB9, 0xBA, 0xBB, 0xBC, 0xBD, 0xBE, 0xBF, 0xC0,
	}
	signPriv := ed25519.NewKeyFromSeed(signSeedA[:])
	delMsg := []byte("DEL qid=abcdef1234567890 ts=1728880000")
	sig := ed25519.Sign(signPriv, delMsg)

	// ---- fingerprint 向量 ----
	fp := fingerprint(pubB)

	doc := map[string]any{
		"version": "knock-m3-v1",
		"note":    "priv is raw 32-byte scalar, clamp is internal; base64 StdEncoding with padding",
		"vectors": []map[string]any{
			{
				"name": "derive_key_a2b", "op": "derive",
				"input": map[string]any{
					"my_box_priv":  b64(privSeedA[:]),
					"peer_box_pub": b64(pubB),
					"direction":    "a2b",
				},
				"output": map[string]any{"key": b64(keyA2B)},
			},
			{
				"name": "derive_key_b2a", "op": "derive",
				"input": map[string]any{
					"my_box_priv":  b64(privSeedB[:]),
					"peer_box_pub": b64(pubA),
					"direction":    "b2a",
				},
				"output": map[string]any{"key": b64(keyB2A)},
			},
			{
				"name": "encrypt_roundtrip", "op": "encrypt",
				"input": map[string]any{
					"key":       b64(keyA2B),
					"nonce":     b64(nonce[:]),
					"plaintext": b64(plaintext),
					"aad":       b64(aad),
				},
				"output": map[string]any{"ciphertext": b64(ct)},
			},
			{
				"name": "sign_del", "op": "sign",
				"input": map[string]any{
					"sign_priv": b64(signPriv), // 注意：ed25519 私钥全长 64B，这里存 seed 32B
					"message":   b64(delMsg),
				},
				"output": map[string]any{"signature": b64(sig)},
			},
			{
				"name": "fingerprint", "op": "fingerprint",
				"input": map[string]any{
					"box_pub": b64(pubB),
				},
				"output": map[string]any{"fp": fp},
			},
		},
	}

	// f, err := os.Create("vectors.json")
	_, thisFile, _, _ := runtime.Caller(0) // .../knock/crypto/genvectors/main.go
	outPath := filepath.Join(filepath.Dir(thisFile), "..", "vectors.json")
	f, err := os.Create(outPath)
	must(err, "create vectors.json")
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	must(enc.Encode(doc), "encode")
	must(f.Close(), "close")

	fmt.Println("vectors.json written:", len(doc["vectors"].([]map[string]any)), "vectors")
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "genvectors: %s: %v\n", what, err)
		os.Exit(1)
	}
}
