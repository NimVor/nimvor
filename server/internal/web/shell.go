package web

import "sync/atomic"

// The page shell (sidebar, top bar, search palette) is rendered from a
// per-user navigation model built by the HTTP layer, which knows the
// permission and plan rules; templates reach it through the shellNav and
// shellUnread functions.

// NavChild is one sub-page of a sidebar item.
type NavChild struct{ Label, URL string }

// NavItem is one sidebar / palette entry.
type NavItem struct {
	Label, Icon, URL, Prefix, Desc string
	Children                       []NavChild
}

// NavGroup is a titled block of items.
type NavGroup struct {
	Name, ID string
	Items    []NavItem
}

// Nav is everything the shell shows for one user.
type Nav struct {
	Groups []NavGroup
	Quick  []NavItem
	// Unread is the notification badge; -1 hides the bell.
	Unread int64
}

// NavProvider builds the navigation for a signed-in user (*identity.User).
type NavProvider func(user any) *Nav

var navProvider atomic.Pointer[NavProvider]

// SetNavProvider installs the navigation builder.
func SetNavProvider(p NavProvider) { navProvider.Store(&p) }

func shellNav(user any) *Nav {
	p := navProvider.Load()
	if p == nil || user == nil {
		return nil
	}
	return (*p)(user)
}
