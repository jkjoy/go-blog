package web

import "embed"

// Files contains every template and static asset required by the server.
//
//go:embed templates static
var Files embed.FS
