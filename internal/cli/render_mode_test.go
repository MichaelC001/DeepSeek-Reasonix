package cli

import "testing"

func TestResolveRenderModePrecedence(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		env     string
		cfg     string
		want    string
		wantErr bool
	}{
		{name: "all empty falls to auto", want: renderModeAuto},
		{name: "flag wins over env and config", flag: "inline", env: "fullscreen", cfg: "fullscreen", want: renderModeInline},
		{name: "explicit auto flag terminates cascade", flag: "auto", env: "inline", cfg: "inline", want: renderModeAuto},
		{name: "env wins over config", env: "fullscreen", cfg: "inline", want: renderModeFullscreen},
		{name: "config wins over auto", cfg: "inline", want: renderModeInline},
		{name: "flag case-insensitive with spaces", flag: " Fullscreen ", want: renderModeFullscreen},
		{name: "unknown env ignored, config applies", env: "bogus", cfg: "fullscreen", want: renderModeFullscreen},
		{name: "invalid flag errors", flag: "bogus", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REASONIX_RENDERER", tc.env)
			got, err := resolveRenderMode(tc.flag, tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveRenderMode(%q, %q) = %q, want error", tc.flag, tc.cfg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRenderMode(%q, %q) unexpected error: %v", tc.flag, tc.cfg, err)
			}
			if got != tc.want {
				t.Fatalf("resolveRenderMode(%q, %q) = %q, want %q", tc.flag, tc.cfg, got, tc.want)
			}
		})
	}
}

func TestRenderModeInlineActive(t *testing.T) {
	restoreDetect := detectTermuxTerminal
	restoreMode := resolvedRenderMode
	t.Cleanup(func() {
		detectTermuxTerminal = restoreDetect
		resolvedRenderMode = restoreMode
	})

	for _, tc := range []struct {
		name   string
		mode   string
		termux bool
		want   bool
	}{
		{name: "explicit inline ignores detection", mode: renderModeInline, termux: false, want: true},
		{name: "explicit fullscreen ignores Termux", mode: renderModeFullscreen, termux: true, want: false},
		{name: "auto follows Termux detection on", mode: renderModeAuto, termux: true, want: true},
		{name: "auto follows Termux detection off", mode: renderModeAuto, termux: false, want: false},
		{name: "unresolved behaves like auto", mode: "", termux: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolvedRenderMode = tc.mode
			detectTermuxTerminal = func() bool { return tc.termux }
			if got := renderModeInlineActive(); got != tc.want {
				t.Fatalf("renderModeInlineActive() with mode=%q termux=%v = %v, want %v", tc.mode, tc.termux, got, tc.want)
			}
		})
	}
}
