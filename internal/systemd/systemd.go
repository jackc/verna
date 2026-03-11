package systemd

import (
	"bytes"
	"strings"
	"text/template"
)

type UnitConfig struct {
	AppName    string
	User       string
	Group      string
	RootDir    string
	ExecPath   string // relative path to executable within artifact dir (e.g. "bin/myapp")
	ExecArgs   []string
}

func (c UnitConfig) ExecArgsSuffix() string {
	if len(c.ExecArgs) == 0 {
		return ""
	}
	return " " + strings.Join(c.ExecArgs, " ")
}

var templateUnitTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description={{.AppName}} (%i)
After=network.target

[Service]
Type=simple
User={{.User}}
Group={{.Group}}
WorkingDirectory={{.RootDir}}/apps/{{.AppName}}/slots/%i
EnvironmentFile=-{{.RootDir}}/apps/{{.AppName}}/slots/%i/env/runtime.env
ExecStart={{.RootDir}}/apps/{{.AppName}}/slots/%i/{{.ExecPath}}{{.ExecArgsSuffix}}
Environment=VERNA_APP={{.AppName}}
Environment=VERNA_SLOT=%i
Restart=always
RestartSec=2
StandardOutput=journal
StandardError=journal
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths={{.RootDir}}/apps/{{.AppName}}/shared

[Install]
WantedBy=multi-user.target
`))

func GenerateTemplateUnit(cfg UnitConfig) (string, error) {
	var buf bytes.Buffer
	if err := templateUnitTmpl.Execute(&buf, cfg); err != nil {
		return "", err
	}
	return buf.String(), nil
}
