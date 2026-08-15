// Package credentialcrypto 提供与 thirdpartysync 后端一致的账号密码加解密能力。
//
// 算法：AES-128-ECB + PKCS5 填充，加密结果为大写十六进制字符串。
// 密文也支持 Base64 格式输入（解密时自动识别）。
//
// 密钥通过环境变量 THIRDPARTYSYNC_AES_KEY 注入，或使用 New 显式传入，不要硬编码在源码中。
package credentialcrypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

const DefaultKeyEnvName = "THIRDPARTYSYNC_AES_KEY"

// Client 持有 AES-128 密钥，用于凭证加解密。
type Client struct {
	key []byte
}

// New 使用 16 字节 AES-128 密钥创建客户端。
// 不足 16 字节时尾部补 0x00，超过 16 字节时截取前 16 字节。
func New(key []byte) (*Client, error) {
	normalized, err := normalizeKey(key)
	if err != nil {
		return nil, err
	}
	return &Client{key: normalized}, nil
}

// NewFromEnv 从环境变量读取密钥并创建客户端。
func NewFromEnv() (*Client, error) {
	keyText := strings.TrimSpace(os.Getenv(DefaultKeyEnvName))
	if keyText == "" {
		return nil, fmt.Errorf("环境变量 %s 未设置", DefaultKeyEnvName)
	}
	return New([]byte(keyText))
}

// MustNewFromEnv 与 NewFromEnv 相同，失败时 panic。
func MustNewFromEnv() *Client {
	client, err := NewFromEnv()
	if err != nil {
		panic(err)
	}
	return client
}

func normalizeKey(key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("AES-128 密钥不能为空")
	}
	if len(key) >= aes.BlockSize {
		return append([]byte(nil), key[:aes.BlockSize]...), nil
	}
	padded := make([]byte, aes.BlockSize)
	copy(padded, key)
	return padded, nil
}

// Encrypt 加密凭证，返回大写十六进制密文。
func (c *Client) Encrypt(plaintext string) (string, error) {
	trimmed := strings.TrimSpace(plaintext)
	if trimmed == "" {
		return "", nil
	}
	return encryptAES128ECB(c.key, trimmed)
}

// Decrypt 解密凭证；不像密文或解密失败时返回错误。
func (c *Client) Decrypt(ciphertext string) (string, error) {
	trimmed := strings.TrimSpace(ciphertext)
	if trimmed == "" {
		return "", nil
	}
	if !LooksLikeEncrypted(trimmed) {
		return trimmed, nil
	}
	plain, err := decryptAES128ECB(c.key, trimmed)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(plain), nil
}

// TryDecrypt 尝试解密；不像密文或解密失败时原样返回。
func (c *Client) TryDecrypt(value string) string {
	plain, err := c.Decrypt(value)
	if err != nil {
		return strings.TrimSpace(value)
	}
	return plain
}

// LooksLikeEncrypted 判断文本是否像 AES 密文（十六进制或 Base64）。
func LooksLikeEncrypted(value string) bool {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return false
	}
	if looksLikeHexCipherText(normalized) {
		return true
	}
	if len(normalized) < 24 || len(normalized)%4 != 0 {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(normalized)
	return err == nil && len(decoded) >= aes.BlockSize && len(decoded)%aes.BlockSize == 0
}

type ecbEncrypter struct {
	block cipher.Block
}

func newECBEncrypter(block cipher.Block) cipher.BlockMode {
	return &ecbEncrypter{block: block}
}

func (e *ecbEncrypter) BlockSize() int { return e.block.BlockSize() }

func (e *ecbEncrypter) CryptBlocks(dst, src []byte) {
	if len(src)%e.block.BlockSize() != 0 {
		panic("crypto/cipher: input not full blocks")
	}
	if len(dst) < len(src) {
		panic("crypto/cipher: output smaller than input")
	}
	for len(src) > 0 {
		e.block.Encrypt(dst, src[:e.block.BlockSize()])
		src = src[e.block.BlockSize():]
		dst = dst[e.block.BlockSize():]
	}
}

type ecbDecrypter struct {
	block cipher.Block
}

func newECBDecrypter(block cipher.Block) cipher.BlockMode {
	return &ecbDecrypter{block: block}
}

func (d *ecbDecrypter) BlockSize() int { return d.block.BlockSize() }

func (d *ecbDecrypter) CryptBlocks(dst, src []byte) {
	if len(src)%d.block.BlockSize() != 0 {
		panic("crypto/cipher: input not full blocks")
	}
	if len(dst) < len(src) {
		panic("crypto/cipher: output smaller than input")
	}
	for len(src) > 0 {
		d.block.Decrypt(dst, src[:d.block.BlockSize()])
		src = src[d.block.BlockSize():]
		dst = dst[d.block.BlockSize():]
	}
}

func pkcs5Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

func pkcs5Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("PKCS5 填充数据为空")
	}
	padding := int(data[len(data)-1])
	if padding <= 0 || padding > len(data) {
		return nil, fmt.Errorf("PKCS5 填充无效")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, fmt.Errorf("PKCS5 填充无效")
		}
	}
	return data[:len(data)-padding], nil
}

func encryptAES128ECB(key []byte, plaintext string) (string, error) {
	if len(key) != aes.BlockSize {
		return "", fmt.Errorf("AES-128 密钥长度必须为 16 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}
	padded := pkcs5Pad([]byte(plaintext), block.BlockSize())
	encrypted := make([]byte, len(padded))
	newECBEncrypter(block).CryptBlocks(encrypted, padded)
	return strings.ToUpper(hex.EncodeToString(encrypted)), nil
}

func decryptAES128ECB(key []byte, ciphertext string) (string, error) {
	if len(key) != aes.BlockSize {
		return "", fmt.Errorf("AES-128 密钥长度必须为 16 字节")
	}
	raw, err := decodeCipherText(ciphertext)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return "", fmt.Errorf("AES 密文长度无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("创建 AES cipher 失败: %w", err)
	}
	decrypted := make([]byte, len(raw))
	newECBDecrypter(block).CryptBlocks(decrypted, raw)
	plain, err := pkcs5Unpad(decrypted)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func decodeCipherText(value string) ([]byte, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return nil, fmt.Errorf("密文为空")
	}
	if looksLikeHexCipherText(normalized) {
		return hex.DecodeString(normalized)
	}
	decoded, err := base64.StdEncoding.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("密文格式无效: %w", err)
	}
	return decoded, nil
}

func looksLikeHexCipherText(content string) bool {
	if len(content) < aes.BlockSize*2 || len(content)%2 != 0 {
		return false
	}
	for _, charValue := range content {
		if (charValue < '0' || charValue > '9') &&
			(charValue < 'a' || charValue > 'f') &&
			(charValue < 'A' || charValue > 'F') {
			return false
		}
	}
	return true
}
