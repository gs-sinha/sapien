package cli

import (
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/growsimplee/sapien/internal/diagnose"
	"github.com/growsimplee/sapien/internal/domain"
	"github.com/growsimplee/sapien/internal/engine"
	"github.com/growsimplee/sapien/internal/errs"
)

func init() { Register(newCallCmd) }

func newCallCmd(app *App) *cobra.Command {
	var params []string
	var headers []string
	var body string
	var inputs []string
	var allowProduction bool
	var saveAs string
	var exampleID string
	var saveExample, saveExampleScope, saveExampleDescription string
	var saveExampleTags []string

	cmd := &cobra.Command{
		Use:   "call <operation-ref>",
		Short: "Call one operation directly, as a one-step run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := app.Engine()
			if err != nil {
				return err
			}
			defer eng.Close()

			envName, err := app.EnvironmentName()
			if err != nil {
				return err
			}
			if err := checkProduction(cmd.Context(), eng, envName, allowProduction); err != nil {
				return err
			}

			paramMap, err := parseParams(params)
			if err != nil {
				return err
			}
			headerMap, err := parseHeaders(headers)
			if err != nil {
				return err
			}
			bodyVal, err := parseBody(body)
			if err != nil {
				return err
			}
			inputMap, err := parseParams(inputs)
			if err != nil {
				return err
			}
			if len(inputMap) > 0 && exampleID == "" {
				return errs.New(errs.Invalid, "call: --input only applies with --example").
					WithHint("pass --example <id>, or drop -i/--input")
			}

			finalParams, finalBody, finalHeaders := paramMap, bodyVal, headerMap

			var run *domain.Run
			var callErr error
			var yamlForSave string

			if exampleID != "" {
				ex, gerr := eng.Examples().Get(cmd.Context(), exampleID)
				if gerr != nil {
					return gerr
				}
				if ex.Operation != args[0] {
					return errs.New(errs.Invalid, "operation %q does not match example %q's operation %q",
						args[0], exampleID, ex.Operation).
						WithHint("call the example's own operation, or pick a different --example")
				}

				finalParams = mergeAnyMaps(ex.Input, paramMap)
				finalHeaders = mergeStringMaps(ex.Headers, headerMap)
				if bodyVal != nil {
					finalBody = bodyVal
				} else {
					finalBody = ex.Body
				}

				flowID := saveAs
				if flowID == "" {
					flowID = "call-" + exampleID
				}
				yamlSrc, rerr := renderCallFlowYAML(flowID, args[0], finalParams, finalBody, finalHeaders)
				if rerr != nil {
					return rerr
				}
				yamlForSave = yamlSrc

				opts := engine.RunOptions{
					Environment:     envName,
					Inputs:          inputMap,
					AllowProduction: allowProduction,
					Trigger:         "cli",
				}
				run, callErr = eng.Runner().RunFlowSource(cmd.Context(), yamlSrc, opts)
			} else {
				req := engine.CallRequest{
					Operation:       args[0],
					Params:          finalParams,
					Body:            finalBody,
					Headers:         finalHeaders,
					Env:             envName,
					AllowProduction: allowProduction,
					Trigger:         "cli",
				}
				run, callErr = eng.Runner().Call(cmd.Context(), req)
			}
			if callErr != nil {
				return callErr
			}

			var savedPath string
			if saveAs != "" {
				yamlSrc := yamlForSave
				if yamlSrc == "" {
					var rerr error
					yamlSrc, rerr = renderCallFlowYAML(saveAs, args[0], finalParams, finalBody, finalHeaders)
					if rerr != nil {
						return rerr
					}
				}
				flow, cerr := eng.Flows().Create(cmd.Context(), yamlSrc, saveAs+".flow.yaml")
				if cerr != nil {
					return cerr
				}
				savedPath = flow.Path
				if savedPath == "" {
					savedPath = saveAs + ".flow.yaml"
				}
			}

			var savedExample *domain.SavedExample
			if saveExample != "" {
				created, ferr := eng.Examples().FromRun(cmd.Context(), engine.ExampleFromRun{
					RunID:       run.ID,
					ID:          saveExample,
					Description: saveExampleDescription,
					Scope:       domain.ExampleScope(saveExampleScope),
					Tags:        saveExampleTags,
					Source:      &domain.MemorySource{Kind: "user"},
				})
				if ferr != nil {
					return ferr
				}
				savedExample = created
			}

			hints := diagnose.Run(cmd.Context(), eng, run)
			if app.Printer.IsJSON() {
				out := runWithExample{runWithHints: runWithHints{Run: *run, Hints: hints}, Example: savedExample}
				if jerr := app.Printer.JSON(out); jerr != nil {
					return jerr
				}
			} else {
				printCallHuman(app.Printer, run)
				printHints(app.Printer, hints)
				if savedPath != "" {
					app.Printer.Line("")
					app.Printer.Line("saved %s", savedPath)
				}
				if savedExample != nil {
					app.Printer.Line("")
					app.Printer.Line("%s", exampleSavedLine(savedExample))
					printExampleUseHint(app, savedExample)
				}
			}
			return runExitError(run)
		},
	}

	cmd.Flags().StringArrayVarP(&params, "param", "p", nil, "operation parameter key=value; JSON-decoded when it parses (repeatable)")
	cmd.Flags().StringVar(&body, "body", "", "request body: inline JSON, or @file to read it from a file")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "extra request header key:value (repeatable)")
	cmd.Flags().BoolVar(&allowProduction, "allow-production", false, "allow calling a production environment")
	cmd.Flags().StringVar(&saveAs, "save-as", "", "save this call as a one-step flow with this id")
	cmd.Flags().StringVar(&exampleID, "example", "", "load a saved example as the base input/body/headers for this call")
	cmd.Flags().StringArrayVarP(&inputs, "input", "i", nil, "template input key=value for an --example body's ${inputs.x} (repeatable)")
	cmd.Flags().StringVar(&saveExample, "save-example", "", "save the result of this call as a verified example with this id")
	cmd.Flags().StringVar(&saveExampleScope, "scope", "", "scope for --save-example: workspace|service (default workspace)")
	cmd.Flags().StringVar(&saveExampleDescription, "description", "", "description for --save-example")
	cmd.Flags().StringArrayVar(&saveExampleTags, "tag", nil, "tag for --save-example (repeatable)")
	return cmd
}

