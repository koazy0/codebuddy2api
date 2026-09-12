package web

import _ "embed"

//go:embed index.html
var IndexHTML []byte

//go:embed app.css
var AppCSS []byte

//go:embed app.js
var AppJS []byte
