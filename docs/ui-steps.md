# UI steps

A flow can drive a mobile app as well as call APIs. A step with `ui:` runs
actions against an Android app on a local emulator, so one flow can
create data through an API, check it on the app's screen, act on it
there, and assert on the API calls the app itself made, passing values
between API and UI steps through `steps.<id>` like any other step.

```yaml
version: 1
id: rider-sees-trip
steps:
  - id: login
    ui:
      app: rider
      actions:
        - launch: {clear_state: true}
        - type: {id: text_form_field, text: "8445544024"}
        - tap: {xpath: "//android.widget.Button"}
        - type: {hint: "000000", text: "000000"}
        - wait_for: {hint: "000000", gone: true, timeout: 30s}
    assert:
      - "body.logs.api.exists(c, c.path.endsWith('/auth') && c.status == 200)"

  - id: assign
    call: logistic.assignTrip            # any API step
    body: {riderPhone: "8445544024"}

  - id: card
    ui:
      app: rider
      actions:
        - scroll_to: {id: "task_card_${steps.assign.body.tripId}"}
        - tap: {id: "task_card_${steps.assign.body.tripId}"}
        - assert_text: {id: trip_status, eq: Assigned}
```

The full action and selector reference is `sapien flow reference ui`
(MCP: `get_dsl_reference("ui")`).

## Setting up a machine

```
sapien device doctor
```

checks, in order, Node (20.19+/22.12+ for Appium 3), Appium and its
`uiautomator2` driver, `ANDROID_HOME`, a JDK on `JAVA_HOME` (the driver
signs its helper APKs with it), adb, and a device or AVD, and prints the
command that fixes each gap. Typically:

```
npm install -g appium
appium driver install uiautomator2
export ANDROID_HOME=$HOME/Library/Android/sdk
export JAVA_HOME=$(/usr/libexec/java_home -v 17)
```

You do not need to start Appium or the emulator yourself: the first ui
step of a run starts `appium` when nothing answers at `appium_url`
(logging to `.sapien/appium.log`, stopped when the run ends) and boots
`avd` when no device is attached (left running for the next run).

## Configuration

**Per environment**: the apps its ui steps can drive, in
`environments/<env>.yaml`, committed with the workspace:

```yaml
apps:
  rider:
    package: com.blitznow.kaptaan
    repo: ../rider_app_fe              # relative to the workspace root
    build: fvm flutter build apk --debug --flavor stag -t lib/main_stag.dart
    apk: build/app/outputs/flutter-apk/app-stag-debug.apk
    log_format: rider-box              # rider-box | overwatch-api-logger | raw
    permissions: [ACCESS_FINE_LOCATION, CAMERA, POST_NOTIFICATIONS]
```

**Per machine**: `.sapien/ui.yaml`, never committed:

```yaml
avd: Pixel_7_API_29          # booted when no device is attached
# serial: emulator-5554      # pick one of several attached devices
# appium_url: http://127.0.0.1:4723
# auto_start_appium: true
# attach: [rider]            # apps you run yourself under `flutter run`
# repos: {rider: /path/to/rider_app_fe}
```

## How an app gets onto the device

- **Managed (default).** On a run's first use of an app, Sapien runs its
  `build` command in `repo` if the APK does not exist (or with
  `sapien flow run --rebuild`), then installs it if its SHA-256 differs from
  what it last installed on that device (`.sapien/device-state.json`).
  `sapien device build <app>` forces both.
- **Attached.** `sapien flow run <flow> --attach rider` (or `attach:` in
  ui.yaml) drives the app you are already running with `flutter run`:
  nothing is built or installed, `launch` only brings it to the front,
  and `clear_state` is skipped with a warning (it would kill your
  session). Hot-reload a fix and rerun the flow.

A debug build is needed for log assertions: both apps only log their
network calls when `kDebugMode`/`!kReleaseMode`.

## Logs

For the whole run Sapien streams `adb logcat` for the app's `log_tag`
(default `flutter`). Each ui step sees the lines logged while it ran:

- `body.logs.lines`: the lines, with the logcat header, ANSI colours, and
  the `logger` package's PrettyPrinter border/emoji stripped.
- `body.logs.api`: the network calls parsed out of those lines by the
  app's `log_format`, each `{method, url, path, status, request: {headers,
  body}, response: {body}, error}`. `rider-box` reads rider_app_fe's
  `[HTTP]`/`[DIO] API REQUEST-RESPONSE LOG` boxes; `overwatch-api-logger`
  pairs overwatch's `API LOGGER → REQUEST` with its `RESPONSE`/`ERROR` by
  URL. A new format is a parser in `internal/device/logs`.

The persisted run record redacts auth headers (and the environment's
`redaction:` paths) in both, and keeps at most the last 500 lines; the
step's `logcat.txt` artifact has all of them.

## Writing selectors

A selector is one of `id`, `text`, `text_contains`, `hint`, `xpath` (plus
`index`). In a Flutter app:

- `id` matches `Semantics(identifier: ...)`, which Flutter exposes as the
  Android resource-id; rider_app_fe's `AutomationLabel` sets it. `ValueKey`
  alone is invisible outside Dart.
- `text` matches visible text or the semantics label (most Flutter text
  surfaces as content-desc, so both are tried).
- `hint` matches a text field's hint.

`sapien device snapshot` lists every selectable element on the current
screen with the selector to write for it (`--json` for agents,
`--screenshot out.png` to see it).

## Results and artifacts

A ui step's record shows each action with its duration and outcome
(`request.body.actions`, `response.body.actions`), the step's `out`, and
its assertions (`assert_*` actions are recorded with the step's own
`assert:` results). Files go to `.sapien/artifacts/<run_id>/<step_id>/`
and are listed on the step as `artifacts`:

- `final.png`: the screen when the step ended.
- `failure.png` and `source.xml`: on a failed action, the screen and its
  UI hierarchy, which show why a selector did not match.
- `logcat.txt`: the step's log lines.
- `<name>.png`: each `screenshot` action.

## Limits (phase 1)

- Android only; the step shape is platform-neutral, so iOS (XCUITest) can
  follow without changing flows.
- One device per run; every ui step in a run shares one Appium session.
- Steps run in the local engine or the daemon on the same machine as the
  emulator.