// runWithExample extends runWithHints (format_run.go) with the example
// `sapien call --save-example` wrote, for --json output. domain.Run and
// Hints are promoted to the same top level runWithHints already gives
// them; Example is simply one more field, omitted when --save-example
// was not passed.
type runWithExample struct {
	runWithHints
	Example *domain.SavedExample `json:"example,omitempty"`
}

// mergeAnyMaps returns a new map holding base's entries with override's
// laid on top (override wins on a key collision). Either may be nil; a
// nil result means both were empty, matching the omitempty shape the
// callers below feed into YAML/JSON.
func mergeAnyMaps(base, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeStringMaps is mergeAnyMaps for map[string]string (headers).
func mergeStringMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// savedFlow and savedFlowStep are the minimal shape `sapien call --save-as`
// (and `--example`) render to YAML (PLAN §21: "version 1, id, steps[0]{id:
// call, call, input, body}", extended here with headers so an example's
// headers survive into the saved flow). They mirror domain.Flow/domain.Step's
// yaml tags without pulling in fields a one-step call never has (assertions,
// extract, ...).
type savedFlow struct {
	Version int             `yaml:"version"`
	ID      string          `yaml:"id"`
	Steps   []savedFlowStep `yaml:"steps"`
}

type savedFlowStep struct {
	ID      string            `yaml:"id"`
	Call    string            `yaml:"call"`
	Input   map[string]any    `yaml:"input,omitempty"`
	Body    any               `yaml:"body,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
}

func renderCallFlowYAML(flowID, opRef string, params map[string]any, body any, headers map[string]string) (string, error) {
	sf := savedFlow{
		Version: 1,
		ID:      flowID,
		Steps:   []savedFlowStep{{ID: "call", Call: opRef, Input: params, Body: body, Headers: headers}},
	}
	data, err := yaml.Marshal(sf)
	if err != nil {
		return "", errs.Wrap(errs.Internal, err, "rendering flow yaml")
	}
	return string(data), nil
}
