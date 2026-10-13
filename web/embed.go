// Package web contiene le pagine incorporate nell'eseguibile.
package web

import "embed"

// FS contiene pagine, script, stili e icone.
//
//go:embed *.html *.js *.css rnnoise/*.js
var FS embed.FS
