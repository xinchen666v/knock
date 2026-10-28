package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// ---- 身份 ----
type Identity struct {
	SignPriv ed25519.PrivateKey
	SignPub  ed25519.PublicKey
	BoxPriv  [32]byte
	BoxPub   [32]byte
}

func GenerateIdentity() (*Identity, error) {
	id := &Identity{}
	if _, err := rand.Read(id.BoxPriv[:]); err != nil {
		return nil, err
	}
	boxPub, err := curve25519.X25519(id.BoxPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	copy(id.BoxPub[:], boxPub)

	// ed25519.GenerateKey 返回 (pub, priv, err)，注意别接反
	signPub, signPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	id.SignPub, id.SignPriv = signPub, signPriv
	return id, nil
}

// 序列化用 JSON 而不是自定义二进制格式：身份要持久化且将来可能迁移存储，
// JSON 的版本演进成本低。base64 编码由 json 的 []byte 处理自动完成
func (id *Identity) Save(w io.Writer) error {
	return json.NewEncoder(w).Encode(map[string][]byte{
		"v1_sign_priv": id.SignPriv,
		"v1_box_priv":  id.BoxPriv[:],
	})
}

func LoadIdentity(r io.Reader) (*Identity, error) {
	var raw map[string][]byte
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	id := &Identity{}
	if sp, ok := raw["v1_sign_priv"]; ok && len(sp) == ed25519.PrivateKeySize {
		id.SignPriv = ed25519.PrivateKey(sp)
		id.SignPub = id.SignPriv.Public().(ed25519.PublicKey)
	}
	if bp, ok := raw["v1_box_priv"]; ok && len(bp) == 32 {
		copy(id.BoxPriv[:], bp)
	}
	boxPub, err := curve25519.X25519(id.BoxPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	copy(id.BoxPub[:], boxPub)
	return id, nil
}

// ---- 协商 ----
// DeriveKey 派生指定方向的方向密钥。
// dirA2B = true  → 派生 A→B 方向密钥（A 用来加密发送，B 用来解密接收）
// dirA2B = false → 派生 B→A 方向密钥（B 用来加密发送，A 用来解密接收）
// "谁是 A"由角色决定（链接创建者/队列 owner），不在本函数参数里——
// 两端必须对"谁是A"达成一致，然后各自按需派生两个方向。
func DeriveKey(myBoxPriv [32]byte, peerBoxPub [32]byte, dirA2B bool) ([]byte, error) {
	shared, err := curve25519.X25519(myBoxPriv[:], peerBoxPub[:])
	if err != nil {
		return nil, err
	}
	info := []byte("knock-m3-v1:a2b")
	if !dirA2B {
		info = []byte("knock-m3-v1:b2a")
	}
	r := hkdf.New(sha256.New, shared, nil, info)
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// ---- 加解密 ----
func Encrypt(key, plaintext, aad []byte) (ciphertext []byte, nonce [24]byte, err error) {
	aead, err := chacha20poly1305.NewX(key) // NewX = XChaCha20-Poly1305，24 字节 nonce
	if err != nil {
		return nil, nonce, err
	}
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, nonce, err
	}
	// Seal 输出 = ciphertext || tag(16B)，tag 附在尾部，解密侧自动校验
	ciphertext = aead.Seal(nil, nonce[:], plaintext, aad)
	return ciphertext, nonce, nil
}

func Decrypt(key, ciphertext, aad []byte, nonce [24]byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce[:], ciphertext, aad)
	// 认证失败返回 error——调用方必须把解密失败当"消息被篡改"处理，不许静默吞掉
}

// ---- 签名 ----
func Sign(priv ed25519.PrivateKey, msg []byte) []byte {
	return ed25519.Sign(priv, msg)
}

func Verify(pub ed25519.PublicKey, msg, sig []byte) bool {
	return ed25519.Verify(pub, msg, sig)
}

// ---- 指纹 ----
func Fingerprint(boxPub [32]byte) string {
	sum := sha256.Sum256(boxPub[:])
	// 取前 8 字节 → 16 hex → 两两分组空格连接
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
