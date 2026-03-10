package main

import (
	"fmt"
	"regexp"

	"github.com/jackc/verna/internal/caddy"
	"github.com/jackc/verna/internal/server"
	"github.com/jackc/verna/internal/systemd"
	"github.com/spf13/cobra"
)

var (
	validAppName = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)
	flagApp      string

	// reservedNames contains system usernames and common service accounts that cannot be used as app names.
	reservedNames = map[string]struct{}{
		// Ubuntu built-in system users
		"root": {}, "daemon": {}, "bin": {}, "sys": {}, "sync": {}, "games": {}, "man": {}, "lp": {},
		"mail": {}, "news": {}, "uucp": {}, "proxy": {}, "www-data": {}, "backup": {}, "list": {},
		"irc": {}, "gnats": {}, "nobody": {}, "systemd-network": {}, "systemd-resolve": {},
		"systemd-timesync": {}, "systemd-oom": {}, "messagebus": {}, "syslog": {}, "uuidd": {},
		"tcpdump": {}, "tss": {},

		// Ubuntu desktop/server services
		"avahi": {}, "avahi-autoipd": {}, "usbmux": {}, "dnsmasq": {}, "kernoops": {},
		"cups-pk-helper": {}, "rtkit": {}, "whoopsie": {}, "sssd": {}, "speech-dispatcher": {},
		"fwupd-refresh": {}, "nm-openvpn": {}, "saned": {}, "colord": {}, "geoclue": {},
		"pulse": {}, "gnome-initial-setup": {}, "hplip": {}, "gdm": {}, "pollinate": {},
		"landscape": {}, "ubuntu": {}, "snap-daemon": {},

		// Core network services
		"sshd": {}, "ntp": {}, "chrony": {}, "ftp": {}, "telnet": {}, "dhcpd": {},
		"named": {}, "bind": {}, "postfix": {}, "dovecot": {}, "openvpn": {}, "wireguard": {},

		// Web servers and proxies
		"nginx": {}, "apache": {}, "caddy": {}, "haproxy": {}, "squid": {}, "varnish": {}, "tomcat": {},

		// Databases
		"postgres": {}, "mysql": {}, "redis": {}, "mongodb": {}, "elasticsearch": {},
		"couchdb": {}, "neo4j": {}, "cassandra": {}, "influxdb": {}, "pgbouncer": {},

		// Message queues and caches
		"rabbitmq": {}, "kafka": {}, "zookeeper": {}, "mosquitto": {}, "memcached": {},

		// Container and infrastructure
		"docker": {}, "lxd": {}, "consul": {}, "vault": {}, "nomad": {}, "etcd": {}, "minio": {},

		// CI/CD and SCM
		"git": {}, "jenkins": {}, "gitlab-runner": {},

		// Monitoring
		"grafana": {}, "prometheus": {}, "nagios": {}, "zabbix": {}, "icinga": {},
		"collectd": {}, "telegraf": {}, "node-exporter": {}, "statsd": {},

		// Logging
		"logstash": {}, "kibana": {}, "fluentd": {}, "td-agent": {},

		// Configuration management
		"puppet": {}, "ansible": {}, "chef": {}, "salt": {},

		// Security
		"clamav": {}, "fail2ban": {}, "unbound": {}, "knot": {}, "certbot": {}, "letsencrypt": {},

		// Generic sensitive names
		"admin": {}, "operator": {}, "supervisor": {},

		// Solr
		"solr": {},
	}
)

func newAppCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "app",
		Short: "Application management commands",
	}
	cmd.PersistentFlags().StringVar(&flagApp, "app", "", "application name (env: VERNA_APP)")
	return cmd
}

func requireApp() (string, error) {
	if flagApp == "" {
		return "", fmt.Errorf("--app is required (or set VERNA_APP)")
	}
	return flagApp, nil
}

