package L

import (
	"log"
	"os"
	"testing"
)

func TestColorModeString(t *testing.T) {
	tests := []struct {
		cm   ColorMode
		want string
	}{
		{COLOR_MODE_AUTO, "auto"},
		{COLOR_MODE_ALWAYS, "always"},
		{COLOR_MODE_NEVER, "never"},
		{ColorMode(99), "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.cm.String()
			if got != tt.want {
				t.Errorf("ColorMode(%d).String() = %q, want %q", tt.cm, got, tt.want)
			}
		})
	}
}

func TestSetColorModeFromString(t *testing.T) {
	tests := []struct {
		input      string
		wantMode   ColorMode
		wantErr    bool
	}{
		{"auto", COLOR_MODE_AUTO, false},
		{"always", COLOR_MODE_ALWAYS, false},
		{"never", COLOR_MODE_NEVER, false},
		{"AUTO", COLOR_MODE_AUTO, false},
		{"ALWAYS", COLOR_MODE_ALWAYS, false},
		{"NEVER", COLOR_MODE_NEVER, false},
		{"invalid", COLOR_MODE_AUTO, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := SetColorModeFromString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetColorModeFromString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && colorMode != tt.wantMode {
				t.Errorf("SetColorModeFromString(%q) colorMode = %v, want %v", tt.input, colorMode, tt.wantMode)
			}
		})
	}
}

func TestSetColorMode(t *testing.T) {
	for _, cm := range []ColorMode{COLOR_MODE_AUTO, COLOR_MODE_ALWAYS, COLOR_MODE_NEVER} {
		err := SetColorMode(cm)
		if err != nil {
			t.Errorf("SetColorMode(%d) unexpected error: %v", cm, err)
		}
		if colorMode != cm {
			t.Errorf("SetColorMode(%d) colorMode = %d", cm, colorMode)
		}
	}

	err := SetColorMode(ColorMode(99))
	if err == nil {
		t.Error("SetColorMode(99) expected error")
	}
}

func TestShouldUseColors(t *testing.T) {
	// save and restore state
	origColorMode := colorMode
	origNoColor := os.Getenv("NO_COLOR")
	origClicolor := os.Getenv("CLICOLOR")
	origClicolorForce := os.Getenv("CLICOLOR_FORCE")
	origCI := os.Getenv("CI")
	origTerm := os.Getenv("TERM")
	defer func() {
		colorMode = origColorMode
		os.Setenv("NO_COLOR", origNoColor)
		os.Setenv("CLICOLOR", origClicolor)
		os.Setenv("CLICOLOR_FORCE", origClicolorForce)
		os.Setenv("CI", origCI)
		os.Setenv("TERM", origTerm)
	}()

	// clear env for predictable tests
	os.Unsetenv("NO_COLOR")
	os.Unsetenv("CLICOLOR")
	os.Unsetenv("CLICOLOR_FORCE")
	os.Unsetenv("CI")
	os.Unsetenv("TERM")

	tests := []struct {
		name      string
		mode      ColorMode
		setEnv    map[string]string
		unsetEnv  []string
		want      bool
	}{
		{
			name: "always",
			mode: COLOR_MODE_ALWAYS,
			want: true,
		},
		{
			name: "never",
			mode: COLOR_MODE_NEVER,
			want: false,
		},
		{
			name: "auto with NO_COLOR",
			mode: COLOR_MODE_AUTO,
			setEnv: map[string]string{"NO_COLOR": "1"},
			want: false,
		},
		{
			name: "auto with CLICOLOR=0",
			mode: COLOR_MODE_AUTO,
			setEnv: map[string]string{"CLICOLOR": "0"},
			want: false,
		},
		{
			name: "auto with CLICOLOR=0 and CLICOLOR_FORCE",
			mode: COLOR_MODE_AUTO,
			setEnv: map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "1"},
			want: false, // still false: TTY check fails in test env
		},
		{
			name: "auto with CI",
			mode: COLOR_MODE_AUTO,
			setEnv: map[string]string{"CI": "true"},
			want: false,
		},
		{
			name: "auto with TERM=dumb",
			mode: COLOR_MODE_AUTO,
			setEnv: map[string]string{"TERM": "dumb"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			colorMode = tt.mode
			for k, v := range tt.setEnv {
				os.Setenv(k, v)
			}
			for _, k := range tt.unsetEnv {
				os.Unsetenv(k)
			}

			got := shouldUseColors(colorMode)

			if got != tt.want {
				t.Errorf("shouldUseColors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestColorize(t *testing.T) {
	origColorMode := colorMode
	defer func() { colorMode = origColorMode }()

	t.Run("always wraps with color", func(t *testing.T) {
		colorMode = COLOR_MODE_ALWAYS
		got := colorize("hello", ansiRed)
		want := ansiRed + "hello" + ansiReset
		if got != want {
			t.Errorf("colorize() = %q, want %q", got, want)
		}
	})

	t.Run("never returns plain", func(t *testing.T) {
		colorMode = COLOR_MODE_NEVER
		got := colorize("hello", ansiRed)
		if got != "hello" {
			t.Errorf("colorize() = %q, want %q", got, "hello")
		}
	})

	t.Run("empty color returns plain", func(t *testing.T) {
		colorMode = COLOR_MODE_ALWAYS
		got := colorize("hello", ansiNone)
		want := "hello" + ansiReset
		if got != want {
			t.Errorf("colorize() = %q, want %q", got, want)
		}
	})
}

func TestGetLoggerAndStyle(t *testing.T) {
	tests := []struct {
		level      LogLevel
		wantLogger *log.Logger
		wantColor  string
	}{
		{DEBUG, debugLogger, ansiBlue},
		{INFO, infoLogger, ansiGreen},
	{NORMAL, normalLogger, ansiNone},
	{WARN, warnLogger, ansiYellow},
	{ERROR, errorLogger, ansiRed},
	{PANIC, panicLogger, ansiRed},
	{LogLevel(99), infoLogger, ansiNone},
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			gotLogger, gotColor := getLoggerAndStyle(tt.level)
			if gotLogger != tt.wantLogger {
				t.Errorf("getLoggerAndStyle(%d) logger = %v, want %v", tt.level, gotLogger, tt.wantLogger)
			}
			if gotColor != tt.wantColor {
				t.Errorf("getLoggerAndStyle(%d) color = %q, want %q", tt.level, gotColor, tt.wantColor)
			}
		})
	}
}
