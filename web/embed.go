// Package web contiene le pagine incorporate nell'eseguibile.
package web

import "embed"

// FS contiene pagine, script, stili e icone.
//
//go:embed *.html *.js *.css *.json rnnoise/*.js avatars/*.svg brand/*.png
var FS embed.FS
