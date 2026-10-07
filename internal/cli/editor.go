package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/itrunswap/Kivo/internal/platform"
)

var errShellExit = errors.New("退出交互终端")

// commandSuggestion 描述交互终端中的一条可发现命令。
type commandSuggestion struct {
	command     string
	description string
	aliases     []string
	priority    int
}

var commandCatalog = []commandSuggestion{
	{command: "/connect", description: "启动内核、接入系统代理并检测外网", priority: 105},
	{command: "/disconnect", description: "恢复本程序管理的系统代理并停止内核", priority: 104},
	{command: "/system-proxy status", description: "查看接入、接管及待恢复备份", priority: 103},
	{command: "/system-proxy on", description: "接入系统代理；要求内核已运行", priority: 102},
	{command: "/system-proxy off", description: "恢复原系统代理，保持内核运行", priority: 101},
	{command: "/system-proxy recover", description: "安全恢复异常退出的代理备份；不启动内核", priority: 100},
	{command: "/status", description: "查看内核、节点、端口和订阅状态", aliases: []string{"/st"}, priority: 110},
	{command: "/proxy check", description: "检测直连、日常入口和节点出口能否上网", priority: 99},
	{command: "/proxy setup", description: "查看系统/浏览器接入和安全停用步骤", priority: 98},
	{command: "/proxy status", description: "查看代理服务、系统接入及联网检测状态", priority: 97},
	{command: "/install mihomo", description: "安装最新版本 Mihomo", priority: 95},
	{command: "/core start", description: "启动代理内核", priority: 90},
	{command: "/core stop", description: "停止代理内核", priority: 89},
	{command: "/core restart", description: "重启代理内核", priority: 88},
	{command: "/core status", description: "查看代理内核详细状态", priority: 87},
	{command: "/core list", description: "列出已安装的内核版本", priority: 86},
	{command: "/core install", description: "安装最新内核", priority: 85},
	{command: "/core update", description: "安装最新或指定内核", priority: 84},
	{command: "/core use", description: "按序号或版本切换内核", priority: 85},
	{command: "/core switch", description: "切换内核（use 的别名）", priority: 84},
	{command: "/core import", description: "导入本地官方安装包", priority: 84},
	{command: "/core uninstall", description: "删除一个非活动内核版本", priority: 83},
	{command: "/core remove", description: "删除一个非活动内核版本", priority: 82},
	{command: "/core purge --yes", description: "停止并删除全部受管内核", priority: 81},
	{command: "/proxy on", description: "启动代理内核快捷命令", priority: 80},
	{command: "/proxy off", description: "停止代理内核快捷命令", priority: 79},
	{command: "/proxy restart", description: "重启代理内核快捷命令", priority: 78},
	{command: "/sub add", description: "交互添加订阅地址和认证信息", priority: 85},
	{command: "/sub list", description: "查看已配置订阅", priority: 84},
	{command: "/sub show", description: "查看指定订阅详情", priority: 83},
	{command: "/sub edit", description: "修改指定订阅", priority: 82},
	{command: "/sub update", description: "更新全部活动订阅", priority: 83},
	{command: "/sub update all --direct", description: "直连更新全部活动订阅", priority: 82},
	{command: "/sub update all --proxy", description: "通过当前代理更新全部活动订阅", priority: 81},
	{command: "/sub update group", description: "更新指定订阅分组", priority: 80},
	{command: "/sub test", description: "检查全部活动订阅", priority: 82},
	{command: "/sub test all --direct", description: "直连检查全部活动订阅", priority: 81},
	{command: "/sub test all --proxy", description: "通过当前代理检查全部订阅", priority: 80},
	{command: "/sub remove", description: "删除指定订阅", priority: 82},
	{command: "/sub enable", description: "启用指定订阅", priority: 81},
	{command: "/sub disable", description: "禁用指定订阅", priority: 80},
	{command: "/sub move", description: "把订阅移动到另一分组", priority: 79},
	{command: "/sub group list", description: "查看订阅分组", priority: 78},
	{command: "/sub group create", description: "创建订阅分组", priority: 77},
	{command: "/sub group use", description: "独占使用一个订阅分组", priority: 76},
	{command: "/sub group enable", description: "启用一个订阅分组", priority: 75},
	{command: "/sub group disable", description: "禁用一个订阅分组", priority: 74},
	{command: "/sub group rename", description: "重命名订阅分组并更新订阅引用", priority: 73},
	{command: "/sub group remove", description: "删除未被引用的订阅分组", priority: 73},
	{command: "/node list", description: "列出节点，可追加关键词筛选", priority: 80},
	{command: "/node test", description: "测试全部节点延迟", priority: 79},
	{command: "/node use", description: "选择一个代理节点", priority: 78},
	{command: "/mode rule", description: "使用规则模式", priority: 70},
	{command: "/mode global", description: "使用全局代理模式", priority: 69},
	{command: "/mode direct", description: "使用直连模式", priority: 68},
	{command: "/route profile list", description: "查看路由配置", priority: 67},
	{command: "/route restore", description: "恢复缺失的内置路由方案和规则组", priority: 67},
	{command: "/route profile use", description: "切换路由配置", priority: 66},
	{command: "/route profile create", description: "创建路由配置", priority: 65},
	{command: "/route profile attach", description: "给路由配置挂载规则组", priority: 64},
	{command: "/route profile detach", description: "从路由配置移除规则组", priority: 63},
	{command: "/route profile remove", description: "删除非活动路由配置", priority: 62},
	{command: "/route group list", description: "查看规则组", priority: 65},
	{command: "/route group create", description: "创建规则组", priority: 64},
	{command: "/route group remove", description: "删除未被引用的规则组", priority: 63},
	{command: "/route rule list", description: "查看规则组内的规则", priority: 65},
	{command: "/route rule add", description: "向规则组添加路由规则", priority: 64},
	{command: "/route rule edit", description: "修改指定序号的路由规则", priority: 64},
	{command: "/route rule move", description: "调整规则组内规则顺序", priority: 64},
	{command: "/route rule remove", description: "按序号删除路由规则", priority: 63},
	{command: "/port", description: "查看或设置混合代理端口", priority: 63},
	{command: "/tun status", description: "查看 TUN 状态", priority: 62},
	{command: "/tun on", description: "开启 TUN", priority: 61},
	{command: "/tun off", description: "关闭 TUN", priority: 60},
	{command: "/lan status", description: "查看局域网代理访问状态", priority: 62},
	{command: "/lan on", description: "允许局域网访问代理端口", priority: 61},
	{command: "/lan off", description: "禁止局域网访问代理端口", priority: 60},
	{command: "/config show", description: "查看公开运行配置", priority: 66},
	{command: "/config validate", description: "验证内核、订阅、端口和权限", priority: 65},
	{command: "/config download-proxy", description: "查看或设置内核下载代理", priority: 64},
	{command: "/config download-retry", description: "设置内核下载重试次数", priority: 63},
	{command: "/doctor", description: "检查内核、订阅、端口和权限", priority: 65},
	{command: "/logs", description: "查看最近内核日志", priority: 60},
	{command: "/web status", description: "查询 Web 控制服务状态", priority: 58},
	{command: "/web start", description: "启动 Web 控制服务", priority: 57},
	{command: "/web stop", description: "关闭 Web 控制服务", priority: 56},
	{command: "/web restart", description: "重新启动 Web 控制服务", priority: 56},
	{command: "/web --show-token", description: "显示管理页面地址和登录密钥", priority: 55},
	{command: "/web token", description: "显示管理页面地址和登录密钥", priority: 54},
	{command: "/shutdown", description: "停止代理与 Core、关闭 Web，然后退出 CLI", priority: 54},
	{command: "/help", description: "查看所有命令和用法", aliases: []string{"/?"}, priority: 50},
	{command: "/clear", description: "清空当前终端", priority: 40},
	{command: "/quit", description: "退出 CLI，后台代理保持运行", aliases: []string{"/q", "/exit"}, priority: 30},
}

