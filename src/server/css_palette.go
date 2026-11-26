package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/apimgr/citylist/src/common/theme"
	graphiqltheme "github.com/apimgr/citylist/src/graphql"
	swaggertheme "github.com/apimgr/citylist/src/swagger"
)

func (s *Server) handleCommonCSS(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/css/common.css")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dark, light := theme.ThemePaletteDark, theme.ThemePaletteLight
	replacer := strings.NewReplacer(
		"__CITYLIST_DARK_BACKGROUND__", dark.Background,
		"__CITYLIST_DARK_FOREGROUND__", dark.Foreground,
		"__CITYLIST_DARK_PRIMARY__", dark.Primary,
		"__CITYLIST_DARK_SECONDARY__", dark.Secondary,
		"__CITYLIST_DARK_ACCENT__", dark.Accent,
		"__CITYLIST_DARK_SUCCESS__", dark.Success,
		"__CITYLIST_DARK_WARNING__", dark.Warning,
		"__CITYLIST_DARK_ERROR__", dark.Error,
		"__CITYLIST_DARK_INFO__", dark.Info,
		"__CITYLIST_DARK_SURFACE__", dark.Surface,
		"__CITYLIST_DARK_SURFACE_ALT__", dark.SurfaceAlt,
		"__CITYLIST_DARK_HOVER__", dark.BackgroundHover,
		"__CITYLIST_DARK_MUTED__", dark.Muted,
		"__CITYLIST_DARK_BORDER__", dark.Border,
		"__CITYLIST_DARK_BORDER_HOVER__", dark.BorderHover,
		"__CITYLIST_DARK_ON_ERROR__", dark.OnError,
		"__CITYLIST_LIGHT_BACKGROUND__", light.Background,
		"__CITYLIST_LIGHT_FOREGROUND__", light.Foreground,
		"__CITYLIST_LIGHT_PRIMARY__", light.Primary,
		"__CITYLIST_LIGHT_SECONDARY__", light.Secondary,
		"__CITYLIST_LIGHT_ACCENT__", light.Accent,
		"__CITYLIST_LIGHT_SUCCESS__", light.Success,
		"__CITYLIST_LIGHT_WARNING__", light.Warning,
		"__CITYLIST_LIGHT_ERROR__", light.Error,
		"__CITYLIST_LIGHT_INFO__", light.Info,
		"__CITYLIST_LIGHT_SURFACE__", light.Surface,
		"__CITYLIST_LIGHT_SURFACE_ALT__", light.SurfaceAlt,
		"__CITYLIST_LIGHT_HOVER__", light.BackgroundHover,
		"__CITYLIST_LIGHT_MUTED__", light.Muted,
		"__CITYLIST_LIGHT_BORDER__", light.Border,
		"__CITYLIST_LIGHT_BORDER_HOVER__", light.BorderHover,
		"__CITYLIST_LIGHT_ON_ERROR__", light.OnError,
	)
	content := []byte(replacer.Replace(string(data)))
	content = append(content, []byte(swaggertheme.ThemeCSS())...)
	content = append(content, []byte(graphiqltheme.ThemeCSS())...)
	if strings.Contains(string(content), "__CITYLIST_") {
		http.Error(w, "Theme palette is incomplete", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	if setAssetCacheHeaders(w, r) {
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}
