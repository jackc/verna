package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jackc/verna/internal/server"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var (
		slot   string
		follow bool
		lines  int
	)

	cmd := &cobra.Command{
		Use:   "logs [-- <extra journalctl args>]",
		Short: "View application logs from journald",
		Long: `View application logs via journalctl over SSH.

By default, shows logs from both slots interleaved by timestamp.
Use --slot to filter to a single slot.

Extra arguments after -- are passed through to journalctl:
  verna app --app myapp logs -- --since "1 hour ago"
  verna app --app myapp logs -- --grep "panic" --priority err`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, err := requireApp()
			if err != nil {
				return err
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

			if _, err := lookupApp(state, appName); err != nil {
				return err
			}

			// Build journalctl command.
			var cmdParts []string
			cmdParts = append(cmdParts, "journalctl")

			if slot != "" {
				if slot != "blue" && slot != "green" {
					return fmt.Errorf("--slot must be \"blue\" or \"green\"")
				}
				cmdParts = append(cmdParts, fmt.Sprintf("-u %s@%s.service", appName, slot))
			} else {
				// Both slots interleaved by timestamp.
				cmdParts = append(cmdParts, fmt.Sprintf("-u %s@blue.service", appName))
				cmdParts = append(cmdParts, fmt.Sprintf("-u %s@green.service", appName))
			}

			cmdParts = append(cmdParts, fmt.Sprintf("-n %d", lines))

			if follow {
				cmdParts = append(cmdParts, "-f")
			} else {
				cmdParts = append(cmdParts, "--no-pager")
			}

			// Pass through any extra args.
			cmdParts = append(cmdParts, args...)

			journalCmd := strings.Join(cmdParts, " ")

			if follow {
				// Stream output until interrupted.
				return client.RunStreaming(journalCmd, os.Stdout, os.Stderr)
			}

			output, err := client.Run(journalCmd)
			if err != nil {
				return fmt.Errorf("reading logs: %w", err)
			}
			fmt.Print(output)
			return nil
		},
	}

	cmd.Flags().StringVar(&slot, "slot", "", "show logs for a specific slot (blue or green; default: both)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "number of recent log lines to show")

	return cmd
}
