package web

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
)

func TestAdminNavigationIsStableAcrossPages(t *testing.T) {
	paths := []string{"/admin", "/admin/users", "/admin/hymatrix", "/admin/llm", "/admin/google", "/admin/browser", "/admin/xbox-child", "/admin/xbot", "/admin/telegram", "/admin/llm/test", "/admin/hymatrix/eval", "/admin/weixin", "/admin/test", "/admin/hymatrix/weixin-reset"}
	r := gin.New()
	RegisterRoutes(r, allowAll)
	var baseline []string
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			root, err := html.Parse(strings.NewReader(rec.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			var links, labels, active []string
			var visit func(*html.Node, bool)
			attr := func(n *html.Node, key string) string {
				for _, a := range n.Attr {
					if a.Key == key {
						return a.Val
					}
				}
				return ""
			}
			var textContent func(*html.Node) string
			textContent = func(n *html.Node) string {
				if n.Type == html.TextNode {
					return n.Data
				}
				s := ""
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					s += textContent(c)
				}
				return s
			}
			visit = func(n *html.Node, inNav bool) {
				inNav = inNav || (n.Data == "nav" && attr(n, "class") == "nav")
				if inNav && n.Data == "a" {
					href := attr(n, "href")
					links = append(links, href)
					labels = append(labels, strings.TrimSpace(textContent(n)))
					if strings.Contains(" "+attr(n, "class")+" ", " active ") {
						active = append(active, href)
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					visit(c, inNav)
				}
			}
			visit(root, false)
			if !reflect.DeepEqual(links, paths) {
				t.Errorf("sidebar links = %v; want %v", links, paths)
			}
			if baseline == nil {
				baseline = labels
			} else if !reflect.DeepEqual(labels, baseline) {
				t.Errorf("sidebar labels changed: %v", labels)
			}
			if !reflect.DeepEqual(active, []string{path}) {
				t.Errorf("active links = %v; want only %s", active, path)
			}
		})
	}
}
