package web

import "embed"

// Content holds the embedded static web assets.
//
//go:embed index.html
var Content embed.FS
