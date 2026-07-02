package cli

import (
	"fmt"
	"os"
	"strings"
)

// Transcript renderer modes. Inline prints finalized transcript blocks into the
// terminal's own scrollback (Claude Code-style: the terminal keeps native
// selection, right-click, wheel scrolling, and search), repainting only a
// pinned bottom region. Fullscreen owns the whole grid via the alt screen with
// an in-app viewport, scrollbar, and mouse capture. Auto is the historical
// behavior: inline on Termux (touch-first, soft-keyboard focus), fullscreen
// everywhere else.
const (
	renderModeAuto       = "auto"
	renderModeInline     = "inline"
	renderModeFullscreen = "fullscreen"
)

// resolvedRenderMode is set once at startup by chatREPL from flag > env >
// config. Empty means "not resolved" (tests, embedded uses) and falls back to
// auto detection, which preserves the pre-flag behavior exactly.
var resolvedRenderMode string

// renderModeInlineActive reports whether the inline (native scrollback)
// renderer is in effect for new chat TUIs.
func renderModeInlineActive() bool {
	switch resolvedRenderMode {
	case renderModeInline:
		return true
	case renderModeFullscreen:
		return false
	}
	return autoRenderModeInline()
}

// autoRenderModeInline is the "auto" policy: Termux needs the normal buffer so
// native touch scrollback and soft-keyboard focus keep working; other
// terminals keep the fullscreen viewport.
func autoRenderModeInline() bool {
	return detectTermuxTerminal()
}

// resolveRenderMode picks the renderer with precedence flag > $REASONIX_RENDERER
// > config (ui.renderer) > auto. An explicit "auto" at any level terminates the
// cascade (it means "use detection", overriding lower levels); empty or unknown
// values fall through to the next level. Only a bad flag value is an error —
// the user typed it right here and should see why it was rejected.
func resolveRenderMode(flagVal, cfgVal string) (string, error) {
	flagMode, ok := normalizeRenderMode(flagVal)
	if !ok {
		return "", fmt.Errorf("invalid --renderer %q (want inline, fullscreen, or auto)", flagVal)
	}
	for _, mode := range []string{flagMode, envRenderMode(), cfgVal} {
		switch mode {
		case renderModeInline, renderModeFullscreen, renderModeAuto:
			return mode, nil
		}
	}
	return renderModeAuto, nil
}

// envRenderMode reads $REASONIX_RENDERER; unknown values are ignored (the env
// var may be set globally for other versions) rather than fatal.
func envRenderMode() string {
	mode, ok := normalizeRenderMode(os.Getenv("REASONIX_RENDERER"))
	if !ok {
		return ""
	}
	return mode
}

// normalizeRenderMode lowercases and validates a renderer value. Empty is valid
// and stays empty ("unset"); ok is false only for a non-empty unknown value.
func normalizeRenderMode(v string) (mode string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", true
	case renderModeInline:
		return renderModeInline, true
	case renderModeFullscreen:
		return renderModeFullscreen, true
	case renderModeAuto:
		return renderModeAuto, true
	}
	return "", false
}
