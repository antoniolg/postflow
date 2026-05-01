package api

import _ "embed"

//go:embed templates/schedule.html
var scheduleHTMLTemplate string

//go:embed templates/login.html
var loginHTMLTemplate string

//go:embed templates/authorize.html
var authorizeHTMLTemplate string
