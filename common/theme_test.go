package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestThemeRendererIsolatesOverridesAndInheritsSharedPages(t *testing.T) {
	source := fstest.MapFS{
		"public/index.html":               {Data: []byte(`classic page: {{template "nav" .}}`)},
		"public/help.html":                {Data: []byte(`shared help: {{template "nav" .}}`)},
		"public/nav.html":                 {Data: []byte(`{{define "nav"}}classic navigation{{end}}`)},
		"public/themes/modern/index.html": {Data: []byte(`modern page: {{template "nav" .}}`)},
		"public/themes/modern/nav.html":   {Data: []byte(`{{define "nav"}}modern navigation{{end}}`)},
	}
	renderer, err := NewThemeRenderer(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ theme, page, want string }{
		{"modern", "index.html", "modern page: modern navigation"},
		{"classic", "index.html", "classic page: classic navigation"},
		{"modern", "help.html", "shared help: modern navigation"},
		{"classic", "help.html", "shared help: classic navigation"},
		{"unknown", "index.html", "classic page: classic navigation"},
	} {
		t.Run(test.theme+"/"+test.page, func(t *testing.T) {
			response := httptest.NewRecorder()
			err := renderer.Instance(test.page, gin.H{"theme": Theme{ID: test.theme}}).Render(response)
			if err != nil {
				t.Fatal(err)
			}
			if got := response.Body.String(); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveThemePrecedenceAndPreferenceCookies(t *testing.T) {
	previousOptions := OptionMap
	t.Cleanup(func() { OptionMap = previousOptions })
	for _, test := range []struct {
		name, query, cookie, siteDefault, want, preference string
		cookieAge                                          int
		secure                                             bool
	}{
		{name: "new visitors follow site default", siteDefault: "modern", want: "modern"},
		{name: "cookie overrides site default", cookie: "classic", siteDefault: "modern", want: "classic", preference: "classic"},
		{name: "query overrides cookie", query: "modern", cookie: "classic", siteDefault: "classic", want: "modern", preference: "modern", cookieAge: 365 * 24 * 60 * 60},
		{name: "invalid query retains valid cookie", query: "../../modern", cookie: "classic", siteDefault: "modern", want: "classic", preference: "classic"},
		{name: "invalid cookie follows site default", cookie: "missing", siteDefault: "modern", want: "modern"},
		{name: "invalid stored default uses classic", siteDefault: "missing", want: "classic"},
		{name: "unset stored default uses classic", want: "classic"},
		{name: "follow site default clears preference", query: "default", cookie: "classic", siteDefault: "modern", want: "modern", cookieAge: -1},
		{name: "HTTPS preference cookie is secure", query: "modern", siteDefault: "classic", want: "modern", preference: "modern", cookieAge: 365 * 24 * 60 * 60, secure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			OptionMap = map[string]string{"WebsiteTheme": test.siteDefault}
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			url := "http://example.com/?theme=" + test.query
			if test.secure {
				url = "https://example.com/?theme=" + test.query
			}
			context.Request = httptest.NewRequest(http.MethodGet, url, nil)
			if test.cookie != "" {
				context.Request.AddCookie(&http.Cookie{Name: ThemeCookieName, Value: test.cookie})
			}
			theme, preference := resolveTheme(context)
			if theme.ID != test.want || preference != test.preference {
				t.Fatalf("got theme %q and preference %q, want %q and %q", theme.ID, preference, test.want, test.preference)
			}
			cookies := response.Result().Cookies()
			if test.cookieAge == 0 {
				if len(cookies) != 0 {
					t.Fatal("reading the existing preference should not write a cookie")
				}
				return
			}
			if len(cookies) != 1 {
				t.Fatalf("got %d cookies, want one", len(cookies))
			}
			cookie := cookies[0]
			if cookie.Name != ThemeCookieName || cookie.Value != test.preference || cookie.MaxAge != test.cookieAge || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || !cookie.HttpOnly || cookie.Secure != test.secure {
				t.Fatalf("unexpected preference cookie: %+v", cookie)
			}
		})
	}
}

func TestRenderPageProvidesNavigationAndThemeContext(t *testing.T) {
	previousOptions := OptionMap
	t.Cleanup(func() { OptionMap = previousOptions })
	OptionMap = map[string]string{"WebsiteName": "Test Files", "WebsiteTheme": "classic"}
	renderer, err := NewThemeRenderer(fstest.MapFS{
		"public/index.html": {Data: []byte(`{{.theme.ID}}|{{.themePreference}}|{{.pagePath}}|{{.query}}|{{.username}}|{{.option.WebsiteName}}|{{len .themes}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := gin.New()
	server.HTMLRender = renderer
	server.GET("/explorer", func(c *gin.Context) {
		c.Set("username", "admin")
		RenderPage(c, http.StatusForbidden, "index.html", nil)
	})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/explorer?theme=modern&query=report", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want forbidden", response.Code)
	}
	if got, want := response.Body.String(), "modern|modern|/explorer|report|admin|Test Files|3"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestThemeRendererReportsInvalidOverride(t *testing.T) {
	_, err := NewThemeRenderer(fstest.MapFS{
		"public/index.html":               {Data: []byte("shared")},
		"public/themes/modern/index.html": {Data: []byte("{{if}}")},
	})
	if err == nil || !strings.Contains(err.Error(), "modern") {
		t.Fatalf("expected an error identifying the broken theme, got %v", err)
	}
}

func TestEmbeddedThemesRenderEveryPage(t *testing.T) {
	renderer, err := NewThemeRenderer(FS)
	if err != nil {
		t.Fatal(err)
	}
	pages := []string{"index.html", "explorer.html", "image.html", "login.html", "manage.html", "help.html", "video.html", "error.html", "404.html", "text-copy.html"}
	for _, theme := range Themes {
		for _, page := range pages {
			for _, populated := range []bool{false, true} {
				t.Run(theme.ID+"/"+page+"/"+map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
					files := []gin.H{}
					if populated {
						files = append(files, gin.H{
							"Id": 1, "Filename": "report.pdf", "Description": "Quarterly report", "Uploader": "admin", "Link": "report.pdf", "Time": "2026-10-07", "DownloadCounter": 3,
							"Name": "report.pdf", "IsFolder": false, "Size": "4 KB", "ModifiedTime": "2026-10-07",
						})
					}
					data := gin.H{
						"theme": theme, "themes": Themes, "themePreference": theme.ID, "pagePath": "/", "query": "report & notes", "files": files, "isQuery": true,
						"username": "admin", "isAdmin": true, "prev": 0, "next": 1, "message": "Test message", "content": "Text file content",
						"FileUploadPermission": RoleGuestUser, "FileDownloadPermission": RoleGuestUser, "ImageUploadPermission": RoleGuestUser, "ImageDownloadPermission": RoleGuestUser,
						"option": map[string]string{"WebsiteName": "Test Files", "WebsiteTheme": "modern", "FooterInfo": "", "Notice": "", "Version": "test"},
					}
					if page == "explorer.html" && populated {
						data["readmeFileLink"] = "explorer?path=README.md"
					}
					response := httptest.NewRecorder()
					if err := renderer.Instance(page, data).Render(response); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(response.Body.String(), "<!DOCTYPE html>") {
						t.Fatal("page did not produce a complete HTML document")
					}
					hasModernStyle := strings.Contains(response.Body.String(), "/public/static/theme-modern.css")
					if hasModernStyle != (theme.ID == "modern") {
						t.Fatal("page inherited the wrong theme stylesheet")
					}
					hasNebulaStyle := strings.Contains(response.Body.String(), "/public/static/theme-nebula.css")
					if hasNebulaStyle != (theme.ID == "nebula") {
						t.Fatal("page inherited the wrong nebula stylesheet")
					}
				})
			}
		}
	}
}
