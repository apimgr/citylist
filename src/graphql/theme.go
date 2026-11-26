package graphql

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/apimgr/citylist/src/common/theme"
)

// ThemeCSS maps the shared project palette into GraphiQL's HSL theme variables.
func ThemeCSS() string {
	return `#graphiql {
  height: 70vh;
  min-height: 32rem;
  overflow: hidden;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
}
#graphiql .graphiql-container {
  height: 100%;
  font-family: var(--font-sans);
}
` + graphiQLThemeCSS("graphiql-dark", theme.ThemePaletteDark) + graphiQLThemeCSS("graphiql-light", theme.ThemePaletteLight)
}

func graphiQLThemeCSS(class string, palette theme.ThemePalette) string {
	return fmt.Sprintf(`body.%s .graphiql-container,
body.%s .CodeMirror-info,
body.%s .CodeMirror-lint-tooltip,
body.%s .graphiql-dialog,
body.%s .graphiql-dialog-overlay,
body.%s .graphiql-tooltip,
body.%s [data-radix-popper-content-wrapper] {
  --color-primary: %s;
  --color-secondary: %s;
  --color-tertiary: %s;
  --color-info: %s;
  --color-success: %s;
  --color-warning: %s;
  --color-error: %s;
  --color-neutral: %s;
  --color-base: %s;
}
`, class, class, class, class, class, class, class,
		hslToken(palette.Primary),
		hslToken(palette.Secondary),
		hslToken(palette.Accent),
		hslToken(palette.Info),
		hslToken(palette.Success),
		hslToken(palette.Warning),
		hslToken(palette.Error),
		hslToken(palette.Foreground),
		hslToken(palette.Background))
}

func hslToken(value string) string {
	raw := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(raw) != 6 {
		return "0, 0%, 0%"
	}
	red, errRed := strconv.ParseUint(raw[0:2], 16, 8)
	green, errGreen := strconv.ParseUint(raw[2:4], 16, 8)
	blue, errBlue := strconv.ParseUint(raw[4:6], 16, 8)
	if errRed != nil || errGreen != nil || errBlue != nil {
		return "0, 0%, 0%"
	}
	r, g, b := float64(red)/255, float64(green)/255, float64(blue)/255
	maxValue := math.Max(r, math.Max(g, b))
	minValue := math.Min(r, math.Min(g, b))
	delta := maxValue - minValue
	lightness := (maxValue + minValue) / 2
	hue := 0.0
	saturation := 0.0
	if delta != 0 {
		saturation = delta / (1 - math.Abs(2*lightness-1))
		switch maxValue {
		case r:
			hue = math.Mod((g-b)/delta, 6)
		case g:
			hue = (b-r)/delta + 2
		default:
			hue = (r-g)/delta + 4
		}
		hue *= 60
		if hue < 0 {
			hue += 360
		}
	}
	return fmt.Sprintf("%.0f, %.0f%%, %.0f%%", hue, saturation*100, lightness*100)
}