func newAppInitCmd() *cobra.Command {
	var (
		domains            []string
		healthCheckPath    string
		healthCheckTimeout int
		releaseRetention   int
		execArgs           []string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize an application on the server",
		Long:  "Creates directories, system user, systemd unit, Caddy route, and registers the app in verna.json.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, err := requireApp()
			if err != nil {
				return err
			}

			// Validate app name.
			if !validAppName.MatchString(appName) {
				return fmt.Errorf("invalid app name %q: must match %s (lowercase letters, digits, hyphens; must start with a letter and not end with a hyphen)", appName, validAppName.String())
			}
			if _, reserved := reservedNames[appName]; reserved {
				return fmt.Errorf("app name %q is a reserved system username and cannot be used", appName)
			}

			// Validate domains.
			if len(domains) == 0 {
				return fmt.Errorf("at least one --domain is required")
			}

			// Connect to server.
			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			// Read server state.
			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("server not initialized (run `verna server init` first): %w", err)
			}

			// Check for duplicate app.
			if _, exists := state.Apps[appName]; exists {
				return fmt.Errorf("app %q already exists", appName)
			}

			// Allocate ports.
			bluePort := state.NextPort
			greenPort := state.NextPort + 1
			state.NextPort += 2

			systemUser := appName

			// Create directory structure.
			fmt.Printf("Creating directories for %s...\n", appName)
			appDir := fmt.Sprintf("%s/apps/%s", defaultRootDir, appName)
			if _, err := client.Run(fmt.Sprintf("mkdir -p %s/releases %s/slots %s/shared", appDir, appDir, appDir)); err != nil {
				return fmt.Errorf("creating app directories: %w", err)
			}

			// Create or verify system user.
			_, userErr := client.Run(fmt.Sprintf("id %s >/dev/null 2>&1", systemUser))
			if userErr == nil {
				// User exists — verify matching group exists.
				if _, err := client.Run(fmt.Sprintf("getent group %s >/dev/null 2>&1", systemUser)); err != nil {
					return fmt.Errorf("system user %q exists but has no matching group %q", systemUser, systemUser)
				}
				fmt.Printf("Using existing system user %s.\n", systemUser)
			} else {
				// User does not exist — create with matching group.
				fmt.Printf("Creating system user %s...\n", systemUser)
				if _, err := client.Run(fmt.Sprintf("useradd --system --user-group --home %s --shell /usr/sbin/nologin %s", appDir, systemUser)); err != nil {
					return fmt.Errorf("creating system user: %w", err)
				}
			}

			// Set ownership of shared directory.
			if _, err := client.Run(fmt.Sprintf("chown %s:%s %s/shared", systemUser, systemUser, appDir)); err != nil {
				return fmt.Errorf("setting shared directory ownership: %w", err)
			}

			// Generate and install systemd template unit.
			fmt.Println("Installing systemd unit...")
			unitContent, err := systemd.GenerateTemplateUnit(systemd.UnitConfig{
				AppName:  appName,
				User:     systemUser,
				Group:    systemUser,
				RootDir:  defaultRootDir,
				ExecArgs: execArgs,
			})
			if err != nil {
				return fmt.Errorf("generating systemd unit: %w", err)
			}

			unitPath := fmt.Sprintf("/etc/systemd/system/%s@.service", appName)
			writeUnitCmd := fmt.Sprintf("cat <<'UNIT_EOF' | tee %s > /dev/null\n%sUNIT_EOF", unitPath, unitContent)
			if _, err := client.Run(writeUnitCmd); err != nil {
				return fmt.Errorf("writing systemd unit: %w", err)
			}

			if _, err := client.Run("systemctl daemon-reload"); err != nil {
				return fmt.Errorf("reloading systemd: %w", err)
			}

			// Configure Caddy route.
			fmt.Println("Configuring Caddy route...")
			if err := caddy.EnsureVernaCaddyServer(client); err != nil {
				fmt.Printf("  Warning: could not configure Caddy server: %v\n", err)
				fmt.Println("  Caddy route will be configured on first deploy.")
			} else if err := caddy.AddAppRoute(client, caddy.RouteConfig{
				AppName: appName,
				Domains: domains,
				Port:    bluePort,
			}); err != nil {
				fmt.Printf("  Warning: could not add Caddy route: %v\n", err)
				fmt.Println("  Caddy route will be configured on first deploy.")
			}

			// Register app in state.
			state.Apps[appName] = &server.AppState{
				Domains:            domains,
				HealthCheckPath:    healthCheckPath,
				HealthCheckTimeout: healthCheckTimeout,
				ReleaseRetention:   releaseRetention,
				User:               systemUser,
				Group:              systemUser,
				ExecArgs:           execArgs,
				ActiveSlot:         "",
				Slots: map[string]server.SlotState{
					"blue":  {Port: bluePort},
					"green": {Port: greenPort},
				},
			}

			// Write updated state.
			if err := server.WriteState(client, defaultRootDir, state); err != nil {
				return fmt.Errorf("writing server state: %w", err)
			}

			fmt.Printf("\nApp %s initialized:\n", appName)
			fmt.Printf("  Domains:    %v\n", domains)
			fmt.Printf("  Blue port:  %d\n", bluePort)
			fmt.Printf("  Green port: %d\n", greenPort)
			fmt.Printf("  User:       %s\n", systemUser)
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&domains, "domain", nil, "domain name for the app (repeatable, at least one required)")
	cmd.Flags().StringVar(&healthCheckPath, "health-check-path", "/health", "health check endpoint path")
	cmd.Flags().IntVar(&healthCheckTimeout, "health-check-timeout", 15, "health check timeout in seconds")
	cmd.Flags().IntVar(&releaseRetention, "release-retention", 5, "number of releases to retain")
	cmd.Flags().StringArrayVar(&execArgs, "exec-arg", nil, "argument to append to the binary in ExecStart (repeatable)")

	return cmd
}
