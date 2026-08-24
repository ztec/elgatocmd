// Package packaging embeds the privileged files required by the self-installer.
package packaging

import _ "embed"

//go:embed 99-elgato-key-light-neo.rules
var UdevRule string

//go:embed elgatolight-user.service.tmpl
var UserServiceTemplate string

//go:embed elgatolight-system.service.tmpl
var SystemServiceTemplate string
