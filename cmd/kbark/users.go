// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shivangtanwar/kbark/internal/auth"
)

var (
	usersName string
	usersRole string
)

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage sign-in for the autofix console",
}

var usersHashCmd = &cobra.Command{
	Use:   "hash <username>",
	Short: "Print a users-file entry with a bcrypt hash of a password you type",
	Long: `hash reads a password (from the terminal without echo, or from stdin
when piped) and prints a ready-to-paste entry for the --auth-file users
file. kbark never stores or prints the password itself.

  kbark users hash asha --name "Asha Rao" --role approver >> users.yaml

Roles: viewer (read only), approver (approve, reject, roll back),
admin (approver, and can see the user list).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch usersRole {
		case auth.RoleViewer, auth.RoleApprover, auth.RoleAdmin:
		default:
			return fmt.Errorf("--role %q: want viewer, approver or admin", usersRole)
		}
		password, err := readPassword("Password (at least 10 characters): ")
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		name := usersName
		if name == "" {
			name = args[0]
		}
		out := cmd.OutOrStdout()
		if stat, _ := os.Stdout.Stat(); stat != nil && stat.Size() == 0 && !term.IsTerminal(int(os.Stdout.Fd())) {
			// First entry of a new file: write the list header too.
			fmt.Fprintln(out, "users:")
		} else if term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Fprintln(out, "# Add under the top-level `users:` key of your users file:")
		}
		fmt.Fprintf(out, "  - username: %s\n    name: %q\n    role: %s\n    password_hash: %q\n", strings.ToLower(args[0]), name, usersRole, hash)
		return nil
	},
}

func init() {
	usersHashCmd.Flags().StringVar(&usersName, "name", "", "Display name recorded in the audit log (default: the username)")
	usersHashCmd.Flags().StringVar(&usersRole, "role", auth.RoleApprover, "viewer, approver or admin")
	usersCmd.AddCommand(usersHashCmd)
	rootCmd.AddCommand(usersCmd)
}

// readPassword reads a secret from the terminal without echo, or one
// line from stdin when it is not a terminal.
func readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, prompt)
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(raw), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
