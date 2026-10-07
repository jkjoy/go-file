package common

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
)

const DefaultThemeID = "classic"
const ThemeCookieName = "go-file-theme"

type Theme struct {
	ID          string
	Name        string
	Description string
	Stylesheet  string
}

// Themes is the list of installed templates shown in the theme selector.
var Themes = []Theme{
	{ID: DefaultThemeID, Name: "经典", Description: "保留原有界面与熟悉的操作方式"},
	{ID: "modern", Name: "现代简约", Description: "清晰布局、柔和留白与现代文件列表", Stylesheet: "/public/static/theme-modern.css"},
}

func IsValidTheme(id string) bool {
	for _, theme := range Themes {
		if theme.ID == id {
			return true
		}
	}
	return false
}

func themeByID(id string) Theme {
	for _, theme := range Themes {
		if theme.ID == id {
			return theme
		}
	}
	return Themes[0]
}

// ResolveTheme keeps a visitor's choice across pages without changing the site default.
func ResolveTheme(c *gin.Context) Theme {
	theme, _ := resolveTheme(c)
	return theme
}

func resolveTheme(c *gin.Context) (Theme, string) {
	if c.Query("theme") == "default" {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(ThemeCookieName, "", -1, "/", "", c.Request.TLS != nil, true)
		return themeByID(OptionMap["WebsiteTheme"]), ""
	}
	if id := c.Query("theme"); IsValidTheme(id) {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(ThemeCookieName, id, 365*24*60*60, "/", "", c.Request.TLS != nil, true)
		return themeByID(id), id
	}
	if id, err := c.Cookie(ThemeCookieName); err == nil && IsValidTheme(id) {
		return themeByID(id), id
	}
	return themeByID(OptionMap["WebsiteTheme"]), ""
}

// RenderPage supplies the same theme context to normal pages and middleware errors.
func RenderPage(c *gin.Context, status int, name string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	data["theme"], data["themePreference"] = resolveTheme(c)
	data["themes"] = Themes
	data["pagePath"] = c.Request.URL.Path
	data["query"] = c.Query("query")
	if _, ok := data["option"]; !ok {
		data["option"] = OptionMap
	}
	if _, ok := data["username"]; !ok {
		data["username"] = c.GetString("username")
	}
	c.HTML(status, name, data)
}

type ThemeRenderer struct {
	templates map[string]*template.Template
}

// NewThemeRenderer clones the shared templates before applying each theme's overrides.
// ParseFS uses basenames, so themes can replace index.html and named partials such as nav.
func NewThemeRenderer(source fs.FS) (*ThemeRenderer, error) {
	base, err := template.New("").Funcs(template.FuncMap{"unescape": UnescapeHTML}).ParseFS(source, "public/*.html")
	if err != nil {
		return nil, fmt.Errorf("load shared templates: %w", err)
	}
	renderer := &ThemeRenderer{templates: make(map[string]*template.Template, len(Themes))}
	for _, theme := range Themes {
		pages, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone theme %s: %w", theme.ID, err)
		}
		overrides, err := fs.Glob(source, "public/themes/"+theme.ID+"/*.html")
		if err != nil {
			return nil, fmt.Errorf("find theme %s templates: %w", theme.ID, err)
		}
		if len(overrides) > 0 {
			pages, err = pages.ParseFS(source, overrides...)
			if err != nil {
				return nil, fmt.Errorf("load theme %s: %w", theme.ID, err)
			}
		}
		renderer.templates[theme.ID] = pages
	}
	return renderer, nil
}

func (r *ThemeRenderer) Instance(name string, data interface{}) render.Render {
	id := DefaultThemeID
	if values, ok := data.(gin.H); ok {
		if theme, ok := values["theme"].(Theme); ok && IsValidTheme(theme.ID) {
			id = theme.ID
		}
	}
	return render.HTML{Template: r.templates[id], Name: name, Data: data}
}
