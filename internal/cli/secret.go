package cli

import (
	"bufio"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/errs"
)

func init() { Register(newSecretCmd) }

func newSecretCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage workspace secrets (PLAN §20): values are never printed",
	}
	cmd.AddCommand(newSecretSetCmd(app), newSecretListCmd(app), newSecretRmCmd(app))
	return cmd
}

func newSecretSetCmd(app *App) *cobra.Command {
	var value string
	var useStdin bool

	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Set a secret's value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			var val string
			switch {
			case value != "":
				val = value
			case useStdin:
				line, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil && err != io.EOF {
					return errs.Wrap(errs.Internal, err, "reading secret value from stdin")
				}
				val = strings.TrimRight(line, "\r\n")
			default:
				return errs.New(errs.Invalid, "no secret value given for %q", name).
					WithHint("pass --value <secret>, or --stdin and pipe the value in (an interactive, no-echo prompt isn't supported by this build)")
			}
			if val == "" {
				return errs.New(errs.Invalid, "secret value for %q is empty", name).
					WithHint("pass --value <secret> or --stdin")
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.Envs().SetSecret(cmd.Context(), name, val); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"set": name})
			}
			app.Printer.Line("secret %s set", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&value, "value", "", "secret value (prefer --stdin on a shared machine)")
	cmd.Flags().BoolVar(&useStdin, "stdin", false, "read the secret value from stdin (its first line)")
	return cmd
}

func newSecretListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secret names (values are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			names, err := eng.Envs().ListSecrets(cmd.Context())
			if err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(names)
			}
			rows := make([][]string, 0, len(names))
			for _, n := range names {
				rows = append(rows, []string{n})
			}
			app.Printer.Table([]string{"NAME"}, rows)
			return nil
		},
	}
}

func newSecretRmCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.Envs().DeleteSecret(cmd.Context(), args[0]); err != nil {
				return err
			}

			if app.Printer.IsJSON() {
				return app.Printer.JSON(map[string]string{"removed": args[0]})
			}
			app.Printer.Line("removed %s", args[0])
			return nil
		},
	}
}
