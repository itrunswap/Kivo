package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"simple", `/node list`, []string{"/node", "list"}},
		{"double quote", `node use "香港 01"`, []string{"node", "use", "香港 01"}},
		{"single quote", `sub remove '主 线路'`, []string{"sub", "remove", "主 线路"}},
		{"escaped space", `node use HK\ 01`, []string{"node", "use", "HK 01"}},
		{"empty quote", `command "" end`, []string{"command", "", "end"}},
		{"windows path", `core import "E:\Program Files\mihomo.zip"`, []string{"core", "import", `E:\Program Files\mihomo.zip`}},
		{"windows bare path", `core import E:\Downloads\mihomo.zip`, []string{"core", "import", `E:\Downloads\mihomo.zip`}},
		{"windows UNC", `core import "\\server\share\mihomo.zip"`, []string{"core", "import", `\\server\share\mihomo.zip`}},
		{"regex escape", `command "^api\.example\.com$"`, []string{"command", `^api\.example\.com$`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := SplitArgs(test.input)
			if err != nil {
				t.Fatalf("SplitArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("SplitArgs() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestSplitArgsRejectsIncompleteInput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`node use "HK 01`, `node use HK\`} {
		if _, err := SplitArgs(input); err == nil {
			t.Fatalf("SplitArgs(%q) should fail", input)
		}
	}
}

func TestMatchingSuggestionsFiltersPrefixAndAlias(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		first string
	}{
		{input: "/", first: "/status"},
		{input: "/st", first: "/status"},
		{input: "/core s", first: "/core start"},
		{input: "/node t", first: "/node test"},
		{input: "/web st", first: "/web status"},
		{input: "/sh", first: "/shutdown"},
	}
	for _, test := range tests {
		matches := matchingSuggestions(test.input, 8)
		if len(matches) == 0 || matches[0].command != test.first {
			t.Fatalf("matchingSuggestions(%q) first = %#v, want %q", test.input, matches, test.first)
		}
	}
	if matches := matchingSuggestions("status", 8); len(matches) != 0 {
		t.Fatalf("non-slash input should not display suggestions: %#v", matches)
	}
}

func TestMatchingSuggestionsWithoutLimitReturnsFullCatalog(t *testing.T) {
	t.Parallel()
	matches := matchingSuggestions("/", 0)
	if len(matches) <= suggestionViewportRows {
		t.Fatalf("matchingSuggestions(\"/\", 0) returned %d items, want more than %d", len(matches), suggestionViewportRows)
	}
	foundQuit := false
	for _, item := range matches {
		if item.command == "/quit" {
			foundQuit = true
			break
		}
	}
	if !foundQuit {
		t.Fatal("full suggestions should contain late catalog entries such as /quit")
	}
}

func TestSuggestionViewportFollowsSelection(t *testing.T) {
	t.Parallel()
	items := matchingSuggestions("/", 0)
	tests := []struct {
		name          string
		selected      int
		wantStart     int
		wantSelection int
	}{
		{name: "first page", selected: 0, wantStart: 0, wantSelection: 0},
		{name: "scroll one row", selected: suggestionViewportRows, wantStart: 1, wantSelection: suggestionViewportRows - 1},
		{name: "last page", selected: len(items) - 1, wantStart: len(items) - suggestionViewportRows, wantSelection: suggestionViewportRows - 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			visible, selected, start := suggestionViewport(items, test.selected, suggestionViewportRows)
			if len(visible) != suggestionViewportRows {
				t.Fatalf("visible item count = %d, want %d", len(visible), suggestionViewportRows)
			}
			if start != test.wantStart || selected != test.wantSelection {
				t.Fatalf("viewport = (start %d, selected %d), want (%d, %d)", start, selected, test.wantStart, test.wantSelection)
			}
			if visible[selected].command != items[test.selected].command {
				t.Fatalf("visible selected item = %#v, want %#v", visible[selected], items[test.selected])
			}
		})
	}
}

func TestHorizontalRuleUsesRequestedDisplayWidth(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		width int
		label string
	}{
		{width: 79, label: "命令 9-16/72 · ↑↓选择 · Tab补全"},
		{width: 20, label: "输入 / 查看命令"},
		{width: 3, label: "窄"},
	} {
		if got := displayWidth(horizontalRule(test.width, test.label)); got != test.width {
			t.Fatalf("horizontalRule(%d, %q) width = %d, want %d", test.width, test.label, got, test.width)
		}
	}
}

func TestRedrawEditorAvoidsUnsupportedCursorSaveRestore(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	shell := &Shell{out: &output}
	shell.redrawEditor("/", 1, matchingSuggestions("/", 0), 0, 0)

	rendered := output.String()
	if strings.Contains(rendered, "\x1b[s") || strings.Contains(rendered, "\x1b[u") {
		t.Fatal("redrawEditor should not use terminal-dependent cursor save/restore sequences")
	}
	if !strings.Contains(rendered, "\x1b[1A\r\x1b[2K> /") {
		t.Fatal("redrawEditor should return from the lower border to the input row with relative cursor movement")
	}
}

func TestIdleEditorUsesReservedRowsInsteadOfShowingBlankBlock(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	shell := &Shell{out: &output}
	shell.redrawEditor("", 0, nil, 0, 0)

	rendered := output.String()
	for _, expected := range []string{"KIVO", "/web status", "/help", "↑↓ 历史"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("idle editor should contain %q", expected)
		}
	}
}

func TestEditorCursorEditingSupportsUnicode(t *testing.T) {
	t.Parallel()
	value := []rune("/状态")
	value, cursor := insertRuneAt(value, 1, '查')
	if got := string(value); got != "/查状态" || cursor != 2 {
		t.Fatalf("insert result=%q cursor=%d", got, cursor)
	}
	value, cursor = deleteRuneBefore(value, cursor)
	if got := string(value); got != "/状态" || cursor != 1 {
		t.Fatalf("backspace result=%q cursor=%d", got, cursor)
	}
	value = deleteRuneAt(value, cursor)
	if got := string(value); got != "/态" {
		t.Fatalf("delete result=%q", got)
	}
	value, cursor, _ = applyEditorKey(keyRight, value, cursor, 0, nil)
	if cursor != 2 {
		t.Fatalf("right cursor=%d, want 2", cursor)
	}
}

func TestEditorInputViewportKeepsCursorVisible(t *testing.T) {
	t.Parallel()
	visible, column := editorInputViewport("0123456789中文", 12, 8)
	if displayWidth(visible) > 8 || column > displayWidth(visible) {
		t.Fatalf("viewport=%q width=%d cursorColumn=%d", visible, displayWidth(visible), column)
	}
	if !strings.Contains(visible, "中文") {
		t.Fatalf("viewport should follow cursor, got %q", visible)
	}
}

func TestEditorDecodesANSIAndWindowsNavigationKeys(t *testing.T) {
	t.Parallel()
	shell := &Shell{reader: bufio.NewReader(strings.NewReader("[D[3~"))}
	if key := shell.readEscapeKey(); key != keyLeft {
		t.Fatalf("left key=%v", key)
	}
	if key := shell.readEscapeKey(); key != keyDelete {
		t.Fatalf("delete key=%v", key)
	}
	shell.reader = bufio.NewReader(strings.NewReader(string(rune(75))))
	if key := shell.readWindowsExtendedKey(); key != keyLeft {
		t.Fatalf("Windows left key=%v", key)
	}
}

func TestCommandHistoryIsBoundedAndDeduplicated(t *testing.T) {
	t.Parallel()
	shell := &Shell{}
	shell.recordHistory("/status")
	shell.recordHistory("/status")
	for index := 0; index < 105; index++ {
		shell.recordHistory(fmt.Sprintf("/logs %d", index))
	}
	if len(shell.history) != 100 {
		t.Fatalf("history length=%d, want 100", len(shell.history))
	}
	if shell.history[len(shell.history)-1] != "/logs 104" {
		t.Fatalf("unexpected last history item: %q", shell.history[len(shell.history)-1])
	}
}

func TestCommandHistoryNavigationRestoresDraft(t *testing.T) {
	t.Parallel()
	history := []string{"/status", "/core status"}
	value := []rune("/st")
	index := len(history)
	var draft []rune
	active := false

	value, index, draft, active = navigateCommandHistory(history, value, index, draft, active, -1)
	if got := string(value); got != "/core status" || !active {
		t.Fatalf("first previous=%q active=%v", got, active)
	}
	value, index, draft, active = navigateCommandHistory(history, value, index, draft, active, -1)
	if got := string(value); got != "/status" {
		t.Fatalf("second previous=%q", got)
	}
	value, index, draft, active = navigateCommandHistory(history, value, index, draft, active, 1)
	if got := string(value); got != "/core status" || !active {
		t.Fatalf("first next=%q active=%v", got, active)
	}
	value, _, _, active = navigateCommandHistory(history, value, index, draft, active, 1)
	if got := string(value); got != "/st" || active {
		t.Fatalf("restored draft=%q active=%v", got, active)
	}
}

func TestTrimLastRuneSupportsChinese(t *testing.T) {
	t.Parallel()
	if got := trimLastRune("节点A"); got != "节点" {
		t.Fatalf("trimLastRune() = %q, want %q", got, "节点")
	}
}

func TestTerminalDisplayWidthAndTruncation(t *testing.T) {
	t.Parallel()
	if got := displayWidth("abc节点"); got != 7 {
		t.Fatalf("displayWidth() = %d, want 7", got)
	}
	tests := []struct {
		value string
		max   int
		want  string
	}{
		{value: "abcdef", max: 4, want: "abc…"},
		{value: "节点测试", max: 5, want: "节点…"},
		{value: "short", max: 8, want: "short"},
	}
	for _, test := range tests {
		if got := truncateDisplay(test.value, test.max); got != test.want {
			t.Fatalf("truncateDisplay(%q, %d) = %q, want %q", test.value, test.max, got, test.want)
		}
	}
}

func TestParseCommandOptionsSupportsCompactSubscriptionSyntax(t *testing.T) {
	positional, options, err := parseCommandOptions([]string{"https://example.com/sub", "--name", "机场A", "--group", "日常"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(positional, []string{"https://example.com/sub"}) || options["name"] != "机场A" || options["group"] != "日常" {
		t.Fatalf("unexpected parse result: %#v %#v", positional, options)
	}
}
