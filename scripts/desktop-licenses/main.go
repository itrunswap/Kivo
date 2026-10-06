// desktop-licenses 从 Go 模块缓存收集许可原文，桌面分发包必须携带这些声明。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type module struct {
	Path, Version, Dir string
	Main               bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	command := exec.Command("go", "list", "-m", "-json", "all")
	command.Dir = "desktop"
	data, err := command.Output()
	if err != nil {
		return fmt.Errorf("读取桌面模块依赖: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	modules := []module{}
	for {
		var item module
		if err := decoder.Decode(&item); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if !item.Main && !strings.HasPrefix(item.Path, "github.com/itrunswap/Kivo") {
			modules = append(modules, item)
		}
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	var result strings.Builder
	result.WriteString("Kivo Desktop 第三方 Go 依赖许可原文\n由 scripts/desktop-licenses 自动收集；部分模块仅用于构建工具。\n界面设计、字体与 Go 工具链许可另见 THIRD_PARTY_NOTICES.md。\nKivo 自身许可仍由项目所有者另行指定。\n\n")
	for _, item := range modules {
		if item.Dir == "" {
			continue
		} // 未下载的模块不进入当前构建；不伪造其许可文本。
		entries, err := os.ReadDir(item.Dir)
		if err != nil {
			return err
		}
		files := []string{}
		for _, entry := range entries {
			upper := strings.ToUpper(entry.Name())
			if !entry.IsDir() && (strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "COPYING") || upper == "NOTICE") {
				files = append(files, entry.Name())
			}
		}
		if len(files) == 0 {
			return fmt.Errorf("模块 %s 缺少根目录许可文件，请人工检查", item.Path)
		}
		fmt.Fprintf(&result, "============================================================\n%s %s\n", item.Path, item.Version)
		for _, file := range files {
			content, err := os.ReadFile(filepath.Join(item.Dir, file))
			if err != nil {
				return err
			}
			fmt.Fprintf(&result, "\n--- %s ---\n%s\n", file, content)
		}
	}
	// 规范文件尾，不修改许可正文；保证重复构建不会制造无意义的空行差异。
	return os.WriteFile("desktop/THIRD_PARTY_LICENSES.txt", []byte(strings.TrimRight(result.String(), "\r\n")+"\n"), 0o644)
}
