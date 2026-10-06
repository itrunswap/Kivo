package cli

import (
	"errors"
	"strings"
	"unicode"
)

// SplitArgs 将交互命令拆分为参数，支持单引号、双引号和反斜杠转义。
func SplitArgs(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false
	hasToken := false
	windowsPath := false
	runes := []rune(strings.TrimSpace(input))
	for index, char := range runes {
		if escaped {
			current.WriteRune(char)
			escaped = false
			hasToken = true
			continue
		}
		if char == '\\' && quote != '\'' {
			prefix := current.String()
			if len(prefix) == 2 && prefix[1] == ':' && ((prefix[0] >= 'A' && prefix[0] <= 'Z') || (prefix[0] >= 'a' && prefix[0] <= 'z')) {
				windowsPath = true
			}
			if current.Len() == 0 && index+1 < len(runes) && runes[index+1] == '\\' {
				windowsPath = true
			}
			// Windows 盘符和 UNC 路径中的反斜杠是目录分隔符，不是 shell 转义。
			// 普通参数仍支持转义空格/引号；未知转义保留原字符以支持正则表达式。
			if windowsPath || (index+1 < len(runes) && !unicode.IsSpace(runes[index+1]) && runes[index+1] != '\\' && runes[index+1] != '"' && runes[index+1] != '\'') {
				current.WriteRune(char)
			} else {
				escaped = true
			}
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			hasToken = true
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			hasToken = true
			continue
		}
		if unicode.IsSpace(char) {
			if hasToken {
				args = append(args, current.String())
				current.Reset()
				hasToken = false
				windowsPath = false
			}
			continue
		}
		current.WriteRune(char)
		hasToken = true
	}
	if escaped {
		return nil, errors.New("命令末尾存在未完成的转义")
	}
	if quote != 0 {
		return nil, errors.New("引号没有闭合")
	}
	if hasToken {
		args = append(args, current.String())
	}
	return args, nil
}
