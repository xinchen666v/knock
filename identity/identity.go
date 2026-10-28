package identity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xinchen666v/knock/crypto"
)

// DefaultPath 返回身份文件的默认位置：%AppData%/knock/identity.json（Win）
// 或 ~/.config/knock/identity.json（Linux/macOS，经 os.UserConfigDir）
func DefaultPath() (string, error) {
	// 测试/多账号场景：允许环境变量指定身份文件位置
	if p := os.Getenv("KNOCK_IDENTITY"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "knock", "identity.json"), nil
}

// LoadOrCreate 读身份；文件不存在则生成并写入。
// 返回 (身份, 是否新建)——调用方可以据此打"首次运行已生成身份"的日志
func LoadOrCreate(path string) (*crypto.Identity, bool, error) {
	// err 要在下面判断是不是"不存在"，所以不能声明在 if 的 init 里
	// （if-init 的变量出了这条 if 就没了）
	_, err := os.Stat(path)
	if err == nil {
		f, err := os.Open(path)
		if err != nil {
			return nil, false, err
		}
		defer f.Close()
		id, err := crypto.LoadIdentity(f)
		if err != nil {
			return nil, false, fmt.Errorf("身份文件损坏（拒绝覆盖，请人工处理 %s）: %w", path, err)
		}
		return id, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	// 不存在 → 生成 + 保存
	id, err := crypto.GenerateIdentity()
	if err != nil {
		return nil, false, err
	}
	if err := SaveToFile(path, id); err != nil {
		return nil, false, err
	}
	return id, true, nil
}

// SaveToFile 原子写：先写临时文件再 rename，中途断电不会留下半个身份文件
func SaveToFile(path string, id *crypto.Identity) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil { // 目录本身也要收紧权限
		return err
	}
	tmp, err := os.CreateTemp(dir, "identity-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if err := id.Save(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName) // 失败不留垃圾
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil { // 私钥文件仅本人可读
		os.Remove(tmpName)
		return err
	}
	// Windows 上 Go 的 os.Rename 使用 MoveFileEx(REPLACE_EXISTING)，覆盖已存在文件没问题
	return os.Rename(tmpName, path)
}
