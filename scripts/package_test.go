package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageRelease(t *testing.T) {
	root := t.TempDir()
	input, output := filepath.Join(root, "input"), filepath.Join(root, "output")
	for _, dir := range []string{input, filepath.Join(root, "docs")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	documents := []string{"README.md", "docs/USER_MANUAL.md", "docs/COMMANDS.md", "docs/API.md", "docs/ARCHITECTURE.md", "docs/CODE_STYLE.md", "docs/WEB_DESIGN.md", "docs/RELEASE.md", "docs/TEST_REPORT_2026-10-06.md", "CHANGELOG.md", "THIRD_PARTY_NOTICES.md"}
	for _, name := range documents {
		if err := os.WriteFile(filepath.Join(root, name), []byte("中文说明\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range releaseTargets {
		name := "kivo-" + target
		if strings.HasPrefix(target, "windows-") {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(input, name), []byte("程序-"+target), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// 验证输入目录里的秘密文件不会进入发行包。
	if err := os.WriteFile(filepath.Join(input, "config.json"), []byte("private-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := packageRelease(input, output, root); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(output, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(string(manifest)), "\n")) != 6 {
		t.Fatal("校验清单必须包含六个发行包")
	}
	for _, target := range releaseTargets {
		t.Run(target, func(t *testing.T) {
			name := assetName(target)
			data, err := os.ReadFile(filepath.Join(output, name))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(manifest), fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)) {
				t.Fatal("校验值不匹配")
			}
			files := map[string][]byte{}
			program := "kivo"
			if strings.HasPrefix(target, "windows-") {
				program += ".exe"
				reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range reader.File {
					file, err := entry.Open()
					if err != nil {
						t.Fatal(err)
					}
					files[entry.Name], err = io.ReadAll(file)
					_ = file.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			} else {
				reader, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				archive := tar.NewReader(reader)
				for {
					entry, err := archive.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if entry.Name == program && entry.Mode != 0755 {
						t.Fatal("macOS/Linux 程序丢失执行权限")
					}
					files[entry.Name], err = io.ReadAll(archive)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(files) != len(documents)+1 || string(files[program]) != "程序-"+target {
				t.Fatal("程序平台或白名单文件数量不正确")
			}
			for _, doc := range documents {
				if string(files[doc]) != "中文说明\n" {
					t.Fatalf("文档 %s 缺失或内容不正确", doc)
				}
			}
		})
	}
	if err := packageRelease(input, output, root); err == nil {
		t.Fatal("不应覆盖已有资产")
	}
	again := filepath.Join(root, "repeat")
	if err := packageRelease(input, again, root); err != nil {
		t.Fatal(err)
	}
	repeated, err := os.ReadFile(filepath.Join(again, "SHA256SUMS"))
	if err != nil || !bytes.Equal(manifest, repeated) {
		t.Fatal("相同输入应产生相同校验值")
	}
}

func TestPackageReleaseMissingInput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "output")
	if err := packageRelease(root, output, root); err == nil {
		t.Fatal("缺失文件时必须失败")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("预检查失败时不应生成输出目录")
	}
}
