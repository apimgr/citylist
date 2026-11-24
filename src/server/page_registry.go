package server

import "github.com/apimgr/citylist/src/common/i18n"

type pageDefinition struct {
	Path       string
	TitleKey   string
	DescKey    string
	Hub        string
	Sitemap    bool
	Priority   string
	ChangeFreq string
}

// serverPageRegistry is the source for public page hubs and SEO discovery.
var serverPageRegistry = []pageDefinition{
	{Path: "/", TitleKey: "home.title", DescKey: "home.description", Sitemap: true, Priority: "1.0", ChangeFreq: "daily"},
	{Path: "/search", TitleKey: "search.title", DescKey: "search.description", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/coordinates", TitleKey: "coordinates.title", DescKey: "coordinates.description", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/about", TitleKey: "pages.about.title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/help", TitleKey: "pages.help.title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/help/mcp", TitleKey: "help.mcp_title", DescKey: "help.mcp_description", Hub: "help", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/privacy", TitleKey: "pages.privacy.title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/dpo", TitleKey: "pages.dpo.title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/terms", TitleKey: "pages.terms.title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/security", TitleKey: "pages.security.title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/security/policy", TitleKey: "pages.security.policy_title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/security/thanks", TitleKey: "pages.security.thanks_title", Sitemap: true, Priority: "0.8", ChangeFreq: "weekly"},
	{Path: "/server/docs/swagger", TitleKey: "common.api_docs", Sitemap: true, Priority: "0.7", ChangeFreq: "weekly"},
	{Path: "/server/docs/graphql", TitleKey: "common.api_docs", Sitemap: true, Priority: "0.7", ChangeFreq: "weekly"},
	{Path: "/server/preferences/tokens", TitleKey: "preferences.tokens_title", DescKey: "preferences.tokens_description", Hub: "preferences"},
	{Path: "/server/preferences/sync", TitleKey: "preferences.sync_title", DescKey: "preferences.sync_description", Hub: "preferences"},
}

func registeredHubPages(lang, hub string) []hubPage {
	pages := make([]hubPage, 0)
	for _, page := range serverPageRegistry {
		if page.Hub != hub {
			continue
		}
		pages = append(pages, hubPage{
			Path: page.Path, Title: i18n.Translate(lang, page.TitleKey), Desc: i18n.Translate(lang, page.DescKey),
		})
	}
	return pages
}
