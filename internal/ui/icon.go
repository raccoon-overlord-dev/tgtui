package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
)

// The icon's colours: the theme's background and accent, set by setTheme. The terminal
// theme names ANSI colours only, so it keeps harvest's.
var iconBg, iconAccent = "#1c1612", "#e8833a"

// iconSVG is tgtui's icon, a speech bubble holding a prompt, in the theme's colours.
func iconSVG(bg, accent string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
<rect width="100" height="100" rx="22" fill="%[1]s"/>
<path d="M30 20 H70 A12 12 0 0 1 82 32 V58 A12 12 0 0 1 70 70 H40 L26 81 L29 70 A12 12 0 0 1 18 58 V32 A12 12 0 0 1 30 20 Z" fill="%[2]s"/>
<polyline points="33,35 43,45 33,55" fill="none" stroke="%[1]s" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/>
<line x1="50" y1="55" x2="66" y2="55" stroke="%[1]s" stroke-width="6" stroke-linecap="round"/>
</svg>
`, bg, accent)
}

// iconPath writes the icon in the current theme's colours to the cache, once per colour
// pair, and returns its path ("" if it can't be written).
func iconPath() string {
	p := filepath.Join(config.CacheDir(), "icon-"+strings.TrimPrefix(iconBg, "#")+"-"+strings.TrimPrefix(iconAccent, "#")+".svg")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if os.WriteFile(p, []byte(iconSVG(iconBg, iconAccent)), 0o644) != nil {
		return ""
	}
	return p
}
