package middleware

import (
	"go-file/common"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

func TestWebAuthAndPermissionErrorsUseVisitorTheme(t *testing.T) {
	previousOptions := common.OptionMap
	t.Cleanup(func() { common.OptionMap = previousOptions })
	common.OptionMap = map[string]string{"WebsiteTheme": "classic"}
	renderer, err := common.NewThemeRenderer(fstest.MapFS{
		"public/login.html":               {Data: []byte("classic login")},
		"public/error.html":               {Data: []byte("classic error")},
		"public/themes/modern/login.html": {Data: []byte("modern login: {{.theme.ID}}")},
		"public/themes/modern/error.html": {Data: []byte("modern error: {{.theme.ID}}")},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := gin.New()
	server.HTMLRender = renderer
	server.Use(sessions.Sessions("session", cookie.NewStore([]byte("theme-test-key"))))
	server.GET("/manage", WebAuth(), func(c *gin.Context) { t.Error("unauthenticated request reached the page") })
	server.GET("/explorer", func(c *gin.Context) {
		// A populated session keeps this test independent of user-token/database lookups.
		session := sessions.Default(c)
		session.Set("username", "visitor")
		session.Set("role", common.RoleCommonUser)
		permissionCheckHelper(c, common.RoleAdminUser)
	})
	for _, test := range []struct{ path, want string }{
		{"/manage", "modern login: modern"},
		{"/explorer", "modern error: modern"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path+"?theme=modern", nil))
			if response.Code != http.StatusForbidden || response.Body.String() != test.want {
				t.Fatalf("got %d %q, want forbidden %q", response.Code, response.Body.String(), test.want)
			}
		})
	}
}
