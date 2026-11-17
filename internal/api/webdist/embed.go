// Package webdist embeds the built dashboard. `make dashboard` copies
// dashboard/dist here; the committed placeholder explains how to build it.
package webdist

import "embed"

//go:embed all:dist
var FS embed.FS
