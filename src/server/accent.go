package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

type accentRGB struct{ r, g, b float64 }
type accentHSL struct{ h, s, l float64 }

func (s *Server) handleAccentCSS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.renderError(w, r, http.StatusMethodNotAllowed, "")
		return
	}
	value := s.cfg.Web.UI.Accent
	if len(value) != 7 || value[0] != '#' {
		http.NotFound(w, r)
		return
	}
	for _, char := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			http.NotFound(w, r)
			return
		}
	}
	css, err := accentCSS(value)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hash := sha256.Sum256([]byte(css))
	etag := `"` + hex.EncodeToString(hash[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(css))
}

func accentCSS(raw string) (string, error) {
	rgb, err := parseAccentRGB(raw)
	if err != nil {
		return "", err
	}
	hsl := rgbToAccentHSL(rgb)
	dark := deriveAccent(rgb, hsl, accentRGB{40.0 / 255, 42.0 / 255, 54.0 / 255}, true)
	light := deriveAccent(rgb, hsl, accentRGB{1, 1, 1}, false)
	darkRGBA, lightRGBA := dark.css(0.15), light.css(0.12)
	return fmt.Sprintf(`:root{--color-primary:%s;--color-primary-bg:%s;--color-accent-primary:%s;--color-accent-hover:%s;--color-accent-secondary:%s;--color-accent-gradient:linear-gradient(135deg,%s,%s);--color-accent-glow:%s;--color-border-focus:var(--color-primary)}
html.theme-light{--color-primary:%s;--color-primary-bg:%s;--color-accent-primary:%s;--color-accent-hover:%s;--color-accent-secondary:%s;--color-accent-gradient:linear-gradient(135deg,%s,%s);--color-accent-glow:%s;--color-border-focus:var(--color-primary)}
@media(prefers-color-scheme:light){html.theme-auto{--color-primary:%s;--color-primary-bg:%s;--color-accent-primary:%s;--color-accent-hover:%s;--color-accent-secondary:%s;--color-accent-gradient:linear-gradient(135deg,%s,%s);--color-accent-glow:%s;--color-border-focus:var(--color-primary)}}
`, dark.hex(), darkRGBA, dark.hex(), dark.hex(), dark.hex(), dark.hex(), dark.hex(), dark.css(0.4),
		light.hex(), lightRGBA, light.hex(), light.hex(), light.hex(), light.hex(), light.hex(), light.css(0.4),
		light.hex(), lightRGBA, light.hex(), light.hex(), light.hex(), light.hex(), light.hex(), light.css(0.4)), nil
}

func parseAccentRGB(value string) (accentRGB, error) {
	if len(value) != 7 || value[0] != '#' {
		return accentRGB{}, fmt.Errorf("invalid accent color")
	}
	parts := [3]float64{}
	for index := range parts {
		parsed, err := strconv.ParseUint(value[1+index*2:3+index*2], 16, 8)
		if err != nil {
			return accentRGB{}, fmt.Errorf("invalid accent color: %w", err)
		}
		parts[index] = float64(parsed) / 255
	}
	return accentRGB{parts[0], parts[1], parts[2]}, nil
}

func rgbToAccentHSL(color accentRGB) accentHSL {
	maxValue := math.Max(color.r, math.Max(color.g, color.b))
	minValue := math.Min(color.r, math.Min(color.g, color.b))
	delta := maxValue - minValue
	lightness := (maxValue + minValue) / 2
	if delta == 0 {
		return accentHSL{l: lightness}
	}
	saturation := delta / (1 - math.Abs(2*lightness-1))
	var hue float64
	switch maxValue {
	case color.r:
		hue = math.Mod((color.g-color.b)/delta, 6)
	case color.g:
		hue = (color.b-color.r)/delta + 2
	default:
		hue = (color.r-color.g)/delta + 4
	}
	hue *= 60
	if hue < 0 {
		hue += 360
	}
	return accentHSL{h: hue, s: saturation, l: lightness}
}

func accentHSLToRGB(color accentHSL) accentRGB {
	c := (1 - math.Abs(2*color.l-1)) * color.s
	x := c * (1 - math.Abs(math.Mod(color.h/60, 2)-1))
	m := color.l - c/2
	switch {
	case color.h < 60:
		return accentRGB{c + m, x + m, m}
	case color.h < 120:
		return accentRGB{x + m, c + m, m}
	case color.h < 180:
		return accentRGB{m, c + m, x + m}
	case color.h < 240:
		return accentRGB{m, x + m, c + m}
	case color.h < 300:
		return accentRGB{x + m, m, c + m}
	default:
		return accentRGB{c + m, m, x + m}
	}
}

func deriveAccent(base accentRGB, hsl accentHSL, background accentRGB, lighten bool) accentRGB {
	if accentContrast(base, background) >= 4.5 {
		return base
	}
	for step := 1; step <= 100; step++ {
		lightness := hsl.l + float64(step)/100
		if !lighten {
			lightness = hsl.l - float64(step)/100
		}
		if lightness < 0 || lightness > 1 {
			break
		}
		candidate := accentHSLToRGB(accentHSL{h: hsl.h, s: hsl.s, l: lightness})
		if accentContrast(candidate, background) >= 4.5 {
			return candidate
		}
	}
	if lighten {
		return accentRGB{1, 1, 1}
	}
	return accentRGB{}
}

func accentContrast(first, second accentRGB) float64 {
	luminance := func(color accentRGB) float64 {
		channel := func(value float64) float64 {
			if value <= 0.04045 {
				return value / 12.92
			}
			return math.Pow((value+0.055)/1.055, 2.4)
		}
		return 0.2126*channel(color.r) + 0.7152*channel(color.g) + 0.0722*channel(color.b)
	}
	firstLum, secondLum := luminance(first), luminance(second)
	if firstLum < secondLum {
		firstLum, secondLum = secondLum, firstLum
	}
	return (firstLum + 0.05) / (secondLum + 0.05)
}

func (color accentRGB) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", channelByte(color.r), channelByte(color.g), channelByte(color.b))
}

func (color accentRGB) css(alpha float64) string {
	return fmt.Sprintf("rgba(%d,%d,%d,%.2f)", channelByte(color.r), channelByte(color.g), channelByte(color.b), alpha)
}

func channelByte(value float64) int {
	return int(math.Round(math.Max(0, math.Min(value, 1)) * 255))
}
