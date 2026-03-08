package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/verna/internal/server"
	"github.com/jackc/verna/internal/ssh"
	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "env",
		Short: "Manage application environment variables",
	}
}

func newEnvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app>",
		Short: "List all environment variables",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("reading server state: %w", err)
			}

			app, err := lookupApp(state, appName)
			if err != nil {
				return err
			}

			keys := make([]string, 0, len(app.Env))
			for k := range app.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				fmt.Printf("%s=%s\n", k, app.Env[k])
			}
			return nil
		},
	}
}

func newEnvGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <app> <key>",
		Short: "Get the value of an environment variable",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			key := args[1]

			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("reading server state: %w", err)
			}

			app, err := lookupApp(state, appName)
			if err != nil {
				return err
			}

			val, ok := app.Env[key]
			if !ok {
				return fmt.Errorf("env var %q not set for app %q", key, appName)
			}

			fmt.Println(val)
			return nil
		},
	}
}

func newEnvSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <app> KEY=VAL [KEY2=VAL2 ...]",
		Short: "Set one or more environment variables",
		Long:  "Sets environment variables and restarts the active slot if deployed.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			// Parse KEY=VAL pairs.
			pairs := make(map[string]string)
			for _, arg := range args[1:] {
				idx := strings.IndexByte(arg, '=')
				if idx < 0 {
					return fmt.Errorf("invalid argument %q: expected KEY=VAL format", arg)
				}
				key := arg[:idx]
				val := arg[idx+1:]
				if key == "" {
					return fmt.Errorf("invalid argument %q: key cannot be empty", arg)
				}
				if key == "PORT" {
					return fmt.Errorf("PORT is reserved and managed by verna")
				}
				pairs[key] = val
			}

			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("reading server state: %w", err)
			}

			app, err := lookupApp(state, appName)
			if err != nil {
				return err
			}

			if app.Env == nil {
				app.Env = make(map[string]string)
			}
			for k, v := range pairs {
				app.Env[k] = v
				fmt.Printf("Set %s\n", k)
			}

			if err := server.WriteState(client, defaultRootDir, state); err != nil {
				return fmt.Errorf("writing server state: %w", err)
			}

			return syncEnvToActiveSlot(client, defaultRootDir, appName, app)
		},
	}
}

func newEnvUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <app> KEY [KEY2 ...]",
		Short: "Remove one or more environment variables",
		Long:  "Removes environment variables and restarts the active slot if deployed.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			keys := args[1:]

			for _, key := range keys {
				if key == "PORT" {
					return fmt.Errorf("PORT is reserved and managed by verna")
				}
			}

			client, err := connectToServer()
			if err != nil {
				return err
			}
			defer client.Close()

			state, err := server.ReadState(client, defaultRootDir)
			if err != nil {
				return fmt.Errorf("reading server state: %w", err)
			}

			app, err := lookupApp(state, appName)
			if err != nil {
				return err
			}

			for _, key := range keys {
				if _, ok := app.Env[key]; !ok {
					fmt.Printf("Warning: %s is not set\n", key)
				} else {
					delete(app.Env, key)
					fmt.Printf("Unset %s\n", key)
				}
			}

			if err := server.WriteState(client, defaultRootDir, state); err != nil {
				return fmt.Errorf("writing server state: %w", err)
			}

			return syncEnvToActiveSlot(client, defaultRootDir, appName, app)
		},
	}
}

func lookupApp(state *server.ServerState, appName string) (*server.AppState, error) {
	app, exists := state.Apps[appName]
	if !exists {
		return nil, fmt.Errorf("app %q not found (run `verna app init %s` first)", appName, appName)
	}
	return app, nil
}

func syncEnvToActiveSlot(client *ssh.Client, rootDir, appName string, app *server.AppState) error {
	if app.ActiveSlot == "" {
		fmt.Println("No active deployment yet; env vars saved to config only.")
		return nil
	}

	slot := app.Slots[app.ActiveSlot]
	slotDir := fmt.Sprintf("%s/apps/%s/slots/%s", rootDir, appName, app.ActiveSlot)

	// Check that the slot symlink actually exists.
	if _, err := client.Run(fmt.Sprintf("test -L %s", slotDir)); err != nil {
		fmt.Printf("Warning: slot %s symlink does not exist; env vars saved to config only.\n", app.ActiveSlot)
		return nil
	}

	if err := server.WriteRuntimeEnv(client, rootDir, appName, app.ActiveSlot, slot.Port, app.Env); err != nil {
		return fmt.Errorf("writing runtime.env: %w", err)
	}

	unitName := fmt.Sprintf("%s@%s.service", appName, app.ActiveSlot)
	fmt.Printf("Restarting %s...\n", unitName)
	if _, err := client.Run(fmt.Sprintf("systemctl restart %s", unitName)); err != nil {
		return fmt.Errorf("restarting %s: %w", unitName, err)
	}

	fmt.Println("Done.")
	return nil
}
