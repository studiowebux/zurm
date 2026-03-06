package config

// Default values used when config file is absent or fields are unset.
var Defaults = Config{
	Font: FontConfig{
		Family: "JetBrains Mono",
		Size:   15,
	},
	Window: WindowConfig{
		Columns: 120,
		Rows:    35,
		Padding: 4,
	},
	Colors: ColorConfig{
		Background: "#0F0F18",
		Foreground: "#E8E8F0",
		Cursor:     "#A855F7",
		Border:     "#1C1C2E",
		Separator:  "#555570",
		// Purple-accent dark palette
		Black:         "#555570",
		Red:           "#F87171",
		Green:         "#34D399",
		Yellow:        "#F59E0B",
		Blue:          "#7C3AED",
		Magenta:       "#C084FC",
		Cyan:          "#67E8F9",
		White:         "#8888A8",
		BrightBlack:   "#555570",
		BrightRed:     "#F87171",
		BrightGreen:   "#34D399",
		BrightYellow:  "#F59E0B",
		BrightBlue:    "#A855F7",
		BrightMagenta: "#C084FC",
		BrightCyan:    "#67E8F9",
		BrightWhite:   "#E8E8F0",
	},
	Shell: ShellConfig{
		Program: "",
		Args:    []string{"-l"},
	},
	Scrollback: ScrollbackConfig{
		Lines: 10000,
	},
	StatusBar: StatusBarConfig{
		Enabled:           true,
		ShowGit:           true,
		ShowCwd:           true,
		ShowClock:         false,
		ShowProcess:       true,
		SegmentSeparator:  " · ",
		SeparatorHeightPx: 1,
		PaddingPx:         4,
	},
	Keyboard: KeyboardConfig{
		LeftOptionAsMeta: true,
		RepeatDelayMs:    500,
		RepeatIntervalMs: 50,
	},
	Help: HelpConfig{
		Enabled:      true,
		ContextMenu:  true,
		CloseConfirm: true,
	},
	Tabs: TabsConfig{
		MaxWidthChars: 24,
		NewTabDir:     "cwd",
	},
	Panes: PanesConfig{
		DividerWidthPixels: 1,
	},
	Input: InputConfig{
		DoubleClickMs: 300,
		CursorBlink:   false,
	},
	Scroll: ScrollConfig{
		WheelLinesPerTick: 3,
	},
	Performance: PerformanceConfig{
		TPS: 30,
	},
	Session: SessionConfig{
		Enabled:         true,
		RestoreOnLaunch: true,
		AutoSave:        false,  // Default to false to prevent accidental session loss
	},
	FileExplorer: FileExplorerConfig{
		Enabled:  true,
		Side:     "left",
		WidthPct: 35,
	},
	Blocks: BlocksConfig{
		Enabled:      false,
		ShowDuration: true,
		Padding:      2,
		Gap:          4,
		BorderWidth:  3,
		BorderColor:  "#1C1C2E",
		SuccessColor: "#34D399",
		FailColor:    "#F87171",
		BgColor:      "",
		BgAlpha:      0.0,
	},
}