const (
	// suggestionViewportRows 是候选区固定占用的行数。固定高度可避免用户输入时
	// 提示符上下跳动，同时让方向键滚动拥有稳定的视觉参照。
	suggestionViewportRows = 8
	editorRowsAboveInput   = suggestionViewportRows + 1 // 候选区 + 上边框。
	editorFrameRows        = suggestionViewportRows + 3 // 候选区 + 上边框 + 输入行 + 下边框。
)

// matchingSuggestions 根据当前输入做前缀过滤。别名参与查询，但始终展示规范命令。
func matchingSuggestions(input string, limit int) []commandSuggestion {
	query := strings.ToLower(strings.TrimSpace(input))
	if !strings.HasPrefix(query, "/") || strings.Contains(query, "\n") {
		return nil
	}
	matches := make([]commandSuggestion, 0, len(commandCatalog))
	for _, item := range commandCatalog {
		matched := strings.HasPrefix(strings.ToLower(item.command), query)
		if !matched {
			matched = slices.ContainsFunc(item.aliases, func(alias string) bool {
				return strings.HasPrefix(strings.ToLower(alias), query)
			})
		}
		if matched {
			matches = append(matches, item)
		}
	}
	slices.SortStableFunc(matches, func(left, right commandSuggestion) int {
		return right.priority - left.priority
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// matchingSuggestions 将运行期节点和订阅加入参数补全。动态数据只使用最近一次
// list 结果，避免每次按键都发起网络请求；执行 /node list 或 /sub list 即可刷新。
func (s *Shell) matchingSuggestions(input string, limit int) []commandSuggestion {
	lower := strings.ToLower(strings.TrimSpace(input))
	dynamic := []commandSuggestion{}
	if strings.HasPrefix(lower, "/node use ") {
		query := strings.TrimSpace(strings.TrimPrefix(lower, "/node use "))
		for index, node := range s.cachedNodes {
			if query != "" && !strings.Contains(strings.ToLower(node.Name+" "+node.ProviderName), query) {
				continue
			}
			dynamic = append(dynamic, commandSuggestion{command: "/node use " + strconv.Quote(node.Name), description: fmt.Sprintf("#%d · %s · %d ms", index+1, node.ProviderName, node.Delay), priority: 200 - index})
		}
	} else {
		for _, action := range []string{"update", "test", "remove", "enable", "disable", "edit", "show", "move"} {
			prefix := "/sub " + action + " "
			if !strings.HasPrefix(lower, prefix) {
				continue
			}
			query := strings.TrimSpace(strings.TrimPrefix(lower, prefix))
			for index, sub := range s.cachedSubscriptions {
				if query != "" && !strings.Contains(strings.ToLower(sub.Name+" "+sub.Group), query) {
					continue
				}
				dynamic = append(dynamic, commandSuggestion{command: prefix + strconv.Quote(sub.Name), description: fmt.Sprintf("#%d · %s", index+1, sub.Group), priority: 200 - index})
			}
		}
	}
	if len(dynamic) > 0 {
		if limit > 0 && len(dynamic) > limit {
			dynamic = dynamic[:limit]
		}
		return dynamic
	}
	return matchingSuggestions(input, limit)
}

// readInteractiveLine 在真实终端中提供即时命令发现；重定向输入时自动回退。
func (s *Shell) readInteractiveLine(ctx context.Context) (string, error) {
	s.interactive = false
	input, inputOK := s.inputFile()
	output, outputOK := s.outputFile()
	if !inputOK || !outputOK {
		return s.readFallbackLine()
	}
	restore, interactive, err := platform.StartRawMode(input, output)
	if err != nil || !interactive {
		return s.readFallbackLine()
	}
	defer restore()
	s.interactive = true

	value := []rune{}
	cursor := 0
	selected := 0
	historyIndex := len(s.history)
	historyDraft := []rune{}
	historyMode := false
	previousRows := 0
	previousRows = s.redrawEditor(string(value), cursor, nil, selected, previousRows)

	leaveHistoryMode := func() {
		historyMode = false
		historyIndex = len(s.history)
		historyDraft = nil
	}
	navigateHistory := func(direction int) {
		value, historyIndex, historyDraft, historyMode = navigateCommandHistory(
			s.history, value, historyIndex, historyDraft, historyMode, direction,
		)
		cursor = len(value)
		selected = 0
	}
	for {
		select {
		case <-ctx.Done():
			s.finishEditor(string(value), previousRows)
			return "", ctx.Err()
		default:
		}

		char, _, readErr := s.reader.ReadRune()
		if readErr != nil {
			s.finishEditor(string(value), previousRows)
			return "", readErr
		}
		// 获取全部匹配项；固定的 8 行视窗只负责展示，不应截断可选择范围。
		suggestions := s.matchingSuggestions(string(value), 0)
		switch char {
		case '\r', '\n':
			s.finishEditor(string(value), previousRows)
			return strings.TrimSpace(string(value)), nil
		case 3: // Ctrl+C
			s.finishEditor(string(value), previousRows)
			return "", errShellExit
		case 4: // Ctrl+D
			if len(value) == 0 {
				s.finishEditor(string(value), previousRows)
				return "", io.EOF
			}
		case 1: // Ctrl+A
			cursor = 0
		case 5: // Ctrl+E
			cursor = len(value)
		case '\t':
			if len(suggestions) > 0 {
				if selected >= len(suggestions) {
					selected = 0
				}
				value = []rune(suggestions[selected].command + " ")
				cursor = len(value)
				selected = 0
				leaveHistoryMode()
			}
		case 8, 127:
			value, cursor = deleteRuneBefore(value, cursor)
			selected = 0
			leaveHistoryMode()
		case 27: // ANSI 方向键、Home、End 和 Delete 序列。
			key := s.readEscapeKey()
			if (key == keyUp || key == keyDown) && (historyMode || len(suggestions) == 0) {
				direction := 1
				if key == keyUp {
					direction = -1
				}
				navigateHistory(direction)
			} else {
				value, cursor, selected = applyEditorKey(key, value, cursor, selected, suggestions)
				if key == keyDelete {
					leaveHistoryMode()
				}
			}
		case 0, 224: // 不支持 VT 输入的旧 Windows Console 扩展键前缀。
			key := s.readWindowsExtendedKey()
			if (key == keyUp || key == keyDown) && (historyMode || len(suggestions) == 0) {
				direction := 1
				if key == keyUp {
					direction = -1
				}
				navigateHistory(direction)
			} else {
				value, cursor, selected = applyEditorKey(key, value, cursor, selected, suggestions)
				if key == keyDelete {
					leaveHistoryMode()
				}
			}
		default:
			if char >= 32 && char != utf8.RuneError {
				value, cursor = insertRuneAt(value, cursor, char)
				selected = 0
				leaveHistoryMode()
			}
		}
		visibleSuggestions := s.matchingSuggestions(string(value), 0)
		if historyMode {
			visibleSuggestions = nil
		}
		previousRows = s.redrawEditor(string(value), cursor, visibleSuggestions, selected, previousRows)
	}
}

// navigateCommandHistory 在历史命令和当前未执行的草稿之间移动。
// direction < 0 表示更旧的命令，direction > 0 表示更新的命令。
// 移动到最新历史之后会恢复用户在进入历史模式前的输入。
func navigateCommandHistory(history []string, current []rune, index int, draft []rune, active bool, direction int) ([]rune, int, []rune, bool) {
	if len(history) == 0 || direction == 0 {
		return current, index, draft, active
	}
	if !active {
		draft = slices.Clone(current)
		index = len(history)
		active = true
	}
	if direction < 0 {
		if index > 0 {
			index--
		}
		return []rune(history[index]), index, draft, active
	}
	if index < len(history)-1 {
		index++
		return []rune(history[index]), index, draft, active
	}
	return slices.Clone(draft), len(history), draft, false
}

type editorKey int

const (
	keyUnknown editorKey = iota
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyDelete
)

func (s *Shell) readEscapeKey() editorKey {
	second, _, err := s.reader.ReadRune()
	if err != nil {
		return keyUnknown
	}
	if second == 'O' {
		third, _, readErr := s.reader.ReadRune()
		if readErr != nil {
			return keyUnknown
		}
		if third == 'H' {
			return keyHome
		}
		if third == 'F' {
			return keyEnd
		}
		return keyUnknown
	}
	if second != '[' {
		return keyUnknown
	}
	third, _, err := s.reader.ReadRune()
	if err != nil {
		return keyUnknown
	}
	switch third {
	case 'A':
		return keyUp
	case 'B':
		return keyDown
	case 'C':
		return keyRight
	case 'D':
		return keyLeft
	case 'H':
		return keyHome
	case 'F':
		return keyEnd
	}
	if third >= '0' && third <= '9' {
		sequence := []rune{third}
		for len(sequence) < 6 {
			next, _, readErr := s.reader.ReadRune()
			if readErr != nil {
				return keyUnknown
			}
			if next == '~' {
				switch string(sequence) {
				case "1", "7":
					return keyHome
				case "3":
					return keyDelete
				case "4", "8":
					return keyEnd
				}
				return keyUnknown
			}
			if next < '0' || next > '9' {
				return keyUnknown
			}
			sequence = append(sequence, next)
		}
	}
	return keyUnknown
}

func (s *Shell) readWindowsExtendedKey() editorKey {
	code, _, err := s.reader.ReadRune()
	if err != nil {
		return keyUnknown
	}
	switch code {
	case 72:
		return keyUp
	case 80:
		return keyDown
	case 75:
		return keyLeft
	case 77:
		return keyRight
	case 71:
		return keyHome
	case 79:
		return keyEnd
	case 83:
		return keyDelete
	default:
		return keyUnknown
	}
}

func applyEditorKey(key editorKey, value []rune, cursor, selected int, suggestions []commandSuggestion) ([]rune, int, int) {
	switch key {
	case keyUp:
		if len(suggestions) > 0 {
			selected = (selected - 1 + len(suggestions)) % len(suggestions)
		}
	case keyDown:
		if len(suggestions) > 0 {
			selected = (selected + 1) % len(suggestions)
		}
	case keyLeft:
		if cursor > 0 {
			cursor--
		}
	case keyRight:
		if cursor < len(value) {
			cursor++
		}
	case keyHome:
		cursor = 0
	case keyEnd:
		cursor = len(value)
	case keyDelete:
		value = deleteRuneAt(value, cursor)
	}
	return value, cursor, selected
}

func insertRuneAt(value []rune, cursor int, char rune) ([]rune, int) {
	cursor = clampCursor(cursor, len(value))
	value = append(value, 0)
	copy(value[cursor+1:], value[cursor:])
	value[cursor] = char
	return value, cursor + 1
}

func deleteRuneBefore(value []rune, cursor int) ([]rune, int) {
	cursor = clampCursor(cursor, len(value))
	if cursor == 0 {
		return value, cursor
	}
	copy(value[cursor-1:], value[cursor:])
	return value[:len(value)-1], cursor - 1
}

func deleteRuneAt(value []rune, cursor int) []rune {
	cursor = clampCursor(cursor, len(value))
	if cursor == len(value) {
		return value
	}
	copy(value[cursor:], value[cursor+1:])
	return value[:len(value)-1]
}

func clampCursor(cursor, size int) int {
	if cursor < 0 {
		return 0
	}
	if cursor > size {
		return size
	}
	return cursor
}

// suggestionViewport 从全部候选中截取固定高度的可见窗口，并返回选中项在
// 可见窗口中的索引和窗口起始位置。选中项越过边缘时，窗口会随之滚动。
func suggestionViewport(items []commandSuggestion, selected, size int) ([]commandSuggestion, int, int) {
	if len(items) == 0 || size <= 0 {
		return nil, 0, 0
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(items) {
		selected = len(items) - 1
	}
	start := 0
	if selected >= size {
		start = selected - size + 1
	}
	if maxStart := len(items) - size; start > maxStart && maxStart > 0 {
		start = maxStart
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], selected - start, start
}

// redrawEditor 重绘固定高度的候选区、上下边框和输入行。previousRows 为 0
// 表示首次绘制，需要先预留完整帧；后续调用时光标应位于输入行。
func (s *Shell) redrawEditor(value string, cursor int, suggestions []commandSuggestion, selected, previousRows int) int {
	// 将一帧重绘包在隐藏光标范围内，减少较慢的远程终端产生闪烁。
	fmt.Fprint(s.out, "\x1b[?25l")
	defer fmt.Fprint(s.out, "\x1b[?25h")

	if previousRows == 0 {
		// 先为整帧预留空间，避免终端底部滚屏破坏后续的光标定位。
		fmt.Fprint(s.out, "\r"+strings.Repeat("\n", editorFrameRows-1))
		fmt.Fprintf(s.out, "\x1b[%dA\r", editorFrameRows-1)
	} else {
		fmt.Fprintf(s.out, "\r\x1b[%dA", editorRowsAboveInput)
	}

	contentWidth := s.editorContentWidth()
	commandColumn := 26
	if contentWidth < 32 {
		commandColumn = contentWidth
	} else if commandColumn > contentWidth-2 {
		commandColumn = contentWidth - 2
	}
	visible, visibleSelected, start := suggestionViewport(suggestions, selected, suggestionViewportRows)
	idleLines := s.renderStatusPanel(contentWidth, true)
	for row := 0; row < suggestionViewportRows; row++ {
		fmt.Fprint(s.out, "\r\x1b[2K")
		if row < len(visible) {
			item := visible[row]
			marker := "  "
			commandColor := cyan
			if row == visibleSelected {
				marker = "› "
				commandColor = purple + bold
			}
			commandText := truncateDisplay(marker+item.command, commandColumn)
			padding := commandColumn - displayWidth(commandText)
			if padding < 0 {
				padding = 0
			}
			line := s.paint(commandColor, commandText) + strings.Repeat(" ", padding)
			descriptionWidth := contentWidth - commandColumn
			if descriptionWidth > 4 {
				line += s.paint(dim, truncateDisplay(item.description, descriptionWidth))
			}
			fmt.Fprint(s.out, line)
		} else if len(suggestions) == 0 && row < len(idleLines) {
			fmt.Fprint(s.out, idleLines[row])
		}
		fmt.Fprint(s.out, "\n")
	}

	label := "/ 命令 · ↑↓ 历史 · ←→ 光标 · Tab 补全"
	if len(suggestions) > 0 {
		end := start + len(visible)
		label = fmt.Sprintf("命令 %d-%d/%d · ↑↓选择 · Tab补全", start+1, end, len(suggestions))
	}
	fmt.Fprint(s.out, "\r\x1b[2K"+s.paint(dim, horizontalRule(contentWidth, label))+"\n")

	// 先清空输入行并绘制下一行的下边框，再使用最基础的“上移一行”回到
	// 输入行。部分 Windows 控制台不支持 CSI s/u 保存、恢复光标；使用它们会
	// 让每次重绘向下漂移一行，最终把旧候选帧堆叠在屏幕上。
	fmt.Fprint(s.out, "\r\x1b[2K\n")
	fmt.Fprint(s.out, "\r\x1b[2K"+s.paint(dim, horizontalRule(contentWidth, "")))
	prompt := "> "
	inputWidth := contentWidth - displayWidth(prompt)
	visibleInput, cursorColumn := editorInputViewport(value, cursor, inputWidth)
	fmt.Fprint(s.out, "\x1b[1A\r\x1b[2K"+s.paint(dim, prompt)+visibleInput)
	if tailWidth := displayWidth(visibleInput) - cursorColumn; tailWidth > 0 {
		fmt.Fprintf(s.out, "\x1b[%dD", tailWidth)
	}
	return editorRowsAboveInput
}

// editorInputViewport 把过长输入裁剪为单行窗口，并返回光标在该窗口中的显示列。
// 光标移动按 rune 处理，因此中文不会被拆成半个 UTF-8 字符。
func editorInputViewport(value string, cursor, maxWidth int) (string, int) {
	if maxWidth <= 0 {
		return "", 0
	}
	runes := []rune(value)
	cursor = clampCursor(cursor, len(runes))
	start := 0
	for start < cursor && displayWidth(string(runes[start:cursor])) > maxWidth {
		start++
	}
	end, width := start, 0
	for end < len(runes) {
		charWidth := runeDisplayWidth(runes[end])
		if width+charWidth > maxWidth {
			break
		}
		width += charWidth
		end++
	}
	if cursor > end {
		end = cursor
	}
	return string(runes[start:end]), displayWidth(string(runes[start:cursor]))
}

// finishEditor 清理状态/候选帧，只保留最终输入；避免每个命令都重复状态和边框。
func (s *Shell) finishEditor(value string, previousRows int) {
	if previousRows == 0 {
		fmt.Fprintln(s.out, s.paint(dim, "> ")+value)
		return
	}
	fmt.Fprint(s.out, "\x1b[?25l")
	defer fmt.Fprint(s.out, "\x1b[?25h")
	fmt.Fprintf(s.out, "\r\x1b[%dA", editorRowsAboveInput)
	for row := 0; row < editorFrameRows; row++ {
		fmt.Fprint(s.out, "\r\x1b[2K")
		if row < editorFrameRows-1 {
			fmt.Fprint(s.out, "\n")
		}
	}
	fmt.Fprintf(s.out, "\x1b[%dA\r", editorFrameRows-1)
	fmt.Fprintln(s.out, s.paint(dim, "> ")+value)
}

// editorContentWidth 返回安全的绘制宽度。保留最后一列可防止部分终端在写满
// 最后一列时自动换行，造成候选区结构错乱。
func (s *Shell) editorContentWidth() int {
	terminalWidth := 80
	if output, ok := s.outputFile(); ok {
		if detected := platform.TerminalWidth(output); detected > 0 {
			terminalWidth = detected
		}
	}
	if terminalWidth <= 1 {
		return 1
	}
	return terminalWidth - 1
}

// horizontalRule 创建与终端内容宽度一致的横线，并可在横线中嵌入状态文字。
func horizontalRule(width int, label string) string {
	if width <= 0 {
		return ""
	}
	if label == "" || width < 6 {
		return strings.Repeat("─", width)
	}
	label = truncateDisplay(label, width-4)
	prefix := "─ " + label + " "
	remaining := width - displayWidth(prefix)
	if remaining < 0 {
		remaining = 0
	}
	return prefix + strings.Repeat("─", remaining)
}

// displayWidth 计算终端中的实际显示列数。中日韩字符和常见 Emoji 占两列，组合字符不占列。
func displayWidth(value string) int {
	width := 0
	for _, char := range value {
		width += runeDisplayWidth(char)
	}
	return width
}

func runeDisplayWidth(char rune) int {
	if char == 0 || char < 32 || (char >= 0x7f && char < 0xa0) {
		return 0
	}
	if unicode.Is(unicode.Mn, char) || unicode.Is(unicode.Me, char) || unicode.Is(unicode.Cf, char) {
		return 0
	}
	// 这些区间覆盖 CJK、全角标点、Hangul 和终端通常按双列显示的 Emoji。
	if char >= 0x1100 && (char <= 0x115f ||
		char == 0x2329 || char == 0x232a ||
		(char >= 0x2e80 && char <= 0xa4cf && char != 0x303f) ||
		(char >= 0xac00 && char <= 0xd7a3) ||
		(char >= 0xf900 && char <= 0xfaff) ||
		(char >= 0xfe10 && char <= 0xfe19) ||
		(char >= 0xfe30 && char <= 0xfe6f) ||
		(char >= 0xff00 && char <= 0xff60) ||
		(char >= 0xffe0 && char <= 0xffe6) ||
		(char >= 0x1f300 && char <= 0x1faff) ||
		(char >= 0x20000 && char <= 0x3fffd)) {
		return 2
	}
	return 1
}

func truncateDisplay(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if displayWidth(value) <= maxWidth {
		return value
	}
	if maxWidth == 1 {
		return "…"
	}
	var result strings.Builder
	used := 0
	for _, char := range value {
		charWidth := runeDisplayWidth(char)
		if used+charWidth > maxWidth-1 {
			break
		}
		result.WriteRune(char)
		used += charWidth
	}
	result.WriteRune('…')
	return result.String()
}

func (s *Shell) readFallbackLine() (string, error) {
	fmt.Fprint(s.out, s.paint(dim, "\n> "))
	line, err := s.reader.ReadString('\n')
	return strings.TrimSpace(line), err
}

func (s *Shell) inputFile() (*os.File, bool) {
	file, ok := s.input.(*os.File)
	return file, ok
}

func (s *Shell) outputFile() (*os.File, bool) {
	file, ok := s.out.(*os.File)
	return file, ok
}

// recordHistory 只保存当前进程内的非重复命令。订阅 URL 可能包含 Token，
// 因此历史记录不落盘，退出 CLI 后自动清除。
func (s *Shell) recordHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" || (len(s.history) > 0 && s.history[len(s.history)-1] == line) {
		return
	}
	const limit = 100
	s.history = append(s.history, line)
	if len(s.history) > limit {
		s.history = slices.Clone(s.history[len(s.history)-limit:])
	}
}

func trimLastRune(value string) string {
	if value == "" {
		return value
	}
	_, size := utf8.DecodeLastRuneInString(value)
	return value[:len(value)-size]
}
