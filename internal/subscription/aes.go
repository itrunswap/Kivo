// Package subscription 提供订阅内容的兼容性解码能力。
package subscription

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // 兼容既有订阅格式；不是密码散列或新加密设计。
	"encoding/base64"
	"errors"
	"fmt"
	"unicode/utf8"
)

// DecryptAES 解密密码保护的订阅内容。该兼容格式为：
//
//	Base64(16 字节 IV || AES-128-CBC-PKCS7(明文, MD5(密码)))
//
// MD5 仅用于复现既有格式的 16 字节密钥派生方式。新协议不应采用该方案；
// 此处也不把它描述成认证加密，因为 CBC 本身不提供完整性认证。
func DecryptAES(encoded []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("AES 解密密码不能为空")
	}
	compact := bytes.Join(bytes.Fields(encoded), nil)
	packed, err := base64.StdEncoding.DecodeString(string(compact))
	if err != nil {
		return nil, fmt.Errorf("订阅密文不是有效的 Base64: %w", err)
	}
	if len(packed) <= aes.BlockSize || (len(packed)-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, errors.New("订阅 AES 密文长度无效")
	}

	key := md5.Sum([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("初始化 AES: %w", err)
	}
	iv, ciphertext := packed[:aes.BlockSize], packed[aes.BlockSize:]
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	plaintext, err = unpadPKCS7(plaintext, aes.BlockSize)
	if err != nil {
		return nil, fmt.Errorf("AES 解密失败，请检查密码: %w", err)
	}
	if !utf8.Valid(plaintext) {
		return nil, errors.New("AES 解密结果不是有效的 UTF-8 文本，请检查密码")
	}
	return plaintext, nil
}

func unpadPKCS7(value []byte, blockSize int) ([]byte, error) {
	if len(value) == 0 || len(value)%blockSize != 0 {
		return nil, errors.New("PKCS#7 数据长度无效")
	}
	padding := int(value[len(value)-1])
	if padding < 1 || padding > blockSize || padding > len(value) {
		return nil, errors.New("PKCS#7 填充无效")
	}
	for _, item := range value[len(value)-padding:] {
		if int(item) != padding {
			return nil, errors.New("PKCS#7 填充无效")
		}
	}
	return value[:len(value)-padding], nil
}
