// `sapien semantic ...` (PLAN §34f item 5): configure and control semantic
// search through engine.SettingsAPI, so every subcommand works the same way
// against a running daemon (live apply, every other open workspace picks it
// up too) and with no daemon (writes config; applies to the in-process
// engine this one invocation opens).
package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/gs-sinha/sapien/internal/config"
	"github.com/gs-sinha/sapien/internal/domain"
	"github.com/gs-sinha/sapien/internal/engine"
)

func init() { Register(newSemanticCmd) }

func newSemanticCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "semantic",
		Short: "Configure and control semantic search",
	}
	cmd.AddCommand(
		newSemanticStatusCmd(app),
		newSemanticEnableCmd(app),
		newSemanticDisableCmd(app),
		newSemanticReindexCmd(app),
		newSemanticTestCmd(app),
	)
	return cmd
}

func newSemanticStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show semantic search's configuration and live status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			out, err := eng.Settings().GetSemantic(cmd.Context())
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(out)
			}
			printSemanticSettings(app.Printer, out)
			return nil
		},
	}
}

// printSemanticSettings renders `semantic status`'s human output.
func printSemanticSettings(p *Printer, s *domain.SemanticSettings) {
	if !s.Enabled {
		p.Line("semantic search: off (%s config)", s.Source)
		return
	}
	p.Line("semantic search: %s -- %s/%s (%s config)", s.Status.State, s.Kind, s.Model, s.Source)
	if s.BaseURL != "" {
		p.Line("base url: %s", s.BaseURL)
	}
	p.Line("embedded %d/%d", s.Status.Embedded, s.Status.Total)
	if s.Status.Error != "" {
		p.Line("%s", p.Dim("last error: "+s.Status.Error))
	}
}

func newSemanticEnableCmd(app *App) *cobra.Command {
	var kind, model, baseURL, apiKey string
	var workspaceScope, force bool
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Turn on semantic search",
		Long: "Validates the config by trying one embed call before saving (like `sapien semantic test`), " +
			"writes it to the user config by default, and applies it live -- no daemon restart needed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			req := engine.SemanticPutRequest{Enabled: true, Kind: kind, Model: model, BaseURL: baseURL, Force: force}
			if workspaceScope {
				req.Scope = "workspace"
			}
			if cmd.Flags().Changed("api-key") {
				req.APIKey = &apiKey
			}

			out, err := eng.Settings().PutSemantic(cmd.Context(), req)
			if err != nil {
				return err
			}
			out, err = waitForSemanticIdle(cmd.Context(), eng)
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(out)
			}
			app.Printer.Line("semantic search enabled: %s/%s (embedded %d/%d)", out.Kind, out.Model, out.Status.Embedded, out.Status.Total)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "embedding provider: openai or ollama")
	cmd.Flags().StringVar(&model, "model", "", "embedding model name")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "embedding endpoint base URL (default: http://127.0.0.1:11434 for ollama)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", `API key, or "${env.NAME}" to read one from the environment at use time`)
	cmd.Flags().BoolVar(&workspaceScope, "workspace-scope", false, "write to this workspace's config instead of the user config")
	cmd.Flags().BoolVar(&force, "force", false, "save even if the connectivity test fails")
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagRequired("model")
	return cmd
}

func newSemanticDisableCmd(app *App) *cobra.Command {
	var workspaceScope bool
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Turn off semantic search (search falls back to lexical-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			// PUT always carries the full config, so the stored kind/model/
			// base_url/batch_size are resubmitted unchanged: disabling must
			// not wipe them, since re-enabling later should remember them.
			current, err := eng.Settings().GetSemantic(cmd.Context())
			if err != nil {
				return err
			}
			req := engine.SemanticPutRequest{
				Enabled: false, Kind: current.Kind, Model: current.Model,
				BaseURL: current.BaseURL, BatchSize: current.BatchSize,
			}
			if workspaceScope {
				req.Scope = "workspace"
			}
			out, err := eng.Settings().PutSemantic(cmd.Context(), req)
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(out)
			}
			app.Printer.Line("semantic search disabled")
			return nil
		},
	}
	cmd.Flags().BoolVar(&workspaceScope, "workspace-scope", false, "write to this workspace's config instead of the user config")
	return cmd
}

func newSemanticReindexCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild the semantic vector index from scratch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.Settings().ReindexSemantic(cmd.Context()); err != nil {
				return err
			}
			out, err := waitForSemanticIdle(cmd.Context(), eng)
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(out)
			}
			app.Printer.Line("reindexed: embedded %d/%d (%s)", out.Status.Embedded, out.Status.Total, out.Status.State)
			return nil
		},
	}
}

func newSemanticTestCmd(app *App) *cobra.Command {
	var kind, model, baseURL, apiKey string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Try an embedding config without saving it",
		Long:  "With no flags, tests the currently configured provider. Any flag given overrides just that field for a one-off check.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := app.Workspace()
			if err != nil {
				return err
			}
			// Read the config directly (rather than through GetSemantic,
			// which never returns the api_key): probing "the currently
			// configured provider" needs the real key, and this process
			// already has the same filesystem access the engine itself
			// would use to read it.
			cfg, err := config.Load(ws)
			if err != nil {
				return err
			}
			probe := domain.SemanticProbe{
				Kind: cfg.Semantic.Kind, Model: cfg.Semantic.Model,
				BaseURL: cfg.Semantic.BaseURL, APIKey: cfg.Semantic.APIKey,
				BatchSize: cfg.Semantic.BatchSize,
			}
			if cmd.Flags().Changed("kind") {
				probe.Kind = kind
			}
			if cmd.Flags().Changed("model") {
				probe.Model = model
			}
			if cmd.Flags().Changed("base-url") {
				probe.BaseURL = baseURL
			}
			if cmd.Flags().Changed("api-key") {
				probe.APIKey = apiKey
			}

			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			out, err := eng.Settings().TestSemantic(cmd.Context(), probe)
			if err != nil {
				return err
			}
			if app.Printer.IsJSON() {
				return app.Printer.JSON(out)
			}
			if out.OK {
				app.Printer.Line("ok: dim=%d, %dms", out.Dim, out.LatencyMS)
			} else {
				app.Printer.Line("failed: %s", out.Error)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "embedding provider: openai or ollama")
	cmd.Flags().StringVar(&model, "model", "", "embedding model name")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "embedding endpoint base URL")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key for this test only")
	return cmd
}

// waitForSemanticIdle polls GetSemantic until its status leaves "indexing"
// (or ctx is done, or a generous timeout elapses), so a one-shot CLI
// process -- which closes its engine, and for an in-process engine.Local
// its database, as soon as the command returns -- reports the outcome of a
// reindex it just triggered instead of racing it. Cheap when nothing is
// indexing: it returns on the first check.
func waitForSemanticIdle(ctx context.Context, eng engine.Engine) (*domain.SemanticSettings, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		out, err := eng.Settings().GetSemantic(ctx)
		if err != nil {
			return nil, err
		}
		if out.Status.State != domain.SemanticIndexing || time.Now().After(deadline) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}
