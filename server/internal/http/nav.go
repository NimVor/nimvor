package http

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
	"github.com/mchkhawarshahzad/whacp/server/internal/web"
)

type navChildDef struct {
	Label, Route, Perm, URL, View string
	Also                          []string
}

type navItemDef struct {
	Label, Icon, Route, Perm, Prefix, Desc, URL string
	Children                                    []navChildDef
}

type navGroupDef struct {
	Name  string
	Items []navItemDef
}

var navIDRe = regexp.MustCompile(`[^a-z0-9]+`)

// buildNav ports View\Navigation::visible: the areas a user may open, per
// group (permission, plan feature and role scope), plus the palette's quick
// actions and the notification count.
func (s *Server) buildNav(ctx context.Context, u *identity.User) *web.Nav {
	az := s.authorizer()
	can := func(perm, route string) bool {
		if ok, err := az.Can(ctx, u, perm); err != nil || !ok {
			return false
		}
		if route == "" {
			return true
		}
		ok, err := az.RouteAllowed(ctx, u, route)
		return err == nil && ok
	}
	serverScope := u.RoleScope == "all"
	nav := &web.Nav{Unread: -1}
	for _, g := range navGroups {
		grp := web.NavGroup{Name: g.Name, ID: "wcp-nav-" + strings.Trim(navIDRe.ReplaceAllString(strings.ToLower(g.Name), "-"), "-")}
		for _, it := range g.Items {
			if !can(it.Perm, it.Route) {
				continue
			}
			item := web.NavItem{Label: it.Label, Icon: it.Icon, URL: it.URL, Prefix: it.Prefix, Desc: it.Desc}
			for _, c := range it.Children {
				if can(c.Perm, c.Route) {
					item.Children = append(item.Children, web.NavChild{Label: c.Label, URL: c.URL})
				}
			}
			// SSL/TLS, Monitoring and Logs sub-pages are for server administrators;
			// a single child adds nothing over its parent.
			if (it.Route == "ssl" || it.Route == "monitoring" || it.Route == "logs") && !serverScope || len(item.Children) < 2 {
				item.Children = nil
			}
			grp.Items = append(grp.Items, item)
		}
		if len(grp.Items) > 0 {
			nav.Groups = append(nav.Groups, grp)
		}
	}
	// Enabled plugins' entries, for users holding their permission.
	var plugGroup web.NavGroup
	for _, pl := range s.pluginManager().Active(ctx) {
		for _, n := range pl.Manifest.Nav {
			if can(n.Permission, "") {
				plugGroup.Items = append(plugGroup.Items, web.NavItem{Label: n.Label, Icon: n.Icon,
					URL: "/plugins/" + pl.Name + strings.TrimRight(n.Path, "/"), Prefix: "/plugins/" + pl.Name})
			}
		}
	}
	if len(plugGroup.Items) > 0 {
		plugGroup.Name, plugGroup.ID = "Plugins", "wcp-nav-plugins"
		nav.Groups = append(nav.Groups, plugGroup)
	}
	for _, q := range []struct{ label, icon, url, perm, desc string }{
		{"Create website", "plus-circle", "/websites/new", "websites.manage", "New website with PHP, SSL and DNS"},
		{"Create hosting account", "person-plus", "/accounts/new", "users.manage", "Customer with plan, website and email"},
		{"Create panel user", "person-add", "/users/new", "users.manage", "Administrator, operator or reseller"},
		{"My profile", "person-circle", "/profile", "dashboard.view", "Password, two-factor sign-in, API tokens"},
	} {
		if can(q.perm, "") {
			nav.Quick = append(nav.Quick, web.NavItem{Label: q.label, Icon: q.icon, URL: q.url, Desc: q.desc})
		}
	}
	if can("notifications.view", "") {
		if n, err := notify.NewRepository(s.db).UnreadCount(ctx, u); err == nil {
			nav.Unread = n
		}
	}
	if list := s.visibleServers(ctx, u); len(list) > 1 {
		nav.ServerID = u.ServerID
		for _, sv := range list {
			nav.Servers = append(nav.Servers, web.NavServer{ID: sv.ID, Name: sv.Name})
		}
	}
	return nav
}

func (s *Server) installNav() {
	web.SetNavProvider(func(user any) *web.Nav {
		u, ok := user.(*identity.User)
		if !ok || u == nil {
			return nil
		}
		return s.buildNav(context.Background(), u)
	})
}

// routeURL maps a PHP route name to its path (menu entries only).
func routeURL(name string) string {
	routeURLsOnce.Do(func() {
		routeURLs = map[string]string{}
		for _, g := range navGroups {
			for _, it := range g.Items {
				routeURLs[it.Route] = it.URL
				for _, c := range it.Children {
					if c.View == "" {
						routeURLs[c.Route] = c.URL
					}
				}
			}
		}
	})
	return routeURLs[name]
}

var (
	routeURLs     map[string]string
	routeURLsOnce sync.Once
)
