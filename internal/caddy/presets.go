package caddy

import "sort"

// Presets maps preset names to their Caddy handle template strings.
// These are resolved by renderHandleTemplate when the template value
// matches a known preset name.
var Presets = map[string]string{
	// proxy: reverse proxy only (default). For API-only apps with no static file serving.
	"proxy": `[{"handler":"reverse_proxy","upstreams":[{"dial":"{{.Dial}}"}]}]`,

	// static-proxy: try static files first, fall back to reverse proxy.
	// Common for web apps that serve static assets alongside a backend.
	"static-proxy": `[{"handler":"subroute","routes":[{"handle":[{"handler":"file_server","root":"{{.SlotDir}}/public","pass_thru":true,"precompressed":{"gzip":{},"zstd":{},"br":{}}}]},{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"{{.Dial}}"}]}]}]}]`,

	// spa-proxy: SPA with reverse-proxied API. For SvelteKit (adapter-static) or similar SPA
	// frameworks with client-side path-based routing. Three routes:
	//   /assets/* — immutable static assets with aggressive cache headers
	//   /api/*   — reverse proxy to backend
	//   *        — try file, then fall back to index.html for client-side routing
	"spa-proxy": `[{"handler":"subroute","routes":[{"match":[{"path":["/assets/*"]}],"handle":[{"handler":"headers","response":{"set":{"Cache-Control":["public, max-age=31536000, immutable"]}}},{"handler":"file_server","root":"{{.SlotDir}}/public","precompressed":{"gzip":{},"zstd":{},"br":{}}}]},{"match":[{"path":["/api/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"{{.Dial}}"}]}]},{"handle":[{"handler":"file_server","root":"{{.SlotDir}}/public","pass_thru":true,"precompressed":{"gzip":{},"zstd":{},"br":{}}},{"handler":"rewrite","uri":"/index.html"},{"handler":"file_server","root":"{{.SlotDir}}/public"}]}]}]`,
}

// ResolvePreset returns the expanded template for a known preset name.
func ResolvePreset(name string) (string, bool) {
	tmpl, ok := Presets[name]
	return tmpl, ok
}

// PresetNames returns the sorted list of available preset names.
func PresetNames() []string {
	names := make([]string, 0, len(Presets))
	for name := range Presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
